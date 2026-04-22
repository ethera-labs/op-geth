package txpool

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/types/interoptypes"
)

type mockInteropFilterAPI struct {
	timeFn       func() (uint64, error)
	accessListFn func(tx *types.Transaction) []common.Hash
	verifyFn     func(ctx context.Context, tx *types.Transaction, inboxEntries []common.Hash, minSafety interoptypes.SafetyLevel, ed interoptypes.ExecutingDescriptor) error
	checkFn      func(ctx context.Context, inboxEntries []common.Hash, minSafety interoptypes.SafetyLevel, ed interoptypes.ExecutingDescriptor) error
}

func (m *mockInteropFilterAPI) CurrentInteropBlockTime() (uint64, error) {
	if m.timeFn != nil {
		return m.timeFn()
	}
	return 0, nil
}

func (m *mockInteropFilterAPI) TxToInteropAccessList(tx *types.Transaction) []common.Hash {
	if m.accessListFn != nil {
		return m.accessListFn(tx)
	}
	return nil
}

func (m *mockInteropFilterAPI) VerifyInteropTx(ctx context.Context, tx *types.Transaction, inboxEntries []common.Hash, minSafety interoptypes.SafetyLevel, ed interoptypes.ExecutingDescriptor) error {
	if m.verifyFn != nil {
		return m.verifyFn(ctx, tx, inboxEntries, minSafety, ed)
	}
	return nil
}

func (m *mockInteropFilterAPI) CheckAccessList(ctx context.Context, inboxEntries []common.Hash, minSafety interoptypes.SafetyLevel, ed interoptypes.ExecutingDescriptor) error {
	if m.checkFn != nil {
		return m.checkFn(ctx, inboxEntries, minSafety, ed)
	}
	return nil
}

func TestInteropFilter(t *testing.T) {
	api := &mockInteropFilterAPI{}
	filter := NewInteropFilter(api, *uint256.NewInt(123))
	tx := types.NewTx(&types.DynamicFeeTx{})

	t.Run("Tx has no access list", func(t *testing.T) {
		api.accessListFn = func(tx *types.Transaction) []common.Hash {
			return nil
		}
		require.True(t, filter.FilterTx(context.Background(), tx))
	})
	t.Run("Tx errored when checking current interop block time", func(t *testing.T) {
		api.timeFn = func() (uint64, error) {
			return 0, errors.New("error")
		}
		require.True(t, filter.FilterTx(context.Background(), tx))
	})
	t.Run("Tx has valid executing message", func(t *testing.T) {
		api.timeFn = func() (uint64, error) {
			return 0, nil
		}
		api.accessListFn = func(tx *types.Transaction) []common.Hash {
			return []common.Hash{{0xaa}}
		}
		api.checkFn = func(ctx context.Context, inboxEntries []common.Hash, minSafety interoptypes.SafetyLevel, ed interoptypes.ExecutingDescriptor) error {
			require.Equal(t, common.Hash{0xaa}, inboxEntries[0])
			return nil
		}
		require.True(t, filter.FilterTx(context.Background(), tx))
	})
	t.Run("Tx has invalid executing message", func(t *testing.T) {
		api.timeFn = func() (uint64, error) {
			return 1, nil
		}
		api.accessListFn = func(tx *types.Transaction) []common.Hash {
			return []common.Hash{{0xaa}}
		}
		api.checkFn = func(ctx context.Context, inboxEntries []common.Hash, minSafety interoptypes.SafetyLevel, ed interoptypes.ExecutingDescriptor) error {
			require.Equal(t, common.Hash{0xaa}, inboxEntries[0])
			return errors.New("error")
		}
		require.False(t, filter.FilterTx(context.Background(), tx))
	})
	t.Run("Tx has valid executing message equal to than expiry", func(t *testing.T) {
		api.timeFn = func() (uint64, error) {
			expiredT := tx.Time().Add(86400 * time.Second)
			return uint64(expiredT.Unix()), nil
		}
		api.accessListFn = func(tx *types.Transaction) []common.Hash {
			return []common.Hash{{0xaa}}
		}
		api.checkFn = func(ctx context.Context, inboxEntries []common.Hash, minSafety interoptypes.SafetyLevel, ed interoptypes.ExecutingDescriptor) error {
			require.Equal(t, common.Hash{0xaa}, inboxEntries[0])
			return nil
		}
		require.True(t, filter.FilterTx(context.Background(), tx))
	})
	t.Run("Tx has valid executing message older than expiry", func(t *testing.T) {
		api.timeFn = func() (uint64, error) {
			expiredT := tx.Time().Add(86401 * time.Second)
			return uint64(expiredT.Unix()), nil
		}
		api.accessListFn = func(tx *types.Transaction) []common.Hash {
			return []common.Hash{{0xaa}}
		}
		api.checkFn = func(ctx context.Context, inboxEntries []common.Hash, minSafety interoptypes.SafetyLevel, ed interoptypes.ExecutingDescriptor) error {
			require.Equal(t, common.Hash{0xaa}, inboxEntries[0])
			return nil
		}
		require.False(t, filter.FilterTx(context.Background(), tx))
	})
}

