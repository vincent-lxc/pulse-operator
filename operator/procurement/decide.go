// 本文件是域名账单的硬规则。模型之后只能在这些动作里选择，不能放宽。
package procurement

import (
	"math/big"
)

// Facts 是决策时已经核对过的公开数字。
type Facts struct {
	CategoryEnabled bool
	PayeeAllowed    bool
	Amount          *big.Int
	Remaining       *big.Int
	PerTxCap        *big.Int
	MonthlySpent    int64
	MonthlyLimit    int64
	QuoteCents      int64
	PreviousCents   int64
	ToleranceCents  int64
	DailyCount      int
	DailyCap        int
	Requoted        bool
	Balance         *big.Int
	MaxBill         *big.Int
	MaxSpend        *big.Int
	RunSpent        *big.Int
}

// Hard 是代码给出的动作。Submit 为真时才会调用 PolicyVault.pay。
type Hard struct {
	Action     string
	Submit     bool
	ReasonCode string
	Reason     string
}

// DecideHard 按固定顺序检查。超上限仍提交 pay，让金库生成审批；其余失败不提交。
func DecideHard(f Facts) Hard {
	if f.QuoteCents <= 0 {
		return Hard{Action: "escalate_to_human", ReasonCode: "quote_missing", Reason: "domain quote is missing"}
	}
	if f.Requoted && f.PreviousCents > 0 && abs64(f.QuoteCents-f.PreviousCents) > f.ToleranceCents {
		return Hard{Action: "escalate_to_human", ReasonCode: "price_drift", Reason: "re-quote moved outside the tolerance"}
	}
	if !f.CategoryEnabled {
		return Hard{Action: "escalate_to_human", ReasonCode: "unknown_category", Reason: "category is not enabled"}
	}
	if !f.PayeeAllowed {
		return Hard{Action: "escalate_to_human", ReasonCode: "payee_not_allowlisted", Reason: "procurement wallet is not an allowlisted payee"}
	}
	if f.DailyCap > 0 && f.DailyCount >= f.DailyCap {
		return Hard{Action: "escalate_to_human", ReasonCode: "daily_cap", Reason: "operator daily domain cap is already used"}
	}
	if f.MonthlyLimit > 0 && f.MonthlySpent+f.QuoteCents > f.MonthlyLimit {
		return Hard{Action: "escalate_to_human", ReasonCode: "monthly_limit", Reason: "quote would pass the Porkbun monthly limit"}
	}
	amount := unitsOf(f.Amount)
	if f.MaxBill != nil && f.MaxBill.Sign() > 0 && amount.Cmp(f.MaxBill) > 0 {
		return Hard{Action: "escalate_to_human", ReasonCode: "max_bill", Reason: "amount is above maxBillUSDC"}
	}
	if f.MaxSpend != nil && f.MaxSpend.Sign() > 0 {
		next := new(big.Int).Add(unitsOf(f.RunSpent), amount)
		if next.Cmp(f.MaxSpend) > 0 {
			return Hard{Action: "escalate_to_human", ReasonCode: "max_spend_per_run", Reason: "amount would pass maxSpendPerRunUSDC"}
		}
	}
	if f.PerTxCap != nil && f.PerTxCap.Sign() > 0 && amount.Cmp(f.PerTxCap) > 0 {
		return Hard{Action: "escalate_to_human", Submit: true, ReasonCode: "over_tx_cap", Reason: "amount is above the category per-transaction cap"}
	}
	if f.Remaining != nil && amount.Cmp(f.Remaining) > 0 {
		return Hard{Action: "escalate_to_human", Submit: true, ReasonCode: "over_budget", Reason: "amount is above the remaining category budget"}
	}
	return Hard{Action: "pay", Submit: true, ReasonCode: "within_policy", Reason: "quote fits the category budget, cap, and operator limits"}
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func unitsOf(v *big.Int) *big.Int {
	if v == nil {
		return big.NewInt(0)
	}
	return v
}
