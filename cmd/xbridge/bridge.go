package main

import (
	"crypto/ecdsa"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// bridgeABI exposes the ComposeL2ToL2Bridge entrypoints this client calls:
//   - bridgeERC20To: source-leg ERC-20 bridge.
//   - receiveTokens: destination-leg; takes a MessageHeader tuple matching
//     IUniversalBridgeMailbox.MessageHeader.
const bridgeABI = `[
  {"type":"function","name":"bridgeERC20To","stateMutability":"nonpayable",
   "inputs":[
     {"name":"chainDest","type":"uint256"},
     {"name":"tokenSrc","type":"address"},
     {"name":"amount","type":"uint256"},
     {"name":"receiver","type":"address"},
     {"name":"sessionId","type":"uint256"}
   ],"outputs":[]},
  {"type":"function","name":"receiveTokens","stateMutability":"nonpayable",
   "inputs":[
     {"name":"msgHeader","type":"tuple",
      "components":[
        {"name":"chainSrc","type":"uint256"},
        {"name":"chainDest","type":"uint256"},
        {"name":"sender","type":"address"},
        {"name":"receiver","type":"address"},
        {"name":"sessionId","type":"uint256"},
        {"name":"label","type":"string"}
      ]}
   ],"outputs":[
     {"name":"token","type":"address"},
     {"name":"amount","type":"uint256"}
   ]}
]`

// MessageHeader mirrors IUniversalBridgeMailbox.MessageHeader so the go-ethereum
// ABI encoder can pack it as the single tuple argument to receiveTokens.
type MessageHeader struct {
	ChainSrc  *big.Int
	ChainDest *big.Int
	Sender    common.Address
	Receiver  common.Address
	SessionId *big.Int
	Label     string
}

// BridgeParams holds the inputs used to build both legs of a transaction pair.
type BridgeParams struct {
	ChainSrc   *big.Int
	ChainDest  *big.Int
	Token      common.Address
	Sender     common.Address
	Receiver   common.Address
	Amount     *big.Int
	SessionId  *big.Int
	SrcBridge  common.Address
	DestBridge common.Address
}

func createSendTransaction(p BridgeParams, nonce uint64, key *ecdsa.PrivateKey) (*types.Transaction, error) {
	parsed, err := abi.JSON(strings.NewReader(bridgeABI))
	if err != nil {
		return nil, err
	}
	calldata, err := parsed.Pack("bridgeERC20To",
		p.ChainDest, p.Token, p.Amount, p.Receiver, p.SessionId,
	)
	if err != nil {
		return nil, err
	}
	return signDynamicTx(p.ChainSrc, nonce, p.SrcBridge, calldata, key)
}

func createReceiveTransaction(p BridgeParams, nonce uint64, key *ecdsa.PrivateKey) (*types.Transaction, error) {
	parsed, err := abi.JSON(strings.NewReader(bridgeABI))
	if err != nil {
		return nil, err
	}
	header := MessageHeader{
		ChainSrc:  p.ChainSrc,
		ChainDest: p.ChainDest,
		Sender:    p.SrcBridge,
		Receiver:  p.Receiver,
		SessionId: p.SessionId,
		Label:     "SEND_TOKENS",
	}
	calldata, err := parsed.Pack("receiveTokens", header)
	if err != nil {
		return nil, err
	}
	return signDynamicTx(p.ChainDest, nonce, p.DestBridge, calldata, key)
}

func signDynamicTx(chainID *big.Int, nonce uint64, to common.Address, data []byte, key *ecdsa.PrivateKey) (*types.Transaction, error) {
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasTipCap: big.NewInt(1_000_000_000),
		GasFeeCap: big.NewInt(20_000_000_000),
		// receiveTokens may trigger CetFactory.deployIfAbsent to deploy a
		// ~4.9KB WrappedCET on first-arrival of a given remote asset, which
		// costs ~850k on top of the mailbox/decode work. 3M leaves a safe
		// margin for subsequent storage writes and the ACK write.
		Gas:   3_000_000,
		To:    &to,
		Value: big.NewInt(0),
		Data:  data,
	})
	return types.SignTx(tx, types.NewLondonSigner(chainID), key)
}
