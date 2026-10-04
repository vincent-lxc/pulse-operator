// 本文件把 pay / sweep 交给 Circle，观察仍走原来的链客户端。
package treasury

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// TagChain 给已有执行结果补上产品标签。dry-run 用它，不访问 Circle。
func TagChain(inner Chain, product string) Chain {
	return tagChain{inner: inner, product: product}
}

// tagChain 给已有执行结果补上产品标签。dry-run 用它，不访问 Circle。
type tagChain struct {
	inner   Chain
	product string
}

func (c tagChain) Observe(ctx context.Context) (Snapshot, error) {
	return c.inner.Observe(ctx)
}

func (c tagChain) Pay(ctx context.Context, call PayCall) (ExecResult, error) {
	res, err := c.inner.Pay(ctx, call)
	if res.Product == "" {
		res.Product = c.product
	}
	return res, err
}

func (c tagChain) Sweep(ctx context.Context, call SweepCall) (ExecResult, error) {
	res, err := c.inner.Sweep(ctx, call)
	if res.Product == "" {
		res.Product = c.product
	}
	return res, err
}

func (c tagChain) Approve(ctx context.Context, requestID string) (ExecResult, error) {
	inner, ok := c.inner.(ApprovalSender)
	if !ok {
		return ExecResult{Product: c.product}, fmt.Errorf("this chain cannot approve requests")
	}
	res, err := inner.Approve(ctx, requestID)
	if res.Product == "" {
		res.Product = c.product
	}
	return res, err
}

func (c tagChain) Reject(ctx context.Context, requestID string) (ExecResult, error) {
	inner, ok := c.inner.(ApprovalSender)
	if !ok {
		return ExecResult{Product: c.product}, fmt.Errorf("this chain cannot reject requests")
	}
	res, err := inner.Reject(ctx, requestID)
	if res.Product == "" {
		res.Product = c.product
	}
	return res, err
}

// PayExplainer 能在交易上链后区分直接支付和待审批。
type PayExplainer interface {
	ExplainPay(ctx context.Context, txHash string, decision common.Hash) (status, requestID string, err error)
}

type walletsChain struct {
	observe     Chain
	client      *WalletsClient
	vault       string
	blockchain  string
	payWallet   string
	sweepWallet string
	fee         CircleFee
}

// NewWalletsChain 用 Circle Developer-Controlled Wallets 发送 pay、sweep、approve 和 reject。
func NewWalletsChain(observe Chain, client *WalletsClient, vault, blockchain, payWallet, sweepWallet string, fee CircleFee) Chain {
	return walletsChain{
		observe: observe, client: client, vault: vault, blockchain: blockchain,
		payWallet: payWallet, sweepWallet: sweepWallet, fee: fee,
	}
}

func (c walletsChain) Observe(ctx context.Context) (Snapshot, error) {
	return c.observe.Observe(ctx)
}

func (c walletsChain) Pay(ctx context.Context, call PayCall) (ExecResult, error) {
	if c.payWallet == "" {
		return ExecResult{Product: ProductWallets}, fmt.Errorf("set CIRCLE_WALLET_ID to the developer-controlled wallet that is the PolicyVault agent")
	}
	params, err := PayArguments(call)
	if err != nil {
		return ExecResult{Product: ProductWallets}, err
	}
	tx, err := c.client.Execute(ctx, ContractExecution{
		IdempotencyKey: IdempotencyFromHash(call.DecisionHash),
		WalletID:       c.payWallet,
		Blockchain:     c.blockchain,
		Contract:       c.vault,
		Signature:      "pay(bytes32,address,uint256,bytes32)",
		Params:         params,
		Fee:            c.fee,
	})
	if err != nil {
		return ExecResult{Product: ProductWallets}, err
	}
	return explain(ctx, c.observe, tx.TxHash, call.DecisionHash, ProductWallets, false), nil
}

func (c walletsChain) Sweep(ctx context.Context, call SweepCall) (ExecResult, error) {
	if c.sweepWallet == "" {
		return ExecResult{Product: ProductWallets}, ErrOwnerKeyRequired
	}
	tx, err := c.client.Execute(ctx, ContractExecution{
		IdempotencyKey: IdempotencyFromHash(call.DecisionHash),
		WalletID:       c.sweepWallet,
		Blockchain:     c.blockchain,
		Contract:       c.vault,
		Signature:      "sweepToReserve(uint256,bytes32)",
		Params:         SweepArguments(call),
		Fee:            c.fee,
	})
	if err != nil {
		return ExecResult{Product: ProductWallets}, err
	}
	res := explain(ctx, c.observe, tx.TxHash, call.DecisionHash, ProductWallets, true)
	return res, nil
}

// Approve 用 owner 钱包调用 PolicyVault.approve。
func (c walletsChain) Approve(ctx context.Context, requestID string) (ExecResult, error) {
	return c.ownerContract(ctx, requestID, "approve", "approve(uint256)", "approved")
}

// Reject 用 owner 钱包调用 PolicyVault.reject。
func (c walletsChain) Reject(ctx context.Context, requestID string) (ExecResult, error) {
	return c.ownerContract(ctx, requestID, "reject", "reject(uint256)", "rejected")
}

func (c walletsChain) ownerContract(ctx context.Context, requestID, action, signature, status string) (ExecResult, error) {
	if c.sweepWallet == "" {
		return ExecResult{Product: ProductWallets}, ErrOwnerKeyRequired
	}
	if _, err := ParseRequestID(requestID); err != nil {
		return ExecResult{Product: ProductWallets}, err
	}
	tx, err := c.client.Execute(ctx, ContractExecution{
		IdempotencyKey: IdempotencyFromHash(OwnerActionHash(action, requestID)),
		WalletID:       c.sweepWallet,
		Blockchain:     c.blockchain,
		Contract:       c.vault,
		Signature:      signature,
		Params:         []string{strings.TrimSpace(requestID)},
		Fee:            c.fee,
	})
	if err != nil {
		return ExecResult{Product: ProductWallets}, err
	}
	return ExecResult{Status: status, TxHash: tx.TxHash, RequestID: strings.TrimSpace(requestID), Product: ProductWallets}, nil
}

func explain(ctx context.Context, observe Chain, txHash string, decision common.Hash, product string, sweep bool) ExecResult {
	res := ExecResult{Status: "circle_confirmed", TxHash: txHash, Product: product}
	if sweep {
		res.Status = "swept"
		return res
	}
	if explainer, ok := observe.(PayExplainer); ok && txHash != "" {
		status, requestID, err := explainer.ExplainPay(ctx, txHash, decision)
		if err == nil && status != "" {
			res.Status = status
			res.RequestID = requestID
		}
	}
	return res
}

// OwnerActionHash 给 approve/reject 一个稳定的幂等键材料。
func OwnerActionHash(action, requestID string) common.Hash {
	sum := crypto.Keccak256([]byte("pulse-operator/" + action + "/v1:" + strings.TrimSpace(requestID)))
	return common.BytesToHash(sum)
}

// TagProduct 在产品标签为空时填上默认值。
func TagProduct(current, fallback string) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	return fallback
}
