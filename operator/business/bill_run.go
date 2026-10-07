// 本文件推进一张域名账单。硬规则先算，模型只能在允许的动作里选择，哈希在付款前落库。
package business

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/planner"
	"github.com/vincent-lxc/pulse-operator/operator/procurement"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

type vaultResult struct {
	TxHash    string
	RequestID string
	Status    string
}

type burnResult struct {
	BurnTx      string
	MintTx      string
	MessageHash string
	Nonce       string
	ForwardFee  string
	CircleTxID  string
}

type merchantResult struct {
	OrderID      string
	CheckoutID   string
	KeptAsCredit bool
	BalanceCents int64
	Scheme       string
	Payer        string
	Receipt      string
	Pending      bool
}

type billDeps struct {
	quote    func(context.Context, *models.Bill) (int64, *big.Int, error)
	vault    func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error)
	burn     func(context.Context, *models.Bill, *big.Int, *big.Int) (burnResult, error)
	merchant func(context.Context, *models.Bill) (merchantResult, error)
	facts    func(*models.Bill, int64, *big.Int) procurement.Facts
	plan     treasury.PlanFunc
	cfg      treasury.Config
	policy   treasury.Policy
	payee    string
	buffer   *big.Int
	save     func(*models.Bill) error
	audit    *treasury.AuditLog
}

// AdvanceBill 把账单从报价推进到完成。已经完成的账单不会再次付款。
func AdvanceBill(ctx context.Context, bill *models.Bill, deps billDeps) error {
	if bill == nil {
		return fmt.Errorf("bill is missing")
	}
	if terminal(bill.State) || (bill.State == "escalated" && bill.VaultTx != "") {
		return nil
	}
	if deps.save == nil {
		deps.save = models.SaveBill
	}
	if deps.buffer == nil {
		deps.buffer = big.NewInt(20_000)
	}
	quoted, maxFee, err := deps.quote(ctx, bill)
	if err != nil {
		return stop(bill, deps, "escalate_to_human", false, "quote_failed", err.Error())
	}
	previous := bill.QuoteCents
	requoted := previous > 0 && previous != quoted
	cost := procurement.CentsToUSDC(quoted)
	fee := new(big.Int).Add(units(maxFee), units(deps.buffer))
	amount := new(big.Int).Add(cost, fee)
	facts := procurement.Facts{QuoteCents: quoted, PreviousCents: previous, Requoted: requoted}
	if deps.facts != nil {
		facts = deps.facts(bill, quoted, amount)
		facts.QuoteCents = quoted
		facts.PreviousCents = previous
		facts.Requoted = requoted
		facts.Amount = amount
	}
	hard := procurement.DecideHard(facts)
	bill.QuoteCents = quoted
	bill.AmountUnits = amount.String()
	bill.FeeAllowanceUnits = fee.String()
	bill.Mode = deps.cfg.Mode
	if bill.Mode == "" {
		bill.Mode = "dry-run"
	}
	if !hashLocked(bill) {
		d := treasury.Decision{
			PayableID: bill.Code, Action: hard.Action, Submit: hard.Submit,
			ReasonCode: hard.ReasonCode, Reason: hard.Reason, Category: bill.CategoryCode,
			Payee: deps.payee, Amount: amount,
		}
		d = applyPlanner(ctx, deps.cfg, deps.plan, d, billInput(bill, facts, hard))
		hash, err := treasury.DecisionHash(treasury.Canonical{
			V: 2, AgentID: deps.policy.AgentID, ChainID: deps.policy.ChainID, Vault: deps.policy.Vault,
			PayableID: bill.Code, Action: d.Action, Category: bill.CategoryCode, Payee: deps.payee,
			AmountUnits: amount.String(), ReasonCode: d.ReasonCode, Planner: empty(d.Planner, "rules"),
			ModelID: empty(d.ModelID, "rules"), PlannerAction: empty(d.PlannerAction, d.Action),
			Rationale: empty(d.Rationale, d.Reason), PromptHash: d.PromptHash, RiskNotes: d.RiskNotes,
			Confidence: d.Confidence, Disagree: d.Disagree,
		})
		if err != nil {
			return err
		}
		bill.DecisionHash = hash
		copyPlan(bill, d)
		if !d.Submit || (d.Action != treasury.ActionPay && d.Action != treasury.ActionEscalate) {
			return stop(bill, deps, d.Action, false, d.ReasonCode, d.Reason)
		}
		bill.State = "decided"
		if err := deps.save(bill); err != nil {
			return err
		}
		if err := writeBillAudit(deps.audit, bill, "decision"); err != nil {
			return err
		}
		if d.Action == treasury.ActionEscalate {
			return submitEscalation(ctx, bill, deps, d, amount)
		}
	} else if bill.Action != treasury.ActionPay {
		return nil
	}
	if bill.VaultTx == "" {
		res, err := deps.vault(ctx, bill, amount, bill.DecisionHash)
		if err != nil {
			if strings.Contains(err.Error(), "DecisionAlreadyUsed") {
				bill.State = "failed_vault"
				bill.Reason = "decision hash was already used"
				_ = deps.save(bill)
				return nil
			}
			bill.State = "failed_vault"
			bill.Reason = err.Error()
			_ = deps.save(bill)
			return err
		}
		bill.VaultTx = res.TxHash
		bill.ArcURL = explorerArc(deps.policy.ChainID, res.TxHash)
		if res.Status == "approval" || (res.RequestID != "" && res.Status != "paid" && res.Status != "simulated_paid" && res.Status != "dry_run_paid" && res.Status != "circle_confirmed") {
			bill.State = "escalated"
			_ = deps.save(bill)
			return nil
		}
		bill.State = "vault_paid"
		if err := deps.save(bill); err != nil {
			return err
		}
	}
	if bill.BaseMintTx == "" {
		burned, err := deps.burn(ctx, bill, cost, maxFee)
		if burned.BurnTx != "" {
			bill.CCTPBurnTx = burned.BurnTx
			bill.BaseMintTx = burned.MintTx
			bill.CCTPMessageHash = burned.MessageHash
			bill.CCTPNonce = burned.Nonce
			bill.ForwardFeeUnits = burned.ForwardFee
			if burned.CircleTxID != "" {
				bill.CircleTxIDs = joinNote(bill.CircleTxIDs, burned.CircleTxID)
			}
			bill.BaseURL = explorerBase(deps.cfg.Base.ChainID, burned.MintTx)
		}
		if err != nil {
			bill.State = "failed_bridge"
			bill.Reason = err.Error()
			_ = deps.save(bill)
			return err
		}
		bill.CCTPBurnTx = burned.BurnTx
		bill.BaseMintTx = burned.MintTx
		bill.CCTPMessageHash = burned.MessageHash
		bill.CCTPNonce = burned.Nonce
		bill.ForwardFeeUnits = burned.ForwardFee
		if burned.CircleTxID != "" {
			bill.CircleTxIDs = joinNote(bill.CircleTxIDs, burned.CircleTxID)
		}
		bill.BaseURL = explorerBase(deps.cfg.Base.ChainID, burned.MintTx)
		bill.State = "bridged"
		if err := deps.save(bill); err != nil {
			return err
		}
	}
	if bill.PorkbunOrderID == "" && bill.State != "kept_as_credit" {
		paid, err := deps.merchant(ctx, bill)
		if err != nil {
			bill.State = "failed_merchant"
			bill.Reason = err.Error()
			_ = deps.save(bill)
			return err
		}
		bill.PorkbunCheckoutID = paid.CheckoutID
		bill.X402Scheme = paid.Scheme
		bill.X402Payer = paid.Payer
		bill.X402Receipt = paid.Receipt
		bill.PorkbunBalanceCents = paid.BalanceCents
		if paid.KeptAsCredit {
			bill.State = "kept_as_credit"
			bill.PorkbunURL = "https://porkbun.com/account/domains"
			_ = deps.save(bill)
			return writeBillAudit(deps.audit, bill, "bill")
		}
		if paid.Pending || paid.OrderID == "" {
			bill.State = "bridged"
			bill.ReasonCode = "payment_pending"
			_ = deps.save(bill)
			return nil
		}
		bill.PorkbunOrderID = paid.OrderID
		bill.State = "merchant_paid"
	}
	bill.State = "done"
	bill.PaidAt = time.Now().UTC().Format(time.RFC3339)
	bill.PorkbunURL = "https://porkbun.com/account/domains"
	bill.Evidence = evidence(bill)
	if err := deps.save(bill); err != nil {
		return err
	}
	return writeBillAudit(deps.audit, bill, "bill")
}

