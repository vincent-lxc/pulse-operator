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
}

// Chain 读取金库并可选地提交交易。
type Chain interface {
	Observe(ctx context.Context) (Snapshot, error)
	Pay(ctx context.Context, call PayCall) (ExecResult, error)
	Sweep(ctx context.Context, call SweepCall) (ExecResult, error)
}
