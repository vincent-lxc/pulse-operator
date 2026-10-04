// 本文件跑完整的观察、评估、决策、执行循环。链写入只发生在 Chain 实现内部。
package treasury

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"
)

// LoopInput 是一轮循环的输入。LastPaid 来自上一轮持久化的冷却记录。
type LoopInput struct {
	Policy        Policy
	Payables      []Payable
	Chain         Chain
	Advisor       Advisor
	Notifier      Notifier
	Audit         *AuditLog
	LastPaid      map[string]time.Time
	Manual        []Inflow
	CircleProduct string
	LimitNote     string
}

// Run 执行一轮。paid 和 closed 不再进入义务；仍在审批中的 escalated 会计入义务。
func Run(ctx context.Context, in LoopInput) (Report, error) {
	if in.Chain == nil {
		return Report{}, fmt.Errorf("chain client is required")
	}
	if in.Advisor == nil {
		in.Advisor = NopAdvisor{}
	}
	if in.Notifier == nil {
		in.Notifier = &LogNotifier{}
	}
	runID, err := newRunID()
	if err != nil {
		return Report{}, err
	}
	snap, err := in.Chain.Observe(ctx)
	if err != nil {
		return Report{}, err
	}
	snap.Balance = unitsOrZero(snap.Balance)
	manualAdded := applyManual(&snap, in.Manual)
	if err := auditPayload(in.Audit, runID, "observation", "", "", "", in.CircleProduct, map[string]any{
		"balance":       FormatUSDC(snap.Balance),
		"paused":        snap.Paused,
		"reserve":       snap.Reserve,
		"inflows":       len(snap.Inflows),
		"manual_added":  FormatUSDC(manualAdded),
		"block":         snap.Block,
		"circle":        in.CircleProduct,
		"circle_limits": in.LimitNote,
	}); err != nil {
		return Report{}, err
	}
	lastPaid := map[string]time.Time{}
	for k, v := range in.LastPaid {
		lastPaid[NormalizeAddress(k)] = v
	}
	open := make([]Payable, 0, len(in.Payables))
	var obligated = big.NewInt(0)
	for _, p := range in.Payables {
		switch p.Status {
		case "paid", "closed":
			continue
		case "escalated":
			if DueWithin(p, in.Policy.Now, in.Policy.Horizon) {
				obligated.Add(obligated, unitsOrZero(p.Amount))
			}
			continue
		}
		open = append(open, p)
	}
	sort.SliceStable(open, func(i, j int) bool {
		if open[i].Due.Equal(open[j].Due) {
			return open[i].ID < open[j].ID
		}
		return open[i].Due.Before(open[j].Due)
	})
	pre := Assess(snap.Balance, sumDue(open, in.Policy, obligated), in.Policy.ReserveFloor, in.Policy.ReserveTarget)
	if err := auditPayload(in.Audit, runID, "liquidity", "", "", "", in.CircleProduct, map[string]any{
		"balance":        FormatUSDC(pre.Balance),
		"obligations":    FormatUSDC(pre.Obligations),
		"reserve_floor":  FormatUSDC(pre.ReserveFloor),
		"reserve_target": FormatUSDC(pre.ReserveTarget),
		"surplus":        FormatUSDC(pre.Surplus),
	}); err != nil {
		return Report{}, err
	}
	report := Report{
		RunID:            runID,
		ObservedAt:       in.Policy.Now,
		Balance:          unitsOrZero(snap.Balance),
		OpeningBalance:   new(big.Int).Set(unitsOrZero(snap.Balance)),
		Block:            snap.Block,
		Inflows:          append([]Inflow(nil), snap.Inflows...),
		Pending:          append([]Approval(nil), snap.Pending...),
		OpeningLiquidity: pre,
		Categories:       snap.Categories,
		PayeePaidAt:      lastPaid,
	}
	var stillDue = new(big.Int).Set(obligated)
	spentThisRun := big.NewInt(0)
	for _, p := range open {
		d := DecidePayable(p, snap, in.Policy, lastPaid)
		if d.Action == ActionPay && in.Policy.MaxSpendPerRun != nil {
			next := new(big.Int).Add(spentThisRun, unitsOrZero(d.Amount))
			if next.Cmp(in.Policy.MaxSpendPerRun) > 0 {
				d = deferDecision(d, ReasonMaxSpend, fmt.Sprintf("paying %s would put this run at %s, above max spend %s", FormatUSDC(d.Amount), FormatUSDC(next), FormatUSDC(in.Policy.MaxSpendPerRun)))
			} else {
				spentThisRun = next
			}
		}
		advice, adviseErr := in.Advisor.Advise(ctx, d)
		d = Annotate(d, advice, adviseErr)
		hash, err := seal(in.Policy, d)
		if err != nil {
			return Report{}, err
		}
		d.DecisionHash = hash
		d.Product = in.CircleProduct
		if d.Submit {
			callHash, err := Hash32(hash)
			if err != nil {
				return Report{}, err
			}
			res, err := in.Chain.Pay(ctx, PayCall{
				Category:     d.Category,
				Payee:        d.Payee,
				Amount:       d.Amount,
				DecisionHash: callHash,
			})
			d.CircleTxID = res.CircleTxID
			d.CircleState = res.CircleState
			if err != nil {
				if errors.Is(err, ErrAgentKeyRequired) {
					d.Outcome = "agent_key_required"
				} else {
					d.Outcome = "error"
				}
				d.Reason = d.Reason + "; execution: " + err.Error()
			} else {
				d.Outcome = res.Status
				d.TxHash = res.TxHash
				d.RequestID = res.RequestID
				d.Calldata = res.Calldata
				if res.Product != "" {
					d.Product = res.Product
				}
				if res.Status == "simulated_paid" || res.Status == "paid" || res.Status == "circle_confirmed" || res.Status == "dry_run_paid" {
					ApplyPay(&snap, d.Category, d.Amount)
					lastPaid[d.Payee] = in.Policy.Now
				}
			}
		} else {
			d.Outcome = "recorded"
		}
		if d.Action == ActionEscalate {
			_ = in.Notifier.Notify(ctx, Notice{Kind: "escalation", Text: escalationText(in.Policy.ChainID, d)})
		}
		if d.Action != ActionPay && DueWithin(p, in.Policy.Now, in.Policy.Horizon) {
			stillDue.Add(stillDue, unitsOrZero(d.Amount))
		}
		if err := writeDecision(in.Audit, runID, d); err != nil {
			return Report{}, err
		}
		report.Decisions = append(report.Decisions, d)
	}
	liq := Assess(snap.Balance, stillDue, in.Policy.ReserveFloor, in.Policy.ReserveTarget)
	sweep := Decision{
		PayableID:  "cycle",
		Action:     ActionSweep,
		Category:   "",
		Payee:      snap.Reserve,
		Amount:     unitsOrZero(liq.Surplus),
		ReasonCode: ReasonSurplus,
		Reason:     "balance exceeds unpaid due obligations plus reserve target",
	}
	if liq.Surplus.Sign() <= 0 {
		sweep.Action = ActionDefer
		sweep.ReasonCode = ReasonNoSurplus
		sweep.Reason = "no surplus above reserve target and unpaid obligations"
		sweep.Amount = big.NewInt(0)
	}
	hash, err := seal(in.Policy, sweep)
	if err != nil {
		return Report{}, err
	}
	sweep.DecisionHash = hash
	sweep.Product = in.CircleProduct
	if sweep.Action == ActionSweep {
		callHash, err := Hash32(hash)
		if err != nil {
			return Report{}, err
		}
		res, err := in.Chain.Sweep(ctx, SweepCall{Amount: sweep.Amount, DecisionHash: callHash, Reserve: snap.Reserve})
		sweep.CircleTxID = res.CircleTxID
		sweep.CircleState = res.CircleState
		if err != nil {
			if errors.Is(err, ErrOwnerKeyRequired) {
				sweep.Outcome = "owner_key_required"
			} else {
				sweep.Outcome = "error"
			}
			sweep.Reason = sweep.Reason + "; execution: " + err.Error()
		} else {
			sweep.Outcome = res.Status
			sweep.TxHash = res.TxHash
			sweep.Calldata = res.Calldata
			if res.Product != "" {
				sweep.Product = res.Product
			}
			if res.Status == "simulated_swept" || res.Status == "swept" {
				ApplySweep(&snap, sweep.Amount)
			}
		}
		_ = in.Notifier.Notify(ctx, Notice{Kind: "sweep", Text: fmt.Sprintf("sweep %s to %s", FormatUSDC(sweep.Amount), snap.Reserve)})
	} else {
		sweep.Outcome = "recorded"
	}
	if err := writeDecision(in.Audit, runID, sweep); err != nil {
		return Report{}, err
	}
	report.Decisions = append(report.Decisions, sweep)
	report.Liquidity = Assess(snap.Balance, stillDue, in.Policy.ReserveFloor, in.Policy.ReserveTarget)
	report.Balance = unitsOrZero(snap.Balance)
	report.PayeePaidAt = lastPaid
	report.Categories = snap.Categories
	if n, ok := in.Notifier.(*LogNotifier); ok {
		report.Notices = append([]Notice(nil), n.Items...)
	}
	return report, nil
}

