package business

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/procurement"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

func TestApplyObservationFailClosed(t *testing.T) {
	base := procurement.Facts{
		CategoryEnabled: true, PayeeAllowed: true,
		Balance: big.NewInt(20_000_000), Remaining: big.NewInt(30_000_000), PerTxCap: big.NewInt(15_000_000),
	}
	got := applyObservation(base, "domains", "0x0000000000000000000000000000000000000001", treasury.Snapshot{}, fmt.Errorf("rpc down"), nil)
	if !got.ObserveFailed || got.CategoryEnabled || got.PayeeAllowed || got.Balance != nil || got.Remaining != nil || got.PerTxCap != nil {
		t.Fatalf("%+v", got)
	}
	ok := applyObservation(base, "domains", "0x0000000000000000000000000000000000000001", treasury.Snapshot{
		Balance: big.NewInt(9_000_000),
		Categories: map[string]treasury.Category{
			"domains": {Enabled: true, Remaining: big.NewInt(12_000_000), PerTxCap: big.NewInt(5_000_000), Payees: map[string]bool{
				"0x0000000000000000000000000000000000000001": true,
			}},
		},
	}, nil, big.NewInt(1_000_000))
	if ok.ObserveFailed || !ok.CategoryEnabled || !ok.PayeeAllowed || ok.Balance.Cmp(big.NewInt(8_000_000)) != 0 || ok.Remaining.Cmp(big.NewInt(11_000_000)) != 0 {
		t.Fatalf("%+v", ok)
	}
}

func TestObserveFailureDoesNotCallVault(t *testing.T) {
	bill := newOpenBill(t, "bill-observe-"+t.Name())
	var pays int
	deps := generousDeps(t)
	deps.facts = func(*models.Bill, int64, *big.Int) procurement.Facts {
		return procurement.Facts{ObserveFailed: true, CategoryEnabled: true, PayeeAllowed: true, QuoteCents: 875}
	}
	deps.vault = func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
		pays++
		return vaultResult{TxHash: "should-not-send"}, nil
	}
	if err := AdvanceBill(context.Background(), bill, deps); err != nil {
		t.Fatal(err)
	}
	if pays != 0 || bill.State != "escalated" || bill.ReasonCode != "observe_failed" || bill.VaultTx != "" {
		t.Fatalf("pays=%d state=%s code=%s vault=%s", pays, bill.State, bill.ReasonCode, bill.VaultTx)
	}
}

