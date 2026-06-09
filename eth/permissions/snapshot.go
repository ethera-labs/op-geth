// Package permissions resolves the institutional permission rules that the
// sequencer enforces at Layer 1, before a transaction is admitted to the pool.
//
// Rules originate from the admin backend and are received over a WebSocket
// config stream (GET /api/v1/config/stream). The backend pushes a full snapshot
// on connect and again whenever the configuration changes, so the admission hot
// path serves from memory and never makes a network call. The snapshot's
// entities and rule groups are joined on ruleGroupId; each of an entity's
// wallet addresses inherits its rule group's capabilities (or, if the entity is
// disabled, a blocked rule set).
package permissions

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
	"github.com/gorilla/websocket"
)

const (
	// reconnectDelay is the wait between stream reconnection attempts.
	reconnectDelay = 3 * time.Second
	// handshakeTimeout bounds the WebSocket opening handshake.
	handshakeTimeout = 10 * time.Second
)

// streamMessage is the envelope pushed over the config stream. A message of
// type "snapshot" carries the full permission set in Data.
type streamMessage struct {
	Type string           `json:"type"`
	Data snapshotResponse `json:"data"`
}

// snapshotResponse is the permission configuration. Only the fields the engine
// enforces at Layer 1 are decoded.
type snapshotResponse struct {
	Entities   []snapshotEntity    `json:"entities"`
	RuleGroups []snapshotRuleGroup `json:"ruleGroups"`
}

// snapshotEntity is a managed actor. A disabled entity (IsActive false) is
// blocked outright; otherwise its capabilities are inherited from the rule
// group referenced by RuleGroupID. An active entity with no rule group is
// unmanaged and unrestricted.
type snapshotEntity struct {
	IsActive        bool                 `json:"isActive"`
	RuleGroupID     int                  `json:"ruleGroupId"`
	WalletAddresses []snapshotWalletAddr `json:"walletAddresses"`
}

type snapshotWalletAddr struct {
	Address string `json:"address"`
}

// snapshotRuleGroup is a named permission bundle. NetworkScope is "all" or
// "restricted"; when restricted, Rollups whitelists the reachable peer chain
// IDs.
type snapshotRuleGroup struct {
	ID                int      `json:"id"`
	CanDeployContract bool     `json:"canDeployContract"`
	NetworkScope      string   `json:"networkScope"`
	Rollups           []uint64 `json:"rollups"`
}

// Rules is the resolved capability set for a single managed wallet address.
type Rules struct {
	// Active is false when the owning entity is disabled, in which case the
	// sender is blocked from admitting any transaction.
	Active            bool
	CanDeployContract bool
	NetworkRestricted bool
	allowedChains     map[uint64]bool
}

// ChainAllowed reports whether the sender may send a cross-chain message to the
// given destination chain ID. Unrestricted senders may reach any peer.
func (r Rules) ChainAllowed(chainID uint64) bool {
	if !r.NetworkRestricted {
		return true
	}
	return r.allowedChains[chainID]
}

// Cache holds the latest permission snapshot and keeps it fresh from the config
// stream. Lookups are served from memory under a read lock.
type Cache struct {
	endpoint string

	mu    sync.RWMutex
	rules map[common.Address]Rules

	cancel context.CancelFunc
	done   chan struct{}
}

// NewCache constructs a Cache that streams from endpoint. The stream is inert
// until Start is called.
func NewCache(endpoint string) *Cache {
	return &Cache{
		endpoint: endpoint,
		rules:    make(map[common.Address]Rules),
		done:     make(chan struct{}),
	}
}

// Lookup returns the rules for addr. known is false when addr is not a managed
// entity, in which case the sequencer applies no restrictions to it.
func (c *Cache) Lookup(addr common.Address) (rules Rules, known bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	rules, known = c.rules[addr]
	return rules, known
}

// Start connects to the config stream and applies pushed snapshots in the
// background until Close is called. The cache serves an empty rule set until the
// first snapshot arrives.
func (c *Cache) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	go c.run(ctx)
}

// Close stops the stream and waits for the background goroutine to exit.
func (c *Cache) Close() {
	if c == nil || c.cancel == nil {
		return
	}
	c.cancel()
	<-c.done
}

// run maintains the stream connection, reconnecting after a fixed delay until
// the context is cancelled.
func (c *Cache) run(ctx context.Context) {
	defer close(c.done)
	for {
		if err := c.stream(ctx); err != nil && ctx.Err() == nil {
			log.Warn("Permission stream disconnected", "endpoint", c.endpoint, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(reconnectDelay):
		}
	}
}

// stream opens the WebSocket connection and applies snapshots until the
// connection drops or the context is cancelled.
func (c *Cache) stream(ctx context.Context) error {
	dialer := websocket.Dialer{HandshakeTimeout: handshakeTimeout}
	conn, _, err := dialer.DialContext(ctx, c.endpoint, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Closing the connection on cancellation unblocks the read loop below.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var msg streamMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			log.Warn("Permission stream message decode failed", "err", err)
			continue
		}
		if msg.Type != "snapshot" {
			continue
		}
		rules := resolveRules(msg.Data)
		c.mu.Lock()
		c.rules = rules
		c.mu.Unlock()
	}
}

// resolveRules joins entities to their rule groups and indexes the resulting
// capabilities by wallet address.
func resolveRules(snapshot snapshotResponse) map[common.Address]Rules {
	groups := make(map[int]snapshotRuleGroup, len(snapshot.RuleGroups))
	for _, group := range snapshot.RuleGroups {
		groups[group.ID] = group
	}

	rules := make(map[common.Address]Rules)
	for _, entity := range snapshot.Entities {
		entityRules, managed := resolveEntity(entity, groups)
		if !managed {
			continue
		}
		for _, wallet := range entity.WalletAddresses {
			if !common.IsHexAddress(wallet.Address) {
				continue
			}
			rules[common.HexToAddress(wallet.Address)] = entityRules
		}
	}
	return rules
}

// resolveEntity returns the rules for an entity. managed is false for an active
// entity with no rule group, which carries no engine-enforced restrictions.
func resolveEntity(entity snapshotEntity, groups map[int]snapshotRuleGroup) (Rules, bool) {
	if !entity.IsActive {
		return Rules{Active: false}, true
	}
	group, ok := groups[entity.RuleGroupID]
	if !ok {
		return Rules{}, false
	}
	return rulesFromGroup(group), true
}

func rulesFromGroup(group snapshotRuleGroup) Rules {
	restricted := strings.EqualFold(group.NetworkScope, "restricted")
	var allowed map[uint64]bool
	if restricted {
		allowed = make(map[uint64]bool, len(group.Rollups))
		for _, id := range group.Rollups {
			allowed[id] = true
		}
	}
	return Rules{
		Active:            true,
		CanDeployContract: group.CanDeployContract,
		NetworkRestricted: restricted,
		allowedChains:     allowed,
	}
}