func submitEscalation(ctx context.Context, bill *models.Bill, deps billDeps, d treasury.Decision, amount *big.Int) error {
	if !d.Submit {
		return stop(bill, deps, d.Action, false, d.ReasonCode, d.Reason)
	}
	res, err := deps.vault(ctx, bill, amount, bill.DecisionHash)
	if err != nil {
		bill.State = "failed_vault"
		bill.Reason = err.Error()
		_ = deps.save(bill)
		return err
	}
	bill.VaultTx = res.TxHash
	bill.ArcURL = explorerArc(deps.policy.ChainID, res.TxHash)
	bill.State = "escalated"
	bill.Action = treasury.ActionEscalate
	return deps.save(bill)
}

func stop(bill *models.Bill, deps billDeps, action string, _ bool, code, reason string) error {
	bill.Action = action
	bill.ReasonCode = code
	bill.Reason = reason
	switch action {
	case treasury.ActionReject:
		bill.State = "closed"
	case treasury.ActionDefer:
		bill.State = "deferred"
	default:
		bill.State = "escalated"
	}
	bill.Evidence = evidence(bill)
	if err := deps.save(bill); err != nil {
		return err
	}
	return writeBillAudit(deps.audit, bill, "bill")
}

func copyPlan(bill *models.Bill, d treasury.Decision) {
	bill.Action = d.Action
	bill.ReasonCode = d.ReasonCode
	bill.Reason = d.Reason
	bill.Rationale = d.Rationale
	bill.ModelID = d.ModelID
	bill.PlannerAction = d.PlannerAction
	bill.PromptHash = d.PromptHash
	bill.RiskNotes = d.RiskNotes
	bill.Confidence = d.Confidence
	bill.PlannerRaw = d.PlannerRaw
	bill.LatencyMS = d.LatencyMS
	if d.SoftNote != "" {
		bill.RiskNotes = joinNote(bill.RiskNotes, d.SoftNote)
	}
	if d.Disagree {
		bill.RiskNotes = joinNote(bill.RiskNotes, "llm_disagreed")
	}
}

