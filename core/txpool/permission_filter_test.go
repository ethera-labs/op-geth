package txpool

import (
	"context"
	"crypto/ecdsa"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

type mockPermissionAPI struct {
	disabled          bool
	canDeployContract bool
	known             bool
	mailbox           *common.Address
	allowedChains     map[uint64]bool
}

func (m *mockPermissionAPI) SenderDisabled(common.Address) bool {
	return m.disabled
}

func (m *mockPermissionAPI) CanDeployContract(common.Address) (bool, bool) {
	return m.canDeployContract, m.known
}

func (m *mockPermissionAPI) ChainReachable(_ common.Address, destChainID uint64) bool {
	return m.allowedChains[destChainID]
}

func (m *mockPermissionAPI) LocalMailbox() (common.Address, bool) {
	if m.mailbox == nil {
		return common.Address{}, false
	}
	return *m.mailbox, true
}

var permissionTestChainID = big.NewInt(7)

func signTx(t *testing.T, key *ecdsa.PrivateKey, to *common.Address, value *big.Int, data []byte) *types.Transaction {
	t.Helper()
	signer := types.LatestSignerForChainID(permissionTestChainID)
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   permissionTestChainID,
		Nonce:     0,
		GasTipCap: big.NewInt(1),
		GasFeeCap: big.NewInt(1),
		Gas:       21000,
		To:        to,
		Value:     value,
		Data:      data,
	})
	signed, err := types.SignTx(tx, signer, key)
	require.NoError(t, err)
	return signed
}

func mailboxWriteCalldata(destChainID uint64) []byte {
	data := make([]byte, 0, 4+32)
	data = append(data, mailboxWriteSelector...)
	var word [32]byte
	new(big.Int).SetUint64(destChainID).FillBytes(word[:])
	return append(data, word[:]...)
}

func TestPermissionFilter(t *testing.T) {
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	contractAddr := common.HexToAddress("0x000000000000000000000000000000000000c0de")
	mailboxAddr := common.HexToAddress("0x00000000000000000000000000000000000ba110")

	t.Run("unmanaged sender is unrestricted", func(t *testing.T) {
		api := &mockPermissionAPI{known: false}
		f := NewPermissionFilter(api, permissionTestChainID, true)
		tx := signTx(t, key, nil, big.NewInt(1), nil)
		require.True(t, f.FilterTx(context.Background(), tx))
	})

	t.Run("blocks all transactions from a disabled entity", func(t *testing.T) {
		api := &mockPermissionAPI{known: true, disabled: true, canDeployContract: true}
		f := NewPermissionFilter(api, permissionTestChainID, true)
		tx := signTx(t, key, &contractAddr, big.NewInt(0), []byte{0x01})
		require.False(t, f.FilterTx(context.Background(), tx))
	})

	t.Run("blocks contract deployment", func(t *testing.T) {
		api := &mockPermissionAPI{known: true, canDeployContract: false}
		f := NewPermissionFilter(api, permissionTestChainID, true)
		tx := signTx(t, key, nil, big.NewInt(0), []byte{0x60, 0x80})
		require.False(t, f.FilterTx(context.Background(), tx))
	})

	t.Run("allows permitted contract deployment", func(t *testing.T) {
		api := &mockPermissionAPI{known: true, canDeployContract: true}
		f := NewPermissionFilter(api, permissionTestChainID, true)
		tx := signTx(t, key, nil, big.NewInt(0), []byte{0x60, 0x80})
		require.True(t, f.FilterTx(context.Background(), tx))
	})

	t.Run("blocks cross-chain write to non-whitelisted chain", func(t *testing.T) {
		api := &mockPermissionAPI{
			known:         true,
			mailbox:       &mailboxAddr,
			allowedChains: map[uint64]bool{222: true},
		}
		f := NewPermissionFilter(api, permissionTestChainID, true)
		tx := signTx(t, key, &mailboxAddr, big.NewInt(0), mailboxWriteCalldata(333))
		require.False(t, f.FilterTx(context.Background(), tx))
	})

	t.Run("allows cross-chain write to whitelisted chain", func(t *testing.T) {
		api := &mockPermissionAPI{
			known:         true,
			mailbox:       &mailboxAddr,
			allowedChains: map[uint64]bool{222: true},
		}
		f := NewPermissionFilter(api, permissionTestChainID, true)
		tx := signTx(t, key, &mailboxAddr, big.NewInt(0), mailboxWriteCalldata(222))
		require.True(t, f.FilterTx(context.Background(), tx))
	})

	t.Run("mailbox call that is not a write is ignored", func(t *testing.T) {
		api := &mockPermissionAPI{
			known:         true,
			mailbox:       &mailboxAddr,
			allowedChains: map[uint64]bool{},
		}
		f := NewPermissionFilter(api, permissionTestChainID, true)
		tx := signTx(t, key, &mailboxAddr, big.NewInt(0), []byte{0xde, 0xad, 0xbe, 0xef})
		require.True(t, f.FilterTx(context.Background(), tx))
	})

	t.Run("unrecoverable sender honours fail mode", func(t *testing.T) {
		unsigned := types.NewTx(&types.DynamicFeeTx{ChainID: permissionTestChainID, To: &contractAddr})
		require.True(t, NewPermissionFilter(&mockPermissionAPI{}, permissionTestChainID, true).FilterTx(context.Background(), unsigned))
		require.False(t, NewPermissionFilter(&mockPermissionAPI{}, permissionTestChainID, false).FilterTx(context.Background(), unsigned))
	})
}
