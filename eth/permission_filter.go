package eth

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
)

// SenderDisabled reports whether from belongs to a disabled entity, whose
// transactions are blocked outright. Unmanaged senders are never disabled.
func (s *Ethereum) SenderDisabled(from common.Address) bool {
	if s.permissionCache == nil {
		return false
	}
	rules, known := s.permissionCache.Lookup(from)
	if !known {
		return false
	}
	return !rules.Active
}

// CanDeployContract reports whether a managed sender may deploy contracts.
// known is false when from is not a managed entity, signalling the filter to
// apply no restrictions.
func (s *Ethereum) CanDeployContract(from common.Address) (allowed, known bool) {
	if s.permissionCache == nil {
		return false, false
	}
	rules, known := s.permissionCache.Lookup(from)
	if !known {
		return false, false
	}
	return rules.CanDeployContract, true
}

// ChainReachable reports whether a managed sender may send a cross-chain message
// to destChainID. Unmanaged senders are unrestricted.
func (s *Ethereum) ChainReachable(from common.Address, destChainID uint64) bool {
	if s.permissionCache == nil {
		return true
	}
	rules, known := s.permissionCache.Lookup(from)
	if !known {
		return true
	}
	return rules.ChainAllowed(destChainID)
}

// LocalMailbox returns this rollup's mailbox contract address. ok is false when
// no mailbox is configured for the local chain.
func (s *Ethereum) LocalMailbox() (common.Address, bool) {
	addr := s.APIBackend.GetMailboxAddressFromChainID(s.blockchain.Config().ChainID.Uint64())
	if addr == (common.Address{}) {
		return common.Address{}, false
	}
	return addr, true
}

// validatePermissionFilterConfig verifies the permission stream URL when one is
// configured. An empty URL disables permission filtering.
func validatePermissionFilterConfig(config *ethconfig.Config) error {
	if strings.TrimSpace(config.PermissionConfigURL) == "" {
		return nil
	}
	parsedURL, err := url.Parse(config.PermissionConfigURL)
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "ws" && parsedURL.Scheme != "wss") {
		return fmt.Errorf("invalid rollup.permissionconfigurl %q: expected a ws:// or wss:// URL", config.PermissionConfigURL)
	}
	return nil
}