func sumDue(open []Payable, policy Policy, already *big.Int) *big.Int {
	total := new(big.Int).Set(unitsOrZero(already))
	for _, p := range open {
		if DueWithin(p, policy.Now, policy.Horizon) {
			total.Add(total, unitsOrZero(p.Amount))
		}
	}
	return total
}

func applyManual(snap *Snapshot, manual []Inflow) *big.Int {
	seen := map[string]bool{}
	for _, in := range snap.Inflows {
		seen[in.TxHash] = true
	}
	added := big.NewInt(0)
	for _, in := range manual {
		if in.TxHash != "" && seen[in.TxHash] {
			continue
		}
		snap.Inflows = append(snap.Inflows, in)
		added.Add(added, unitsOrZero(in.Amount))
	}
	snap.Balance.Add(snap.Balance, added)
	return added
}

func seal(policy Policy, d Decision) (string, error) {
	amount := "0"
	if d.Amount != nil {
		amount = d.Amount.String()
	}
	payee := d.Payee
	if payee == "" {
		payee = "cycle"
	}
	return DecisionHash(Canonical{
		V:           1,
		AgentID:     policy.AgentID,
		ChainID:     policy.ChainID,
		Vault:       policy.Vault,
		PayableID:   d.PayableID,
		Action:      d.Action,
		Category:    d.Category,
		Payee:       payee,
		AmountUnits: amount,
		ReasonCode:  d.ReasonCode,
	})
}

func writeDecision(log *AuditLog, runID string, d Decision) error {
	if err := auditPayload(log, runID, "decision", d.DecisionHash, "", "", d.Product, d); err != nil {
		return err
	}
	return auditPayload(log, runID, "execution", d.DecisionHash, d.TxHash, d.Outcome, d.Product, map[string]any{
		"action":       d.Action,
		"payable_id":   d.PayableID,
		"request_id":   d.RequestID,
		"circle":       d.Product,
		"circle_tx_id": d.CircleTxID,
		"circle_state": d.CircleState,
	})
}

func auditPayload(log *AuditLog, runID, kind, decisionHash, txHash, outcome, circle string, payload any) error {
	if log == nil {
		return nil
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return log.Append(AuditEvent{
		RunID:        runID,
		Kind:         kind,
		DecisionHash: decisionHash,
		TxHash:       txHash,
		Outcome:      outcome,
		Circle:       circle,
		Payload:      b,
	})
}

func newRunID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "run-" + hex.EncodeToString(b[:]), nil
}
