// 本文件定义链客户端。dry-run 与 live 都走这个接口，测试使用 mock。
package treasury

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// PayCall 是一次 PolicyVault.pay。
type PayCall struct {
	Category     string
	Payee        string
	Amount       *big.Int
	DecisionHash common.Hash
}

// SweepCall 是一次 sweepToReserve。
type SweepCall struct {
	Amount       *big.Int
	DecisionHash common.Hash
	Reserve      string
}

// ExecResult 是执行或模拟的结果。
type ExecResult struct {
	Status    string
	TxHash    string
	RequestID string
	Calldata  string
	// Product 是处理这笔执行的 Circle 产品标签，例如 circle:wallets。
	Product string
	// CircleTxID 是 Circle 返回的交易 id，不是链上哈希。
	CircleTxID string
	// CircleState 是 Circle 交易状态，例如 COMPLETE 或 FAILED。
	CircleState string
}

// PendingSource 读取 PolicyVault 上仍未决的审批。
type PendingSource interface {
	ListPending(ctx context.Context) ([]Approval, error)
}

// RevertProber 用 eth_call 复现回退，便于解出自定义错误。
type RevertProber interface {
	ProbeOwner(ctx context.Context, data []byte) error
	ProbeAgent(ctx context.Context, data []byte) error
}

// Chain 读取金库并可选地提交交易。
type Chain interface {
	Observe(ctx context.Context) (Snapshot, error)
	Pay(ctx context.Context, call PayCall) (ExecResult, error)
	Sweep(ctx context.Context, call SweepCall) (ExecResult, error)
}

// ApprovalSender 由 owner 签名 approve 或 reject。
type ApprovalSender interface {
	Approve(ctx context.Context, requestID string) (ExecResult, error)
	Reject(ctx context.Context, requestID string) (ExecResult, error)
}
