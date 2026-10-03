// 本文件实现硬限制决策。软判断只能附加说明，不能把拒绝改成支付。
package treasury

import (
	"fmt"
	"math/big"
	"time"
)

// DecidePayable 对一笔待付给出 pay、defer 或 escalate。
// working 会被调用方在成功支付后更新；本函数不修改它。
func DecidePayable(p Payable, snap Snapshot, policy Policy, lastPaid map[string]time.Time) Decision {
	amount := unitsOrZero(p.Amount)
	d := Decision{
		PayableID:  p.ID,
		Category:   p.Category,
		Payee:      NormalizeAddress(p.Payee),
		Amount:     amount,
		ReasonCode: ReasonWithinPolicy,
	}
	if snap.Paused {
		return deferDecision(d, ReasonPaused, "vault is paused")
	}
	if p.Due.After(policy.Now) {
		return deferDecision(d, ReasonNotDue, "due date is still in the future")
	}
	cat, ok := snap.Categories[p.Category]
	if !ok || !cat.Enabled {
		d.Action = ActionEscalate
		d.ReasonCode = ReasonUnknownCategory
		d.Reason = "category is not enabled on the vault"
		d.Submit = false
		return d
	}
	if !cat.Payees[d.Payee] {
		d.Action = ActionEscalate
		d.ReasonCode = ReasonPayeeNotAllowed
		d.Reason = "payee is not on the category allowlist; pay would revert"
		d.Submit = false
		return d
	}
	cap := unitsOrZero(cat.PerTxCap)
	if amount.Cmp(cap) > 0 {
		d.Action = ActionEscalate
		d.ReasonCode = ReasonOverTxCap
		d.Reason = fmt.Sprintf("amount %s exceeds per-tx cap %s; submitting pay for owner approval", FormatUSDC(amount), FormatUSDC(cap))
		d.Submit = true
		return d
	}
	remaining := unitsOrZero(cat.Remaining)
	if amount.Cmp(remaining) > 0 {
		d.Action = ActionEscalate
		d.ReasonCode = ReasonOverBudget
		d.Reason = fmt.Sprintf("amount %s exceeds remaining budget %s; submitting pay for owner approval", FormatUSDC(amount), FormatUSDC(remaining))
		d.Submit = true
		return d
	}
	balance := unitsOrZero(snap.Balance)
	floor := unitsOrZero(policy.ReserveFloor)
	after := new(big.Int).Sub(balance, amount)
	if after.Cmp(floor) < 0 {
		return deferDecision(d, ReasonReserveFloor, fmt.Sprintf("paying would leave %s, below reserve floor %s", FormatUSDC(after), FormatUSDC(floor)))
	}
	if policy.Cooldown > 0 {
		if last, ok := lastPaid[d.Payee]; ok && !last.IsZero() && policy.Now.Sub(last) < policy.Cooldown {
			return deferDecision(d, ReasonCooldown, "payee cooldown has not elapsed")
		}
	}
	d.Action = ActionPay
	d.ReasonCode = ReasonWithinPolicy
	d.Reason = "within allowlist, per-tx cap, epoch budget, reserve floor and cooldown"
	d.Submit = true
	return d
}

func deferDecision(d Decision, code, reason string) Decision {
	d.Action = ActionDefer
	d.ReasonCode = code
	d.Reason = reason
	d.Submit = false
	return d
}

// DueWithin 表示这笔在评估窗口内（含已逾期）需要留流动性。
func DueWithin(p Payable, now time.Time, horizon time.Duration) bool {
	return !p.Due.After(now.Add(horizon))
}

// Assess 对照余额、窗口内未付义务和储备目标。
func Assess(balance, obligations, floor, target *big.Int) Liquidity {
	balance = unitsOrZero(balance)
	obligations = unitsOrZero(obligations)
	floor = unitsOrZero(floor)
	target = unitsOrZero(target)
	need := new(big.Int).Add(obligations, target)
	surplus := new(big.Int).Sub(balance, need)
	if surplus.Sign() < 0 {
		surplus = big.NewInt(0)
	}
	headroom := new(big.Int).Sub(balance, new(big.Int).Add(obligations, floor))
	return Liquidity{
		Balance:       balance,
		Obligations:   obligations,
		ReserveFloor:  floor,
		ReserveTarget: target,
		Surplus:       surplus,
		Headroom:      headroom,
	}
}

// ApplyPay 在自主支付成功后扣减工作副本的余额和预算。
func ApplyPay(snap *Snapshot, category string, amount *big.Int) {
	if snap.Balance == nil {
		snap.Balance = big.NewInt(0)
	}
	snap.Balance.Sub(snap.Balance, amount)
	cat := snap.Categories[category]
	cat.Spent = new(big.Int).Add(unitsOrZero(cat.Spent), amount)
	cat.Remaining = new(big.Int).Sub(unitsOrZero(cat.Remaining), amount)
	if cat.Remaining.Sign() < 0 {
		cat.Remaining = big.NewInt(0)
	}
	snap.Categories[category] = cat
}

// ApplySweep 从工作副本余额中扣除划转金额。
func ApplySweep(snap *Snapshot, amount *big.Int) {
	if snap.Balance == nil {
		snap.Balance = big.NewInt(0)
	}
	snap.Balance.Sub(snap.Balance, amount)
}