func TestInteropFilterRPCFailures(t *testing.T) {
	tests := []struct {
		name        string
		networkErr  bool
		timeout     bool
		invalidResp bool
	}{
		{
			name:       "Network Error",
			networkErr: true,
		},
		{
			name:    "Timeout",
			timeout: true,
		},
		{
			name:        "Invalid Response",
			invalidResp: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &mockInteropFilterAPI{}
			filter := NewInteropFilter(api, *uint256.NewInt(123))
			api.accessListFn = func(tx *types.Transaction) []common.Hash {
				return []common.Hash{{0xaa}}
			}
			api.checkFn = func(ctx context.Context, inboxEntries []common.Hash, minSafety interoptypes.SafetyLevel, ed interoptypes.ExecutingDescriptor) error {
				if tt.networkErr {
					return &net.OpError{Op: "dial", Err: errors.New("connection refused")}
				}
				if tt.timeout {
					return context.DeadlineExceeded
				}
				if tt.invalidResp {
					return errors.New("invalid response format")
				}
				return nil
			}

			result := filter.FilterTx(context.Background(), &types.Transaction{})
			require.Equal(t, false, result, "FilterTx result mismatch")
		})
	}
}

func TestInteropFilterVerification(t *testing.T) {
	t.Run("verification failure rejects tx before supervisor check", func(t *testing.T) {
		api := &mockInteropFilterAPI{}
		filter := NewInteropFilter(api, *uint256.NewInt(123))
		tx := types.NewTx(&types.DynamicFeeTx{})
		checkCalled := false

		api.timeFn = func() (uint64, error) {
			return 1, nil
		}
		api.accessListFn = func(tx *types.Transaction) []common.Hash {
			return []common.Hash{{0xaa}}
		}
		api.verifyFn = func(ctx context.Context, tx *types.Transaction, inboxEntries []common.Hash, minSafety interoptypes.SafetyLevel, ed interoptypes.ExecutingDescriptor) error {
			require.Equal(t, common.Hash{0xaa}, inboxEntries[0])
			return errors.New("reject")
		}
		api.checkFn = func(ctx context.Context, inboxEntries []common.Hash, minSafety interoptypes.SafetyLevel, ed interoptypes.ExecutingDescriptor) error {
			checkCalled = true
			return nil
		}

		require.False(t, filter.FilterTx(context.Background(), tx))
		require.False(t, checkCalled)
	})

	t.Run("verification success continues to supervisor check", func(t *testing.T) {
		api := &mockInteropFilterAPI{}
		filter := NewInteropFilter(api, *uint256.NewInt(123))
		tx := types.NewTx(&types.DynamicFeeTx{})
		verifyCalled := false
		checkCalled := false

		api.timeFn = func() (uint64, error) {
			return 1, nil
		}
		api.accessListFn = func(tx *types.Transaction) []common.Hash {
			return []common.Hash{{0xaa}}
		}
		api.verifyFn = func(ctx context.Context, tx *types.Transaction, inboxEntries []common.Hash, minSafety interoptypes.SafetyLevel, ed interoptypes.ExecutingDescriptor) error {
			verifyCalled = true
			return nil
		}
		api.checkFn = func(ctx context.Context, inboxEntries []common.Hash, minSafety interoptypes.SafetyLevel, ed interoptypes.ExecutingDescriptor) error {
			checkCalled = true
			return nil
		}

		require.True(t, filter.FilterTx(context.Background(), tx))
		require.True(t, verifyCalled)
		require.True(t, checkCalled)
	})
}
