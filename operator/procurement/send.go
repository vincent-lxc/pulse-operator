// 本文件用原始私钥发送一笔合约调用。只有闸门通过后的 live 路径会调用它。
package procurement

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// TxGas 是 Arc 交易的手续费上限。MaxFeeWei 是配置里的封顶，TipWei 加在 base fee 上。
type TxGas struct {
	MaxFeeWei *big.Int
	TipWei    *big.Int
}

// SendCall 签名并等待回执。调用方必须已经通过主网闸门。
func SendCall(ctx context.Context, rpc string, chainID *big.Int, key *ecdsa.PrivateKey, to common.Address, data []byte, quote TxGas) (string, error) {
	if key == nil {
		return "", fmt.Errorf("signer is missing")
	}
	client, err := ethclient.DialContext(ctx, rpc)
	if err != nil {
		return "", err
	}
	defer client.Close()
	from := crypto.PubkeyToAddress(key.PublicKey)
	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return "", err
	}
	limit, err := client.EstimateGas(ctx, ethereum.CallMsg{From: from, To: &to, Data: data})
	if err != nil {
		return "", err
	}
	var baseFee *big.Int
	if header, err := client.HeaderByNumber(ctx, nil); err == nil && header != nil {
		baseFee = header.BaseFee
	}
	suggested, suggestErr := client.SuggestGasPrice(ctx)
	if suggestErr != nil {
		suggested = nil
	}
	price := suggested
	if arcChain(chainID) {
		price = ArcGasPrice(suggested, baseFee, quote.TipWei, quote.MaxFeeWei)
	} else if price == nil || price.Sign() <= 0 {
		price = big.NewInt(50_000_000_000)
	}
	tx := types.NewTx(&types.LegacyTx{
		Nonce: nonce, To: &to, Value: big.NewInt(0), Gas: limit + limit/5, GasPrice: price, Data: data,
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), key)
	if err != nil {
		return "", err
	}
	if err := client.SendTransaction(ctx, signed); err != nil {
		return "", err
	}
	wait, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	receipt, err := waitReceipt(wait, client, signed.Hash())
	if err != nil {
		return signed.Hash().Hex(), err
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return signed.Hash().Hex(), fmt.Errorf("transaction reverted: %s", signed.Hash().Hex())
	}
	return signed.Hash().Hex(), nil
}

func waitReceipt(ctx context.Context, client *ethclient.Client, hash common.Hash) (*types.Receipt, error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		receipt, err := client.TransactionReceipt(ctx, hash)
		if err == nil {
			return receipt, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
