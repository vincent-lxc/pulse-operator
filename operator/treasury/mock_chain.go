// 本文件是内存金库。它执行与 PolicyVault 相同的限额分支，但不广播交易。
package treasury

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// MockChain 从快照出发，在进程内模拟 pay 和 sweep。
type MockChain struct {
	snap          Snapshot
	decisionUsed  map[common.Hash]bool
	nextRequestID uint64
}

// NewMockChain 复制一份快照，调用方之后的修改不会回写原件。
func NewMockChain(snap Snapshot) *MockChain {
	return &MockChain{snap: cloneSnapshot(snap), decisionUsed: map[common.Hash]bool{}, nextRequestID: 1}
}

// Observe 返回当前模拟状态。
func (m *MockChain) Observe(context.Context) (Snapshot, error) {
	return cloneSnapshot(m.snap), nil
}

// ListPending 返回仍未决的模拟审批。
func (m *MockChain) ListPending(context.Context) ([]Approval, error) {
	out := make([]Approval, 0, len(m.snap.Pending))
	for _, item := range m.snap.Pending {
		if item.Status == "" || item.Status == "pending" {
			out = append(out, item)
		}
	}
	return out, nil
}

// Snapshot 返回调用后的模拟状态，供 dry-run 在进程外延续。
func (m *MockChain) Snapshot() Snapshot {
	return cloneSnapshot(m.snap)
}

// Pay 模拟自主支付或超限升级。不允许的收款方返回错误，不产生状态。
func (m *MockChain) Pay(_ context.Context, call PayCall) (ExecResult, error) {
	if m.decisionUsed[call.DecisionHash] {
		return ExecResult{}, fmt.Errorf("decision already used")
	}
	cat, ok := m.snap.Categories[call.Category]
	if !ok || !cat.Enabled {
		return ExecResult{}, fmt.Errorf("unknown category")
	}
	payee := NormalizeAddress(call.Payee)
	if !cat.Payees[payee] {
		return ExecResult{}, fmt.Errorf("payee not allowed")
	}
	amount := unitsOrZero(call.Amount)
	if amount.Sign() <= 0 {
		return ExecResult{}, fmt.Errorf("amount must be positive")
	}
	data, err := PackPay(call.Category, payee, amount, call.DecisionHash)
	if err != nil {
		return ExecResult{}, err
	}
	m.decisionUsed[call.DecisionHash] = true
	res := ExecResult{Calldata: "0x" + fmt.Sprintf("%x", data), TxHash: mockTx(call.DecisionHash)}
	if amount.Cmp(unitsOrZero(cat.PerTxCap)) > 0 || amount.Cmp(unitsOrZero(cat.Remaining)) > 0 {
		id := fmt.Sprintf("%d", m.nextRequestID)
		m.nextRequestID++
		m.snap.Pending = append(m.snap.Pending, Approval{
			RequestID:    id,
			Category:     call.Category,
			Payee:        payee,
			Amount:       amount,
			DecisionHash: call.DecisionHash.Hex(),
			Status:       "pending",
		})
		res.Status = "simulated_approval"
		res.RequestID = id
		return res, nil
	}
	if unitsOrZero(m.snap.Balance).Cmp(amount) < 0 {
		return ExecResult{}, fmt.Errorf("insufficient balance")
	}
	ApplyPay(&m.snap, call.Category, amount)
	res.Status = "simulated_paid"
	return res, nil
}

// Sweep 模拟划转到储备地址。
func (m *MockChain) Sweep(_ context.Context, call SweepCall) (ExecResult, error) {
	amount := unitsOrZero(call.Amount)
	if amount.Sign() <= 0 {
		return ExecResult{}, fmt.Errorf("amount must be positive")
	}
	if unitsOrZero(m.snap.Balance).Cmp(amount) < 0 {
		return ExecResult{}, fmt.Errorf("insufficient balance")
	}
	data, err := PackSweep(amount, call.DecisionHash)
	if err != nil {
		return ExecResult{}, err
	}
	ApplySweep(&m.snap, amount)
	return ExecResult{
		Status:   "simulated_swept",
		TxHash:   mockTx(call.DecisionHash),
		Calldata: "0x" + fmt.Sprintf("%x", data),
	}, nil
}

// Approve 在内存里通过一笔待审批并扣减余额。
func (m *MockChain) Approve(_ context.Context, requestID string) (ExecResult, error) {
	return m.settleRequest(requestID, true)
}

// Reject 在内存里拒绝一笔待审批，不移动资金。
func (m *MockChain) Reject(_ context.Context, requestID string) (ExecResult, error) {
	return m.settleRequest(requestID, false)
}

func (m *MockChain) settleRequest(requestID string, approve bool) (ExecResult, error) {
	id := strings.TrimSpace(requestID)
	for i := range m.snap.Pending {
		item := &m.snap.Pending[i]
		if item.RequestID != id {
			continue
		}
		if item.Status != "pending" {
			return ExecResult{}, fmt.Errorf("request %s is %s", id, item.Status)
		}
		hash := common.HexToHash(item.DecisionHash)
		if hash == (common.Hash{}) {
			hash = OwnerActionHash(map[bool]string{true: "approve", false: "reject"}[approve], id)
		}
		if approve {
			item.Status = "approved"
			ApplyPay(&m.snap, item.Category, item.Amount)
			return ExecResult{Status: "simulated_approved", TxHash: mockTx(hash), RequestID: id}, nil
		}
		item.Status = "rejected"
		return ExecResult{Status: "simulated_rejected", TxHash: mockTx(hash), RequestID: id}, nil
	}
	return ExecResult{}, fmt.Errorf("request %s is not pending", id)
}

func mockTx(decision common.Hash) string {
	sum := crypto.Keccak256([]byte("pulse-operator/mock-tx/v1:" + decision.Hex()))
	return "0x" + fmt.Sprintf("%x", sum)
}

func cloneSnapshot(in Snapshot) Snapshot {
	out := in
	out.Balance = unitsOrZero(in.Balance)
	out.Categories = map[string]Category{}
	for k, c := range in.Categories {
		c.Budget = unitsOrZero(c.Budget)
		c.PerTxCap = unitsOrZero(c.PerTxCap)
		c.Spent = unitsOrZero(c.Spent)
		c.Remaining = unitsOrZero(c.Remaining)
		payees := map[string]bool{}
		for p, ok := range c.Payees {
			payees[p] = ok
		}
		c.Payees = payees
		out.Categories[k] = c
	}
	out.Inflows = append([]Inflow(nil), in.Inflows...)
	out.Pending = append([]Approval(nil), in.Pending...)
	return out
}
