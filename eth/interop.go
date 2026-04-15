package eth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/types/interoptypes"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/eth/interop"
	"github.com/ethereum/go-ethereum/miner"
)

func (s *Ethereum) setSupervisorFailsafe(enabled bool) {
	s.supervisorFailsafe.Store(enabled)
}

func (s *Ethereum) GetSupervisorFailsafe() bool {
	return s.supervisorFailsafe.Load()
}

func (s *Ethereum) CheckAccessList(ctx context.Context, inboxEntries []common.Hash, minSafety interoptypes.SafetyLevel, execDesc interoptypes.ExecutingDescriptor) error {
	if s.interopRPC == nil {
		return errors.New("cannot check interop access list, no RPC available")
	}

	err := s.interopRPC.CheckAccessList(ctx, inboxEntries, minSafety, execDesc)

	// Detect failsafe mode and cache it in the backend
	switch err {
	case nil:
		s.setSupervisorFailsafe(false)
	case interoptypes.ErrFailsafeEnabled:
		s.setSupervisorFailsafe(true)
	}
	return err
}

func (s *Ethereum) VerifyInteropTx(ctx context.Context, tx *types.Transaction, inboxEntries []common.Hash, minSafety interoptypes.SafetyLevel, execDesc interoptypes.ExecutingDescriptor) error {
	if s.interopVerification == nil {
		return nil
	}
	if tx == nil {
		return errors.New("cannot verify interop tx: transaction is nil")
	}

	txData, err := tx.MarshalBinary()
	if err != nil {
		return fmt.Errorf("marshal interop tx: %w", err)
	}

	return s.interopVerification.VerifyTx(ctx, interop.VerificationRequest{
		TxHash:              tx.Hash(),
		TxData:              txData,
		InboxEntries:        inboxEntries,
		MinSafety:           minSafety,
		ExecutingDescriptor: execDesc,
	})
}

// QueryFailsafe queries the supervisor for the failsafe status,
// caches it in the backend, and returns the status.
func (s *Ethereum) QueryFailsafe(ctx context.Context) (bool, error) {
	if s.interopRPC == nil {
		return false, errors.New("cannot query failsafe, no RPC available")
	}

	enabled, err := s.interopRPC.GetFailsafeEnabled(ctx)
	if err != nil {
		return false, err
	}

	s.setSupervisorFailsafe(enabled)
	return enabled, nil
}

func (s *Ethereum) inferBlockTime(current *types.Header) (uint64, error) {
	if current.Number.Uint64() == 0 {
		return 0, errors.New("current head is at genesis: penultimate header is nil")
	}
	penultimate := s.BlockChain().GetHeaderByHash(current.ParentHash)
	if penultimate == nil {
		// We could use a for loop and retry, but this function is used
		// in the ingress filters, which should fail fast to maintain uptime.
		return 0, errors.New("penultimate header is nil")
	}
	return current.Time - penultimate.Time, nil
}

// CurrentInteropBlockTime returns the current block time,
// or an error if Interop is not enabled.
func (s *Ethereum) CurrentInteropBlockTime() (uint64, error) {
	chainConfig := s.APIBackend.ChainConfig()
	if !chainConfig.IsOptimism() {
		return 0, errors.New("chain is not an Optimism chain")
	}
	if chainConfig.InteropTime == nil {
		return 0, errors.New("interop time not set in chain config")
	}
	// The pending block may be aliased to the current block in op-geth. Infer the pending time instead.
	currentHeader := s.BlockChain().CurrentHeader()
	blockTime, err := s.inferBlockTime(currentHeader)
	if err != nil {
		return 0, fmt.Errorf("infer block time: %v", err)
	}
	return currentHeader.Time + blockTime, nil
}

// TxToInteropAccessList returns the interop specific access list storage keys for a transaction.
func (s *Ethereum) TxToInteropAccessList(tx *types.Transaction) []common.Hash {
	return interoptypes.TxToInteropAccessList(tx)
}

func validateInteropVerificationConfig(config *ethconfig.Config) error {
	if !config.InteropVerificationEnabled {
		return nil
	}
	if !config.InteropMempoolFiltering {
		return errors.New("interop verification requires rollup.interopmempoolfiltering")
	}
	if config.InteropMessageRPC == "" {
		return errors.New("interop verification requires rollup.interoprpc")
	}
	if strings.TrimSpace(config.InteropVerificationURL) == "" {
		return errors.New("interop verification requires rollup.interopverificationurl")
	}
	parsedURL, err := url.Parse(config.InteropVerificationURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return fmt.Errorf("invalid rollup.interopverificationurl %q", config.InteropVerificationURL)
	}
	if config.InteropVerificationTimeout <= 0 {
		return errors.New("interop verification requires a positive rollup.interopverificationtimeout")
	}
	return nil
}

var _ miner.BackendWithInterop = (*Ethereum)(nil)
