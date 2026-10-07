// 本文件处理还没进金库的升级账单。批准会走付款路径，重开和关闭不会。
package business

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/procurement"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

// ApproveBill 由所有者把一张离链升级的账单重新送进金库。不调用模型。
func ApproveBill(ctx context.Context, cfg treasury.Config, id string, flags BillFlags) (*models.Bill, error) {
	if flags.OverrideCap && strings.TrimSpace(flags.OverrideReason) == "" {
		return nil, fmt.Errorf("bill approve --override-cap requires --override-reason")
	}
	if cfg.Mode != "" && cfg.Mode != "dry-run" && !flags.Yes {
		return nil, fmt.Errorf("bill approve requires --yes for this one bill")
	}
	if !cycleMu.TryLock() {
		return nil, models.NewBusinessError("operator cycle already running")
	}
	defer cycleMu.Unlock()
	file := billsFile(cfg)
	bill, err := loadOneBill(id, file)
	if err != nil {
		return nil, err
	}
	if err := authorizeBills(ctx, cfg, flags, 1); err != nil {
		return nil, err
	}
	deps, err := depsFor(ctx, cfg)
	if err != nil {
		return nil, err
	}
	payErr := ownerPay(ctx, bill, deps, flags)
	if writeErr := writeBillLedger(file, bill); payErr == nil {
		payErr = writeErr
	}
	return bill, payErr
}

// ReopenBill 把离链升级、关闭或推迟的账单放回 quoted，下一轮会重新决策。
func ReopenBill(id, billsFilePath string) (*models.Bill, error) {
	if !cycleMu.TryLock() {
		return nil, models.NewBusinessError("operator cycle already running")
	}
	defer cycleMu.Unlock()
	bill, err := loadOneBill(id, billsFilePath)
	if err != nil {
		return nil, err
	}
	switch bill.State {
	case "done", "merchant_paid", "kept_as_credit", "vault_paid", "bridged":
		return nil, fmt.Errorf("bill is already paid")
	}
	if strings.TrimSpace(bill.VaultTx) != "" {
		return nil, fmt.Errorf("bill already has a vault request; settle it on Approvals")
	}
	switch bill.State {
	case "escalated", "closed", "deferred":
	default:
		return nil, fmt.Errorf("only an off-chain escalated, closed, or deferred bill can be reopened")
	}
	bill.State = "quoted"
	bill.DecisionHash = ""
	bill.Planner = ""
	bill.Action = ""
	bill.ReasonCode = ""
	bill.Reason = ""
	bill.Rationale = ""
	bill.ModelID = ""
	bill.PlannerAction = ""
	bill.PromptHash = ""
	bill.RiskNotes = ""
	bill.Confidence = ""
	bill.PlannerRaw = ""
	bill.LatencyMS = 0
	if err := models.SaveBill(bill); err != nil {
		return nil, err
	}
	if err := writeBillLedger(billsFilePath, bill); err != nil {
		return nil, err
	}
	return bill, nil
}

// CloseBill 关闭一张还没进金库的账单。
func CloseBill(id, billsFilePath string) (*models.Bill, error) {
	if !cycleMu.TryLock() {
		return nil, models.NewBusinessError("operator cycle already running")
	}
	defer cycleMu.Unlock()
	bill, err := loadOneBill(id, billsFilePath)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(bill.VaultTx) != "" {
		return nil, fmt.Errorf("bill already has a vault request; settle it on Approvals")
	}
	switch bill.State {
	case "escalated", "deferred", "quoted":
	default:
		return nil, fmt.Errorf("only an off-chain bill can be closed here")
	}
	bill.State = "closed"
	if bill.Action == "" {
		bill.Action = treasury.ActionReject
	}
	if bill.ReasonCode == "" {
		bill.ReasonCode = "owner_closed"
	}
	if bill.Reason == "" {
		bill.Reason = "owner closed the bill"
	}
	if err := saveProgress(billDeps{save: models.SaveBill}, bill); err != nil {
		return nil, err
	}
	if err := writeBillLedger(billsFilePath, bill); err != nil {
		return nil, err
	}
	return bill, nil
}

