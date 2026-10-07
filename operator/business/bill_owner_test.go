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
	if err := ownerPay(context.Background(), escalated, deps); err != nil {
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
	if err := ownerPay(context.Background(), blocked, deps); err == nil || !strings.Contains(err.Error(), "observe_failed") || blocked.VaultTx != "" {
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
