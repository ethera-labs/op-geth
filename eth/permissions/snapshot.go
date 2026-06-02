// Package permissions resolves the institutional permission rules that the
// sequencer enforces at Layer 1, before a transaction is admitted to the pool.
//
// Rules originate from the admin backend and are pulled as a periodically
// refreshed snapshot (GET /api/v1/config/snapshot) so the admission hot path
// never makes a network call. The backend serves entities and rule groups as
// separate collections: an entity carries one or more wallet addresses and a
// reference to its rule group, while the engine-enforceable capabilities
// (contract deployment, cross-rollup reachability) live on the rule group. The
// cache joins the two and indexes the resolved rules by wallet address.
package permissions

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
)

const (
	// defaultRefreshInterval is how often the snapshot is re-pulled from the backend.
	defaultRefreshInterval = 10 * time.Second
	// defaultRequestTimeout bounds a single snapshot request.
	defaultRequestTimeout = 2 * time.Second
)

// snapshotResponse mirrors the admin backend's GET /api/v1/config/snapshot
// payload. Only the fields the engine enforces at Layer 1 are decoded.
type snapshotResponse struct {
	Entities   []snapshotEntity    `json:"entities"`
	RuleGroups []snapshotRuleGroup `json:"ruleGroups"`
}

// snapshotEntity is a managed actor. Its capabilities are inherited from the
// rule group referenced by RuleGroupID; an entity with no rule group (ID 0) is
// unmanaged and unrestricted.
type snapshotEntity struct {
	RuleGroupID     int                  `json:"ruleGroupId"`
	WalletAddresses []snapshotWalletAddr `json:"walletAddresses"`
}

type snapshotWalletAddr struct {
	Address string `json:"address"`
}

// snapshotRuleGroup is a named permission bundle. NetworkScope is "all" or
// "restricted"; when restricted, NetworkRollups whitelists the reachable peer
// chain IDs. CanSendTx is carried but not yet enforced (see [Rules]).
type snapshotRuleGroup struct {
	ID                int      `json:"id"`
	CanSendTx         bool     `json:"canSendTx"`
	CanDeployContract bool     `json:"canDeployContract"`
	NetworkScope      string   `json:"networkScope"`
	NetworkRollups    []uint64 `json:"networkRollups"`
}

// Rules is the resolved capability set for a single managed wallet address.
//
// CanSendTx reflects the rule group's on-rollup transaction gate. Its exact
// enforcement semantics (block all transactions vs. block native-coin value
// transfers only) are pending confirmation with the backend owner, so the
// admission filter does not yet act on it.
type Rules struct {
	CanSendTx         bool
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

// Cache holds the latest permission snapshot and keeps it fresh by polling the
// admin backend. Lookups are served from memory under a read lock.
type Cache struct {
	endpoint string
	interval time.Duration
	client   *http.Client

	mu    sync.RWMutex
	rules map[common.Address]Rules

	cancel context.CancelFunc
	done   chan struct{}
}

// NewCache constructs a Cache that polls endpoint. The poller is inert until
// Start is called.
func NewCache(endpoint string) *Cache {
	return &Cache{
		endpoint: endpoint,
		interval: defaultRefreshInterval,
		client:   &http.Client{Timeout: defaultRequestTimeout},
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

// Start performs an initial refresh and then polls in the background until
// Close is called. A failed initial refresh is logged and retried on the next
// tick; the cache simply serves an empty rule set until the backend responds.
func (c *Cache) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel

	if err := c.refresh(ctx); err != nil {
		log.Warn("Initial permission snapshot refresh failed", "endpoint", c.endpoint, "err", err)
	}

	go func() {
		defer close(c.done)
		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := c.refresh(ctx); err != nil {
					log.Warn("Permission snapshot refresh failed", "endpoint", c.endpoint, "err", err)
				}
			}
		}
	}()
}

// Close stops the background poller and waits for it to exit.
func (c *Cache) Close() {
	if c == nil || c.cancel == nil {
		return
	}
	c.cancel()
	<-c.done
}

func (c *Cache) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return fmt.Errorf("build snapshot request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch snapshot: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("snapshot endpoint returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("read snapshot body: %w", err)
	}

	var snapshot snapshotResponse
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return fmt.Errorf("decode snapshot: %w", err)
	}

	c.mu.Lock()
	c.rules = resolveRules(snapshot)
	c.mu.Unlock()
	return nil
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
		group, ok := groups[entity.RuleGroupID]
		if !ok {
			continue
		}
		entityRules := rulesFromGroup(group)
		for _, wallet := range entity.WalletAddresses {
			if !common.IsHexAddress(wallet.Address) {
				continue
			}
			rules[common.HexToAddress(wallet.Address)] = entityRules
		}
	}
	return rules
}

func rulesFromGroup(group snapshotRuleGroup) Rules {
	restricted := strings.EqualFold(group.NetworkScope, "restricted")
	var allowed map[uint64]bool
	if restricted {
		allowed = make(map[uint64]bool, len(group.NetworkRollups))
		for _, id := range group.NetworkRollups {
			allowed[id] = true
		}
	}
	return Rules{
		CanSendTx:         group.CanSendTx,
		CanDeployContract: group.CanDeployContract,
		NetworkRestricted: restricted,
		allowedChains:     allowed,
	}
}