func ownerPay(ctx context.Context, bill *models.Bill, deps billDeps, flags BillFlags) error {
	if bill == nil {
		return fmt.Errorf("bill is missing")
	}
	if bill.State != "escalated" || strings.TrimSpace(bill.VaultTx) != "" {
		return fmt.Errorf("approve only an escalated bill that has not reached the vault")
	}
	if deps.quote == nil || deps.vault == nil {
		return fmt.Errorf("payment path is not configured")
	}
	if deps.buffer == nil {
		deps.buffer = big.NewInt(20_000)
	}
	quoted, maxFee, err := deps.quote(ctx, bill)
	if err != nil {
		return err
	}
	if quoted <= 0 {
		return fmt.Errorf("bill has no quote")
	}
	cost := procurement.CentsToUSDC(quoted)
	fee := new(big.Int).Add(units(maxFee), units(deps.buffer))
	amount := new(big.Int).Add(cost, fee)
	facts := procurement.Facts{QuoteCents: quoted, Amount: amount}
	if deps.facts != nil {
		facts = deps.facts(bill, quoted, amount)
		facts.QuoteCents = quoted
		facts.Amount = amount
	}
	hard := procurement.DecideHard(facts)
	if ownerPayBlocked(hard, flags) {
		if err := auditApproveBlocked(deps, bill, hard); err != nil {
			return err
		}
		return fmt.Errorf("%s: %s", hard.ReasonCode, hard.Reason)
	}
	rationale := "owner approved payment through the vault"
	if capOverride(hard.ReasonCode) {
		if err := auditCapOverride(deps, bill, hard.ReasonCode, flags.OverrideReason); err != nil {
			return err
		}
		rationale = "owner override of " + hard.ReasonCode + ": " + strings.TrimSpace(flags.OverrideReason)
	}
	d := treasury.Decision{
		PayableID: bill.Code, Action: treasury.ActionPay, Submit: true,
		ReasonCode: "owner_approved", Reason: "owner approved an off-chain escalation",
		Category: bill.CategoryCode, Payee: deps.payee, Amount: amount,
		Planner: "owner", ModelID: "owner", PlannerAction: treasury.ActionPay,
		Rationale: rationale, Confidence: "1",
	}
	hash, err := treasury.DecisionHash(treasury.Canonical{
		V: 2, AgentID: deps.policy.AgentID, ChainID: deps.policy.ChainID, Vault: deps.policy.Vault,
		PayableID: bill.Code, Action: d.Action, Category: bill.CategoryCode, Payee: deps.payee,
		AmountUnits: amount.String(), ReasonCode: d.ReasonCode, Planner: d.Planner,
		ModelID: d.ModelID, PlannerAction: d.PlannerAction, Rationale: d.Rationale,
		Confidence: d.Confidence,
	})
	if err != nil {
		return err
	}
	bill.QuoteCents = quoted
	bill.AmountUnits = amount.String()
	bill.FeeAllowanceUnits = fee.String()
	if bill.Mode == "" {
		bill.Mode = deps.cfg.Mode
	}
	if bill.Mode == "" {
		bill.Mode = "dry-run"
	}
	bill.DecisionHash = hash
	copyPlan(bill, d)
	commitSpend(deps, amount)
	bill.State = "decided"
	if err := recordBillDecision(bill, amount.String()); err != nil {
		return err
	}
	if err := auditDecision(deps, bill, false); err != nil {
		return err
	}
	if err := saveProgress(deps, bill); err != nil {
		return err
	}
	return executePay(ctx, bill, deps, cost, maxFee, amount)
}

func ownerPayBlocked(hard procurement.Hard, flags BillFlags) bool {
	switch hard.ReasonCode {
	case "monthly_limit", "daily_cap":
		return true
	case "max_bill", "max_spend_per_run":
		return !(flags.OverrideCap && strings.TrimSpace(flags.OverrideReason) != "")
	case "observe_failed", "insufficient_balance", "unknown_category", "payee_not_allowlisted", "quote_missing":
		return true
	default:
		return false
	}
}

func capOverride(reason string) bool {
	return reason == "max_bill" || reason == "max_spend_per_run"
}

func auditApproveBlocked(deps billDeps, bill *models.Bill, hard procurement.Hard) error {
	if deps.audit == nil || bill == nil {
		return nil
	}
	payload, err := json.Marshal(map[string]any{
		"bill_id": bill.Code, "decision_hash": bill.DecisionHash,
		"reason_code": hard.ReasonCode, "reason": hard.Reason,
	})
	if err != nil {
		return err
	}
	return deps.audit.Append(treasury.AuditEvent{
		RunID: bill.Code, Kind: "approve_blocked", DecisionHash: bill.DecisionHash,
		Outcome: hard.ReasonCode, Payload: payload,
	})
}

func auditCapOverride(deps billDeps, bill *models.Bill, reasonCode, reason string) error {
	if deps.audit == nil {
		return fmt.Errorf("--override-cap requires auditLog so the reason is recorded")
	}
	payload, err := json.Marshal(map[string]any{
		"bill_id": bill.Code, "reason_code": reasonCode, "reason": strings.TrimSpace(reason),
	})
	if err != nil {
		return err
	}
	return deps.audit.Append(treasury.AuditEvent{
		RunID: bill.Code, Kind: "owner_override", Outcome: reasonCode, Payload: payload,
	})
}

func billsFile(cfg treasury.Config) string {
	if strings.TrimSpace(cfg.BillsFile) == "" {
		return DefaultBillsFile
	}
	return cfg.BillsFile
}

func loadOneBill(id, billsFilePath string) (*models.Bill, error) {
	if err := models.EnsureStorage(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(billsFilePath) != "" {
		if err := importBillFile(billsFilePath); err != nil {
			return nil, err
		}
	}
	rows, err := selectBills([]string{strings.TrimSpace(id)})
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("pass one bill code")
	}
	return rows[0], nil
}

func writeBillLedger(billsFilePath string, bill *models.Bill) error {
	if strings.TrimSpace(billsFilePath) == "" || bill == nil {
		return nil
	}
	return upsertBillFile(billsFilePath, bill)
}
