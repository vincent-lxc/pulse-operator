// 本文件在付款前读链号，并调用 pendingCount 确认金库代码。
package procurement

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// ReadChain 读取 chain id。vault 非空时再调用 pendingCount，成功才算代码核对通过。
func ReadChain(ctx context.Context, rpc, vault string) (*big.Int, bool, error) {
	client, err := ethclient.DialContext(ctx, rpc)
	if err != nil {
		return nil, false, fmt.Errorf("dial: %w", err)
	}
	defer client.Close()
	id, err := client.ChainID(ctx)
	if err != nil {
		return nil, false, err
	}
	if !common.IsHexAddress(vault) {
		return id, false, nil
	}
	addr := common.HexToAddress(vault)
	raw, err := client.CallContract(ctx, ethereum.CallMsg{To: &addr, Data: PendingCountCalldata()}, nil)
	if err != nil || len(raw) < 32 {
		return id, false, nil
	}
	return id, true, nil
}

// VaultAgent 只读 PolicyVault.agent()。
func VaultAgent(ctx context.Context, rpc, vault string) (common.Address, error) {
	if !common.IsHexAddress(vault) {
		return common.Address{}, fmt.Errorf("vault address is missing")
	}
	raw, err := callTo(ctx, rpc, common.HexToAddress(vault), crypto.Keccak256([]byte("agent()"))[:4])
	if err != nil {
		return common.Address{}, fmt.Errorf("PolicyVault.agent(): %w", err)
	}
	if len(raw) < 32 {
		return common.Address{}, fmt.Errorf("PolicyVault.agent() returned a short result")
	}
	return common.BytesToAddress(raw[len(raw)-32:]), nil
}

// NativeBalance 读取地址的原生余额。Arc 上这是用来付 gas 的 USDC。
func NativeBalance(ctx context.Context, rpc, account string) (*big.Int, error) {
	if !common.IsHexAddress(account) {
		return nil, fmt.Errorf("address is missing")
	}
	client, err := ethclient.DialContext(ctx, rpc)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	defer client.Close()
	bal, err := client.BalanceAt(ctx, common.HexToAddress(account), nil)
	if err != nil {
		return nil, err
	}
	if bal == nil {
		return big.NewInt(0), nil
	}
	return bal, nil
}

// TokenBalance 只读 ERC-20 balanceOf。
func TokenBalance(ctx context.Context, rpc, token, account string) (*big.Int, error) {
	if !common.IsHexAddress(token) || !common.IsHexAddress(account) {
		return nil, fmt.Errorf("token balance address is missing")
	}
	sig := crypto.Keccak256([]byte("balanceOf(address)"))[:4]
	data := append([]byte{}, sig...)
	data = append(data, common.LeftPadBytes(common.HexToAddress(account).Bytes(), 32)...)
	raw, err := callTo(ctx, rpc, common.HexToAddress(token), data)
	if err != nil {
		return nil, err
	}
	if len(raw) < 32 {
		return nil, fmt.Errorf("balanceOf returned a short result")
	}
	return new(big.Int).SetBytes(raw[len(raw)-32:]), nil
}

// AuthorizationState 读取 USDC authorizationState(authorizer, nonce)。
// 返回 true 表示这个 nonce 已经用过。
func AuthorizationState(ctx context.Context, rpc, token, authorizer, nonceHex string) (bool, error) {
	if !common.IsHexAddress(token) || !common.IsHexAddress(authorizer) {
		return false, fmt.Errorf("authorizationState address is missing")
	}
	nonce := common.FromHex(strings.TrimSpace(nonceHex))
	if len(nonce) != 32 {
		return false, fmt.Errorf("authorization nonce must be 32 bytes")
	}
	sig := crypto.Keccak256([]byte("authorizationState(address,bytes32)"))[:4]
	data := append([]byte{}, sig...)
	data = append(data, common.LeftPadBytes(common.HexToAddress(authorizer).Bytes(), 32)...)
	data = append(data, nonce...)
	raw, err := callTo(ctx, rpc, common.HexToAddress(token), data)
	if err != nil {
		return false, fmt.Errorf("authorizationState: %w", err)
	}
	if len(raw) < 32 {
		return false, fmt.Errorf("authorizationState returned a short result")
	}
	return new(big.Int).SetBytes(raw[len(raw)-32:]).Sign() != 0, nil
}

func callTo(ctx context.Context, rpc string, to common.Address, data []byte) ([]byte, error) {
	client, err := ethclient.DialContext(ctx, rpc)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	defer client.Close()
	return client.CallContract(ctx, ethereum.CallMsg{To: &to, Data: data}, nil)
}
