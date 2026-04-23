package main

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// erc20ABI — just enough for allowance + approve.
const erc20ABI = `[
  {"type":"function","name":"allowance","stateMutability":"view",
   "inputs":[{"name":"owner","type":"address"},{"name":"spender","type":"address"}],
   "outputs":[{"name":"","type":"uint256"}]},
  {"type":"function","name":"approve","stateMutability":"nonpayable",
   "inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],
   "outputs":[{"name":"","type":"bool"}]}
]`

var maxUint256 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

// ensureAllowance makes sure the owner has granted at least `amount` to the
// spender on the given chain. Idempotent: returns early if allowance is
// already sufficient; otherwise submits an approve(max) and waits for receipt.
func ensureAllowance(
	ctx context.Context,
	rpcURL string,
	chainID *big.Int,
	token, spender common.Address,
	owner *ecdsa.PrivateKey,
	ownerAddr common.Address,
	amount *big.Int,
) error {
	parsed, err := abi.JSON(strings.NewReader(erc20ABI))
	if err != nil {
		return fmt.Errorf("parse erc20 abi: %w", err)
	}

	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return fmt.Errorf("dial %s: %w", rpcURL, err)
	}
	defer client.Close()

	current, err := readAllowance(ctx, client, parsed, token, ownerAddr, spender)
	if err != nil {
		return fmt.Errorf("read allowance: %w", err)
	}
	if current.Cmp(amount) >= 0 {
		return nil
	}

	fmt.Printf("  allowance %s < %s; approving max on %s\n", current, amount, token.Hex())

	calldata, err := parsed.Pack("approve", spender, maxUint256)
	if err != nil {
		return fmt.Errorf("pack approve: %w", err)
	}
	nonce, err := client.PendingNonceAt(ctx, ownerAddr)
	if err != nil {
		return fmt.Errorf("nonce: %w", err)
	}
	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return fmt.Errorf("gas price: %w", err)
	}

	tx := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       &token,
		Value:    big.NewInt(0),
		Gas:      200_000,
		GasPrice: gasPrice,
		Data:     calldata,
	})
	signed, err := types.SignTx(tx, types.NewEIP155Signer(chainID), owner)
	if err != nil {
		return fmt.Errorf("sign approve: %w", err)
	}
	if err := client.SendTransaction(ctx, signed); err != nil {
		return fmt.Errorf("send approve: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	receipt, err := bind.WaitMined(waitCtx, client, signed)
	if err != nil {
		return fmt.Errorf("wait approve: %w", err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return fmt.Errorf("approve reverted (tx=%s)", signed.Hash().Hex())
	}
	fmt.Printf("  approved (tx=%s)\n", signed.Hash().Hex())
	return nil
}

func readAllowance(
	ctx context.Context,
	client *ethclient.Client,
	parsed abi.ABI,
	token, owner, spender common.Address,
) (*big.Int, error) {
	calldata, err := parsed.Pack("allowance", owner, spender)
	if err != nil {
		return nil, err
	}
	raw, err := client.CallContract(ctx, ethereum.CallMsg{To: &token, Data: calldata}, nil)
	if err != nil {
		return nil, err
	}
	values, err := parsed.Methods["allowance"].Outputs.Unpack(raw)
	if err != nil {
		return nil, err
	}
	return values[0].(*big.Int), nil
}