func TestDryRunBalanceStopsTheNextBill(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "vault.yaml")
	if err := os.WriteFile(fixture, []byte(`
balance_usdc: "10.00"
categories:
  - name: domains
    budget_usdc: "30.00"
    per_tx_cap_usdc: "15.00"
    spent_usdc: "0"
    period_seconds: 1209600
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := treasury.Config{
		Mode: "dry-run", VaultFixture: fixture, ChainID: "5042002",
		Porkbun: treasury.PorkbunConfig{MonthlyLimitCents: 100000, DailyCap: 10},
	}
	var pays int
	deps := generousDeps(t)
	deps.cfg = cfg
	deps.runSpent = big.NewInt(0)
	deps.facts = func(bill *models.Bill, cents int64, amount *big.Int) procurement.Facts {
		return dryFacts(bill, cents, amount, cfg, deps.runSpent)
	}
	deps.vault = func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
		pays++
		return vaultResult{TxHash: "dry-vault", Status: "paid"}, nil
	}
	first := newOpenBill(t, "bill-bal-a-"+t.Name())
	second := newOpenBill(t, "bill-bal-b-"+t.Name())
	if err := AdvanceBill(context.Background(), first, deps); err != nil {
		t.Fatal(err)
	}
	if first.State != "done" || first.Action != treasury.ActionPay {
		t.Fatalf("first %s %s", first.State, first.ReasonCode)
	}
	if err := AdvanceBill(context.Background(), second, deps); err != nil {
		t.Fatal(err)
	}
	if pays != 1 || second.State != "escalated" || second.ReasonCode != "insufficient_balance" || second.VaultTx != "" {
		t.Fatalf("pays=%d second %s %s vault=%s", pays, second.State, second.ReasonCode, second.VaultTx)
	}
}

func TestOwnerCanApproveReopenOrCloseOffChainBill(t *testing.T) {
	escalated := escalateOffChain(t, "bill-approve-"+t.Name())
	var pays int
	deps := payingDeps(t)
	deps.vault = func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
		pays++
		return vaultResult{TxHash: "dry-vault-owner", Status: "paid"}, nil
	}
	if err := ownerPay(context.Background(), escalated, deps, BillFlags{}); err != nil {
		t.Fatal(err)
	}
	if pays != 1 || escalated.State != "done" || escalated.Planner != "owner" || escalated.ReasonCode != "owner_approved" {
		t.Fatalf("pays=%d state=%s planner=%s code=%s", pays, escalated.State, escalated.Planner, escalated.ReasonCode)
	}
	row, err := models.FindDecision(escalated.DecisionHash)
	if err != nil || row == nil || row.Outcome != "done" {
		t.Fatalf("outcome %+v %v", row, err)
	}
	again, err := treasury.DecisionHash(treasury.Canonical{
		V: 2, AgentID: deps.policy.AgentID, ChainID: deps.policy.ChainID, Vault: deps.policy.Vault,
		PayableID: escalated.Code, Action: escalated.Action, Category: escalated.CategoryCode, Payee: deps.payee,
		AmountUnits: escalated.AmountUnits, ReasonCode: escalated.ReasonCode, Planner: escalated.Planner,
		ModelID: escalated.ModelID, PlannerAction: escalated.PlannerAction, Rationale: escalated.Rationale,
		Confidence: escalated.Confidence,
	})
	if err != nil || again != escalated.DecisionHash {
		t.Fatalf("hash stored %s rebuilt %s err %v", escalated.DecisionHash, again, err)
	}

	blocked := escalateOffChain(t, "bill-approve-block-"+t.Name())
	deps = payingDeps(t)
	deps.facts = func(*models.Bill, int64, *big.Int) procurement.Facts {
		return procurement.Facts{ObserveFailed: true, QuoteCents: 875}
	}
	deps.vault = func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
		t.Fatal("observe failure reached the vault")
		return vaultResult{}, nil
	}
	if err := ownerPay(context.Background(), blocked, deps, BillFlags{}); err == nil || !strings.Contains(err.Error(), "observe_failed") || blocked.VaultTx != "" {
		t.Fatalf("err=%v vault=%s", err, blocked.VaultTx)
	}

	reopenedBill := escalateOffChain(t, "bill-reopen-"+t.Name())
	reopened, err := ReopenBill(reopenedBill.Code, "")
	if err != nil {
		t.Fatal(err)
	}
	if reopened.State != "quoted" || reopened.DecisionHash != "" || reopened.Planner != "" {
		t.Fatalf("%+v", reopened)
	}
	deps = payingDeps(t)
	if err := AdvanceBill(context.Background(), reopened, deps); err != nil {
		t.Fatal(err)
	}
	if reopened.State != "done" || reopened.Planner != "rules" {
		t.Fatalf("reopened %s %s", reopened.State, reopened.Planner)
	}

	closedBill := escalateOffChain(t, "bill-close-"+t.Name())
	var closes int
	deps = payingDeps(t)
	deps.vault = func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
		closes++
		return vaultResult{}, nil
	}
	closed, err := CloseBill(closedBill.Code, "")
	if err != nil {
		t.Fatal(err)
	}
	if closes != 0 || closed.State != "closed" {
		t.Fatalf("closes=%d state=%s", closes, closed.State)
	}
	decision, err := models.FindDecision(closed.DecisionHash)
	if err != nil || decision == nil || decision.Outcome != "closed" {
		t.Fatalf("outcome %+v %v", decision, err)
	}
	closed.VaultTx = "dry-vault"
	if err := models.SaveBill(closed); err != nil {
		t.Fatal(err)
	}
	if _, err := ReopenBill(closed.Code, ""); err == nil {
		t.Fatal("reopened a bill that already reached the vault")
	}
}

func newOpenBill(t *testing.T, code string) *models.Bill {
	t.Helper()
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	bill := models.NewBill()
	bill.Code = code
	bill.Domain = "pulse.xyz"
	bill.Vendor = "porkbun"
	bill.Kind = "domain_register"
	bill.CategoryCode = "domains"
	bill.State = "quoted"
	saved, err := models.InsertBill(bill)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func escalateOffChain(t *testing.T, code string) *models.Bill {
	t.Helper()
	bill := newOpenBill(t, code)
	deps := generousDeps(t)
	deps.facts = func(_ *models.Bill, _ int64, amount *big.Int) procurement.Facts {
		return procurement.Facts{
			CategoryEnabled: true, PayeeAllowed: true, DailyCap: 10, MonthlyLimit: 100000,
			Remaining: big.NewInt(50_000_000), PerTxCap: big.NewInt(20_000_000),
			Balance: big.NewInt(20_000_000), MaxBill: big.NewInt(1), Amount: amount,
		}
	}
	deps.vault = func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
		t.Fatal("off-chain escalation called the vault")
		return vaultResult{}, nil
	}
	if err := AdvanceBill(context.Background(), bill, deps); err != nil {
		t.Fatal(err)
	}
	if bill.State != "escalated" || bill.VaultTx != "" || bill.ReasonCode != "max_bill" {
		t.Fatalf("state=%s code=%s vault=%s", bill.State, bill.ReasonCode, bill.VaultTx)
	}
	return bill
}

func TestApproveBillRequiresExplicitConfirm(t *testing.T) {
	_, err := ApproveBill(context.Background(), treasury.Config{Mode: "mainnet"}, "bill-x", BillFlags{})
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatal(err)
	}
	_, err = ApproveBill(context.Background(), treasury.Config{Mode: "testnet"}, "bill-x", BillFlags{UnderstandRealMoney: true})
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatal(err)
	}
	_, err = ApproveBill(context.Background(), treasury.Config{Mode: "dry-run"}, "bill-x", BillFlags{OverrideCap: true})
	if err == nil || !strings.Contains(err.Error(), "--override-reason") {
		t.Fatal(err)
	}
}

func TestOwnerApproveBlocksPorkbunLimitsAndCaps(t *testing.T) {
	monthly := escalateFor(t, "bill-month-"+t.Name(), func(deps *billDeps) {
		deps.quote = func(context.Context, *models.Bill) (int64, *big.Int, error) {
			return 1108, big.NewInt(54050), nil
		}
		deps.facts = func(_ *models.Bill, _ int64, amount *big.Int) procurement.Facts {
			return procurement.Facts{
				CategoryEnabled: true, PayeeAllowed: true, DailyCap: 10, MonthlyLimit: 1000,
				Remaining: big.NewInt(50_000_000), PerTxCap: big.NewInt(20_000_000),
				Balance: big.NewInt(20_000_000), Amount: amount,
			}
		}
	}, "monthly_limit")
	assertOwnerBlocked(t, monthly, BillFlags{OverrideCap: true, OverrideReason: "still no"}, "monthly_limit")

	daily := escalateFor(t, "bill-day-"+t.Name(), func(deps *billDeps) {
		deps.facts = func(_ *models.Bill, _ int64, amount *big.Int) procurement.Facts {
			return procurement.Facts{
				CategoryEnabled: true, PayeeAllowed: true, DailyCap: 1, DailyCount: 1, MonthlyLimit: 100000,
				Remaining: big.NewInt(50_000_000), PerTxCap: big.NewInt(20_000_000),
				Balance: big.NewInt(20_000_000), Amount: amount,
			}
		}
	}, "daily_cap")
	assertOwnerBlocked(t, daily, BillFlags{OverrideCap: true, OverrideReason: "still no"}, "daily_cap")

	capped := escalateOffChain(t, "bill-cap-"+t.Name())
	assertOwnerBlocked(t, capped, BillFlags{}, "max_bill")
	assertOwnerBlocked(t, capped, BillFlags{OverrideCap: true}, "max_bill")

	log, err := treasury.OpenAudit(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	deps := payingDeps(t)
	deps.audit = log
	deps.facts = func(_ *models.Bill, _ int64, amount *big.Int) procurement.Facts {
		return procurement.Facts{
			CategoryEnabled: true, PayeeAllowed: true, DailyCap: 10, MonthlyLimit: 100000,
			Remaining: big.NewInt(50_000_000), PerTxCap: big.NewInt(20_000_000),
			Balance: big.NewInt(20_000_000), MaxBill: big.NewInt(1), Amount: amount,
		}
	}
	var pays int
	deps.vault = func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
		pays++
		return vaultResult{TxHash: "dry-vault-override", Status: "paid"}, nil
	}
	if err := ownerPay(context.Background(), capped, deps, BillFlags{OverrideCap: true, OverrideReason: "owner checked the quote"}); err != nil {
		t.Fatal(err)
	}
	if pays != 1 || capped.State != "done" || !strings.Contains(capped.Rationale, "owner checked the quote") {
		t.Fatalf("pays=%d state=%s rationale=%s", pays, capped.State, capped.Rationale)
	}
	raw, err := os.ReadFile(log.Path())
	if err != nil || !strings.Contains(string(raw), "owner_override") || !strings.Contains(string(raw), "owner checked the quote") {
		t.Fatalf("audit %s err %v", raw, err)
	}

	spent := escalateFor(t, "bill-spend-"+t.Name(), func(deps *billDeps) {
		deps.facts = func(_ *models.Bill, _ int64, amount *big.Int) procurement.Facts {
			return procurement.Facts{
				CategoryEnabled: true, PayeeAllowed: true, DailyCap: 10, MonthlyLimit: 100000,
				Remaining: big.NewInt(50_000_000), PerTxCap: big.NewInt(20_000_000),
				Balance: big.NewInt(20_000_000), MaxSpend: big.NewInt(1), Amount: amount,
			}
		}
	}, "max_spend_per_run")
	assertOwnerBlocked(t, spent, BillFlags{}, "max_spend_per_run")
}

func TestReopenPaidBillSaysAlreadyPaid(t *testing.T) {
	bill := newOpenBill(t, "bill-paid-"+t.Name())
	bill.State = "done"
	bill.VaultTx = "dry-vault"
	if err := models.SaveBill(bill); err != nil {
		t.Fatal(err)
	}
	_, err := ReopenBill(bill.Code, "")
	if err == nil || !strings.Contains(err.Error(), "already paid") {
		t.Fatal(err)
	}
}

func TestLiveObservationDoesNotSubtractSettledSpend(t *testing.T) {
	payee := "0x0000000000000000000000000000000000000001"
	chain := big.NewInt(10_000_000)
	remaining := big.NewInt(30_000_000)
	pending := big.NewInt(0)
	observe := func() procurement.Facts {
		return applyObservation(procurement.Facts{}, "domains", payee, treasury.Snapshot{
			Balance: new(big.Int).Set(chain),
			Categories: map[string]treasury.Category{
				"domains": {Enabled: true, Remaining: new(big.Int).Set(remaining), PerTxCap: big.NewInt(15_000_000), Payees: map[string]bool{payee: true}},
			},
		}, nil, pending)
	}
	if observe().Balance.Cmp(chain) != 0 {
		t.Fatal(observe().Balance)
	}
	pending.Add(pending, big.NewInt(3_000_000))
	if observe().Balance.Cmp(big.NewInt(7_000_000)) != 0 || observe().Remaining.Cmp(big.NewInt(27_000_000)) != 0 {
		t.Fatalf("pending balance %s remaining %s", observe().Balance, observe().Remaining)
	}
	chain.Sub(chain, big.NewInt(3_000_000))
	remaining.Sub(remaining, big.NewInt(3_000_000))
	pending.Sub(pending, big.NewInt(3_000_000))
	settled := observe()
	if settled.Balance.Cmp(big.NewInt(7_000_000)) != 0 || settled.Remaining.Cmp(big.NewInt(27_000_000)) != 0 {
		t.Fatalf("settled balance %s remaining %s", settled.Balance, settled.Remaining)
	}
}

func TestPaidVaultClearsPendingAndApprovalKeepsIt(t *testing.T) {
	bill := newOpenBill(t, "bill-pend-"+t.Name())
	deps := generousDeps(t)
	deps.runSpent = big.NewInt(0)
	deps.pendingSpend = big.NewInt(0)
	var paid *big.Int
	deps.vault = func(_ context.Context, _ *models.Bill, amount *big.Int, _ string) (vaultResult, error) {
		paid = new(big.Int).Set(amount)
		return vaultResult{TxHash: "dry-vault", Status: "paid"}, nil
	}
	if err := AdvanceBill(context.Background(), bill, deps); err != nil {
		t.Fatal(err)
	}
	if deps.pendingSpend.Sign() != 0 || deps.runSpent.Cmp(paid) != 0 {
		t.Fatalf("pending %s run %s paid %s", deps.pendingSpend, deps.runSpent, paid)
	}

	held := newOpenBill(t, "bill-hold-"+t.Name())
	deps = generousDeps(t)
	deps.runSpent = big.NewInt(0)
	deps.pendingSpend = big.NewInt(0)
	deps.vault = func(_ context.Context, _ *models.Bill, _ *big.Int, _ string) (vaultResult, error) {
		return vaultResult{TxHash: "dry-vault", RequestID: "9", Status: "approval"}, nil
	}
	if err := AdvanceBill(context.Background(), held, deps); err != nil {
		t.Fatal(err)
	}
	if held.State != "escalated" || deps.pendingSpend.Sign() <= 0 || deps.pendingSpend.Cmp(deps.runSpent) != 0 {
		t.Fatalf("state %s pending %s run %s", held.State, deps.pendingSpend, deps.runSpent)
	}
}

func TestBlockedApproveLeavesHashedFields(t *testing.T) {
	bill := escalateOffChain(t, "bill-keep-hash-"+t.Name())
	before := *bill
	log, err := treasury.OpenAudit(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	deps := payingDeps(t)
	deps.audit = log
	deps.facts = func(_ *models.Bill, _ int64, amount *big.Int) procurement.Facts {
		return procurement.Facts{
			CategoryEnabled: true, PayeeAllowed: true, DailyCap: 10, MonthlyLimit: 1,
			Remaining: big.NewInt(50_000_000), PerTxCap: big.NewInt(20_000_000),
			Balance: big.NewInt(20_000_000), Amount: amount,
		}
	}
	deps.vault = func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
		t.Fatal("blocked approve reached the vault")
		return vaultResult{}, nil
	}
	if err := ownerPay(context.Background(), bill, deps, BillFlags{}); err == nil || !strings.Contains(err.Error(), "monthly_limit") {
		t.Fatal(err)
	}
	if bill.DecisionHash != before.DecisionHash || bill.ReasonCode != before.ReasonCode || bill.Reason != before.Reason || bill.Rationale != before.Rationale || bill.Action != before.Action || bill.Planner != before.Planner {
		t.Fatalf("hashed fields changed: code %s->%s rationale %q->%q hash %s->%s", before.ReasonCode, bill.ReasonCode, before.Rationale, bill.Rationale, before.DecisionHash, bill.DecisionHash)
	}
	again, err := treasury.DecisionHash(treasury.Canonical{
		V: 2, AgentID: deps.policy.AgentID, ChainID: deps.policy.ChainID, Vault: deps.policy.Vault,
		PayableID: bill.Code, Action: bill.Action, Category: bill.CategoryCode, Payee: deps.payee,
		AmountUnits: bill.AmountUnits, ReasonCode: bill.ReasonCode, Planner: bill.Planner,
		ModelID: bill.ModelID, PlannerAction: bill.PlannerAction, Rationale: bill.Rationale,
		PromptHash: bill.PromptHash, RiskNotes: bill.RiskNotes, Confidence: bill.Confidence,
	})
	if err != nil || again != bill.DecisionHash {
		t.Fatalf("stored %s rebuilt %s err %v", bill.DecisionHash, again, err)
	}
	raw, err := os.ReadFile(log.Path())
	if err != nil || !strings.Contains(string(raw), "approve_blocked") || !strings.Contains(string(raw), "monthly_limit") {
		t.Fatalf("audit %s err %v", raw, err)
	}
}

func escalateFor(t *testing.T, code string, tune func(*billDeps), reason string) *models.Bill {
	t.Helper()
	bill := newOpenBill(t, code)
	deps := generousDeps(t)
	tune(&deps)
	deps.vault = func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
		t.Fatal("escalation called the vault")
		return vaultResult{}, nil
	}
	if err := AdvanceBill(context.Background(), bill, deps); err != nil {
		t.Fatal(err)
	}
	if bill.State != "escalated" || bill.VaultTx != "" || bill.ReasonCode != reason {
		t.Fatalf("state=%s code=%s vault=%s", bill.State, bill.ReasonCode, bill.VaultTx)
	}
	return bill
}

func assertOwnerBlocked(t *testing.T, bill *models.Bill, flags BillFlags, reason string) {
	t.Helper()
	deps := payingDeps(t)
	deps.facts = func(_ *models.Bill, cents int64, amount *big.Int) procurement.Facts {
		facts := procurement.Facts{
			CategoryEnabled: true, PayeeAllowed: true, DailyCap: 10, MonthlyLimit: 100000,
			Remaining: big.NewInt(50_000_000), PerTxCap: big.NewInt(20_000_000),
			Balance: big.NewInt(20_000_000), Amount: amount, QuoteCents: cents,
		}
		switch reason {
		case "monthly_limit":
			facts.MonthlyLimit = 1
		case "daily_cap":
			facts.DailyCap = 1
			facts.DailyCount = 1
		case "max_bill":
			facts.MaxBill = big.NewInt(1)
		case "max_spend_per_run":
			facts.MaxSpend = big.NewInt(1)
		}
		return facts
	}
	deps.vault = func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
		t.Fatalf("%s reached the vault", reason)
		return vaultResult{}, nil
	}
	err := ownerPay(context.Background(), bill, deps, flags)
	if err == nil || !strings.Contains(err.Error(), reason) || bill.VaultTx != "" {
		t.Fatalf("reason %s err=%v vault=%s", reason, err, bill.VaultTx)
	}
}

func payingDeps(t *testing.T) billDeps {
	t.Helper()
	deps := generousDeps(t)
	deps.facts = func(_ *models.Bill, _ int64, amount *big.Int) procurement.Facts {
		return procurement.Facts{
			CategoryEnabled: true, PayeeAllowed: true, DailyCap: 10, MonthlyLimit: 100000,
			Remaining: big.NewInt(50_000_000), PerTxCap: big.NewInt(20_000_000),
			Balance: big.NewInt(20_000_000), Amount: amount,
		}
	}
	return deps
}
