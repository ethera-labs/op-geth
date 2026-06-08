package txpool

import (
	"bytes"
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// mailboxWriteSelector is the 4-byte selector of the v1 mailbox outbox call
// write(uint256 chainMessageRecipient, address, uint256, bytes, bytes). The
// first ABI word of its calldata is the destination chain ID.
var mailboxWriteSelector = crypto.Keccak256([]byte("write(uint256,address,uint256,bytes,bytes)"))[:4]

// permissionFilterAPI resolves institutional permission rules for a sender. It
// is backed by a snapshot streamed from the admin backend.
type permissionFilterAPI interface {
	// SenderDisabled reports whether from belongs to a disabled entity, whose
	// transactions are blocked outright.
	SenderDisabled(from common.Address) bool

	// CanDeployContract reports whether a managed sender may deploy contracts.
	// known is false when from is not managed, in which case the sequencer
	// applies no restrictions.
	CanDeployContract(from common.Address) (allowed, known bool)

	// ChainReachable reports whether from may send a cross-chain message to
	// destChainID.
	ChainReachable(from common.Address, destChainID uint64) bool

	// LocalMailbox returns the local mailbox contract address used to recognise
	// cross-chain message writes. ok is false when no mailbox is configured.
	LocalMailbox() (addr common.Address, ok bool)
}

// permissionFilter enforces per-entity rollup permissions before a transaction
// is admitted to the pool: blocking disabled entities outright, blocking
// contract deployment, and restricting cross-chain messaging to whitelisted
// chain IDs.
type permissionFilter struct {
	api      permissionFilterAPI
	signer   types.Signer
	failOpen bool
}

// NewPermissionFilter creates an IngressFilter that enforces institutional
// permissions sourced from api. When failOpen is true, transactions whose
// sender cannot be recovered are admitted rather than dropped.
func NewPermissionFilter(api permissionFilterAPI, chainID *big.Int, failOpen bool) IngressFilter {
	return &permissionFilter{
		api:      api,
		signer:   types.LatestSignerForChainID(chainID),
		failOpen: failOpen,
	}
}

// FilterTx implements IngressFilter.FilterTx.
func (f *permissionFilter) FilterTx(_ context.Context, tx *types.Transaction) bool {
	from, err := types.Sender(f.signer, tx)
	if err != nil {
		return f.failOpen
	}

	// A disabled entity may not admit any transaction.
	if f.api.SenderDisabled(from) {
		return false
	}

	// Contract deployment: a creation is decided solely by the deploy capability.
	if tx.To() == nil {
		allowed, known := f.api.CanDeployContract(from)
		if known && !allowed {
			return false
		}
		return true
	}

	// Cross-chain message: enforce the destination chain-id whitelist.
	if destChainID, ok := f.crossChainDest(tx); ok {
		if !f.api.ChainReachable(from, destChainID) {
			return false
		}
	}

	return true
}

// crossChainDest returns the destination chain ID when tx is a write to the
// local mailbox outbox, and false otherwise.
func (f *permissionFilter) crossChainDest(tx *types.Transaction) (uint64, bool) {
	mailbox, ok := f.api.LocalMailbox()
	if !ok {
		return 0, false
	}
	if to := tx.To(); to == nil || *to != mailbox {
		return 0, false
	}

	data := tx.Data()
	if len(data) < 4+32 || !bytes.Equal(data[:4], mailboxWriteSelector) {
		return 0, false
	}

	dest := new(big.Int).SetBytes(data[4:36])
	if !dest.IsUint64() {
		return 0, false
	}
	return dest.Uint64(), true
}