func hashLocked(bill *models.Bill) bool {
	if bill.DecisionHash == "" {
		return false
	}
	switch bill.State {
	case "decided", "paying", "vault_paid", "bridged", "merchant_paid", "done", "failed_bridge", "failed_merchant", "failed_vault", "kept_as_credit":
		return true
	default:
		return bill.VaultTx != ""
	}
}

func terminal(state string) bool {
	switch state {
	case "done", "closed", "kept_as_credit":
		return true
	default:
		return false
	}
}

func billInput(bill *models.Bill, facts procurement.Facts, hard procurement.Hard) planner.PublicInput {
	return planner.PublicInput{
		Kind: "bill", Vendor: bill.Vendor,
		Bill: map[string]any{
			"id": bill.Code, "domain": bill.Domain, "kind": bill.Kind, "years": bill.Years,
			"quote_cents": facts.QuoteCents, "category": bill.CategoryCode,
		},
		Vault: map[string]any{
			"remaining": treasury.FormatUSDC(facts.Remaining), "per_tx_cap": treasury.FormatUSDC(facts.PerTxCap),
			"monthly_spent_cents": facts.MonthlySpent, "monthly_limit_cents": facts.MonthlyLimit,
			"daily_count": facts.DailyCount, "daily_cap": facts.DailyCap,
		},
		History: []map[string]any{}, Allowed: treasury.AllowedActions(hard.Action),
		HardAction: hard.Action, HardReason: hard.Reason,
	}
}

func units(v *big.Int) *big.Int {
	if v == nil {
		return big.NewInt(0)
	}
	return new(big.Int).Set(v)
}

func empty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func explorerArc(chainID, tx string) string {
	if tx == "" || strings.HasPrefix(tx, "dry-") {
		return ""
	}
	if chainID == "5042" {
		return "https://explorer.arc.io/tx/" + tx
	}
	return "https://explorer.testnet.arc.io/tx/" + tx
}

func explorerBase(chainID, tx string) string {
	if tx == "" || strings.HasPrefix(tx, "dry-") {
		return ""
	}
	if chainID == "8453" || chainID == "" {
		return "https://basescan.org/tx/" + tx
	}
	return "https://sepolia.basescan.org/tx/" + tx
}

func evidence(bill *models.Bill) string {
	raw, _ := json.MarshalIndent(map[string]any{
		"bill_id": bill.Code, "vendor": bill.Vendor, "domain": bill.Domain, "kind": bill.Kind,
		"quote_cents": bill.QuoteCents, "decision_hash": bill.DecisionHash, "action": bill.Action,
		"rationale": bill.Rationale, "model_id": bill.ModelID, "prompt_hash": bill.PromptHash,
		"risk_notes": bill.RiskNotes, "confidence": bill.Confidence, "vault_tx": bill.VaultTx,
		"vault_chain": 5042, "cctp_burn_tx": bill.CCTPBurnTx, "cctp_message": bill.CCTPMessageHash,
		"base_mint_tx": bill.BaseMintTx, "forward_fee": bill.ForwardFeeUnits, "x402_scheme": bill.X402Scheme,
		"x402_payer": bill.X402Payer, "porkbun_order_id": bill.PorkbunOrderID, "mode": bill.Mode,
		"state": bill.State,
	}, "", "  ")
	return string(raw)
}

func writeBillAudit(log *treasury.AuditLog, bill *models.Bill, kind string) error {
	if log == nil || bill == nil {
		return nil
	}
	raw := []byte(bill.Evidence)
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	return log.Append(treasury.AuditEvent{
		RunID: bill.Code, Kind: kind, DecisionHash: bill.DecisionHash, TxHash: bill.VaultTx,
		Outcome: bill.State, Payload: raw,
	})
}

// EvidenceMarkdown 是给操作者留档的短记录。
func EvidenceMarkdown(bill *models.Bill) string {
	if bill == nil {
		return ""
	}
	return fmt.Sprintf(`# %s %s

- state: %s
- decision: %s
- model: %s
- rationale: %s
- vault: %s
- burn: %s
- base mint: %s
- porkbun order: %s
- mode: %s
`, bill.Domain, bill.Code, bill.State, bill.DecisionHash, bill.ModelID, bill.Rationale, bill.VaultTx, bill.CCTPBurnTx, bill.BaseMintTx, bill.PorkbunOrderID, bill.Mode)
}
