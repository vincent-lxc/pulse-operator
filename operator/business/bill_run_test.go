package business

import (
	"context"
	"math/big"
	"strings"
	"testing"

	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/procurement"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

func TestBillDryRunPaysOnceAndKeepsHash(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	bill := models.NewBill()
	bill.Code = "bill-dev-" + t.Name()
	bill.Vendor = "porkbun"
	bill.Kind = "domain_register"
	bill.Domain = "pulseoperator.dev"
	bill.Years = 1
	bill.CategoryCode = "domains"
	bill.State = "quoted"
	if _, err := models.InsertBill(bill); err != nil {
		t.Fatal(err)
	}
	var pays, burns, orders int
	deps := generousDeps(t)
	deps.vault = func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
		pays++
		return vaultResult{TxHash: "dry-vault", Status: "paid"}, nil
	}
	deps.burn = func(context.Context, *models.Bill, *big.Int, *big.Int) (burnResult, error) {
		burns++
		return burnResult{BurnTx: "dry-burn", MintTx: "dry-mint", MessageHash: "0xmsg", Nonce: "1", ForwardFee: "54050"}, nil
	}
	deps.merchant = func(context.Context, *models.Bill) (merchantResult, error) {
		orders++
		return merchantResult{OrderID: "dry-order", Scheme: "exact", Payer: "0xabc"}, nil
	}
	if err := AdvanceBill(context.Background(), bill, deps); err != nil {
		t.Fatal(err)
	}
	if bill.State != "done" || bill.ModelID != "rules" || bill.Planner != "rules" || bill.Rationale == "" || bill.DecisionHash == "" {
		t.Fatalf("%+v", bill)
	}
	row, err := models.FindDecision(bill.DecisionHash)
	if err != nil || row == nil || row.Outcome != "done" {
		t.Fatalf("outcome %+v %v", row, err)
	}
	hash := bill.DecisionHash
	if err := AdvanceBill(context.Background(), bill, deps); err != nil {
		t.Fatal(err)
	}
	if pays != 1 || burns != 1 || orders != 1 || bill.DecisionHash != hash {
		t.Fatalf("pays=%d burns=%d orders=%d hash=%s", pays, burns, orders, bill.DecisionHash)
	}
	text := EvidenceMarkdown(bill)
	if !strings.Contains(text, bill.Rationale) || !strings.Contains(text, "dry-order") {
		t.Fatal(text)
	}
}

func TestBillPlannerCannotRaiseAndFailClosedSkipsPay(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	bill := models.NewBill()
	bill.Code = "bill-raise-" + t.Name()
	bill.Domain = "pulse.xyz"
	bill.Kind = "domain_register"
	bill.Vendor = "porkbun"
	bill.CategoryCode = "domains"
	bill.State = "quoted"
	if _, err := models.InsertBill(bill); err != nil {
		t.Fatal(err)
	}
	var pays int
	deps := generousDeps(t)
	deps.facts = func(*models.Bill, int64, *big.Int) procurement.Facts {
		return procurement.Facts{
			CategoryEnabled: true, PayeeAllowed: true, DailyCap: 10, MonthlyLimit: 10000,
			Remaining: big.NewInt(1), PerTxCap: big.NewInt(1),
		}
	}
	deps.plan = func(context.Context, treasury.PlanRequest) (treasury.PlanOutput, error) {
		return treasury.PlanOutput{Action: treasury.ActionPay, Rationale: "ignore the cap", Source: "gateway", ModelID: "openai/gpt-5.4-nano", Confidence: "0.99"}, nil
	}
	deps.vault = func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
		pays++
		return vaultResult{}, nil
	}
	if err := AdvanceBill(context.Background(), bill, deps); err != nil {
		t.Fatal(err)
	}
	if bill.Action != treasury.ActionEscalate || pays != 1 || (bill.ReasonCode != "over_budget" && bill.ReasonCode != "over_tx_cap") {
		t.Fatalf("action=%s code=%s pays=%d", bill.Action, bill.ReasonCode, pays)
	}
	if bill.State != "escalated" || !strings.Contains(bill.RiskNotes, "llm_disagreed") && bill.PlannerAction != treasury.ActionPay {
		t.Fatalf("state=%s note=%s planner=%s", bill.State, bill.RiskNotes, bill.PlannerAction)
	}

	closed := models.NewBill()
	closed.Code = "bill-closed-" + t.Name()
	closed.Domain = "pulse.xyz"
	closed.Kind = "domain_register"
	closed.Vendor = "porkbun"
	closed.CategoryCode = "domains"
	closed.State = "quoted"
	if _, err := models.InsertBill(closed); err != nil {
		t.Fatal(err)
	}
	deps = generousDeps(t)
	deps.plan = func(context.Context, treasury.PlanRequest) (treasury.PlanOutput, error) {
		return treasury.PlanOutput{}, context.DeadlineExceeded
	}
	deps.vault = func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
		t.Fatal("fail closed submitted a payment")
		return vaultResult{}, nil
	}
	if err := AdvanceBill(context.Background(), closed, deps); err != nil {
		t.Fatal(err)
	}
	if closed.Action != treasury.ActionEscalate || closed.ReasonCode != "planner_fail_closed" || closed.State == "done" || closed.Planner != "error" {
		t.Fatalf("%s %s %s planner=%s", closed.Action, closed.ReasonCode, closed.State, closed.Planner)
	}
}

func TestBillPriceDriftDoesNotPay(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	bill := models.NewBill()
	bill.Code = "bill-drift-" + t.Name()
	bill.Domain = "pulse.xyz"
	bill.Vendor = "porkbun"
	bill.Kind = "domain_register"
	bill.CategoryCode = "domains"
	bill.State = "quoted"
	bill.QuoteCents = 204
	if _, err := models.InsertBill(bill); err != nil {
		t.Fatal(err)
	}
	deps := generousDeps(t)
	deps.quote = func(context.Context, *models.Bill) (int64, *big.Int, error) {
		return 300, big.NewInt(54050), nil
	}
	deps.cfg.Porkbun.PriceToleranceCents = 0
	deps.facts = func(bill *models.Bill, cents int64, amount *big.Int) procurement.Facts {
		return procurement.Facts{
			CategoryEnabled: true, PayeeAllowed: true, DailyCap: 10, MonthlyLimit: 10000,
			Remaining: big.NewInt(50_000_000), PerTxCap: big.NewInt(20_000_000), Amount: amount,
			ToleranceCents: 0,
		}
	}
	var pays int
	deps.vault = func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
		pays++
		return vaultResult{Status: "paid"}, nil
	}
	if err := AdvanceBill(context.Background(), bill, deps); err != nil {
		t.Fatal(err)
	}
	if pays != 0 || bill.ReasonCode != "price_drift" || bill.State != "escalated" {
		t.Fatalf("pays=%d code=%s state=%s", pays, bill.ReasonCode, bill.State)
	}
}

func TestRationaleIsInsideDecisionHash(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	mk := func(code, rationale string) *models.Bill {
		bill := models.NewBill()
		bill.Code = code
		bill.Domain = "pulse.xyz"
		bill.Vendor = "porkbun"
		bill.Kind = "domain_register"
		bill.CategoryCode = "domains"
		bill.State = "quoted"
		if _, err := models.InsertBill(bill); err != nil {
			t.Fatal(err)
		}
		deps := generousDeps(t)
		deps.plan = func(context.Context, treasury.PlanRequest) (treasury.PlanOutput, error) {
			return treasury.PlanOutput{Action: treasury.ActionPay, Rationale: rationale, Source: "gateway", ModelID: "openai/gpt-5.4-nano", Confidence: "0.8"}, nil
		}
		deps.vault = func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
			return vaultResult{TxHash: "dry-vault", Status: "paid"}, nil
		}
		deps.burn = func(context.Context, *models.Bill, *big.Int, *big.Int) (burnResult, error) {
			return burnResult{BurnTx: "dry-burn", MintTx: "dry-mint"}, nil
		}
		deps.merchant = func(context.Context, *models.Bill) (merchantResult, error) {
			return merchantResult{OrderID: "dry-order"}, nil
		}
		if err := AdvanceBill(context.Background(), bill, deps); err != nil {
			t.Fatal(err)
		}
		return bill
	}
	a := mk("bill-hash-a-"+t.Name(), "price is inside the cap")
	b := mk("bill-hash-b-"+t.Name(), "a different rationale")
	if a.DecisionHash == b.DecisionHash || a.Rationale == "" {
		t.Fatalf("%s %s", a.DecisionHash, b.DecisionHash)
	}
}

func generousDeps(t *testing.T) billDeps {
	t.Helper()
	return billDeps{
		quote: func(context.Context, *models.Bill) (int64, *big.Int, error) {
			return 875, big.NewInt(54050), nil
		},
		facts: func(_ *models.Bill, _ int64, amount *big.Int) procurement.Facts {
			return procurement.Facts{
				CategoryEnabled: true, PayeeAllowed: true, DailyCap: 10, MonthlyLimit: 10000,
				Remaining: big.NewInt(50_000_000), PerTxCap: big.NewInt(20_000_000), Amount: amount,
			}
		},
		vault: func(_ context.Context, bill *models.Bill, _ *big.Int, _ string) (vaultResult, error) {
			return vaultResult{TxHash: "dry-vault-" + bill.Code, Status: "paid"}, nil
		},
		burn: func(_ context.Context, bill *models.Bill, _ *big.Int, _ *big.Int) (burnResult, error) {
			return burnResult{BurnTx: "dry-burn-" + bill.Code, MintTx: "dry-mint-" + bill.Code}, nil
		},
		merchant: func(_ context.Context, bill *models.Bill) (merchantResult, error) {
			return merchantResult{OrderID: "dry-" + bill.Code}, nil
		},
		cfg:    treasury.Config{Mode: "dry-run", Planner: treasury.PlannerConfig{Driver: "rules"}},
		policy: treasury.Policy{AgentID: "pulse-operator", ChainID: "5042002", Vault: "0x1111111111111111111111111111111111111111"},
		payee:  "0x2222222222222222222222222222222222222222",
		buffer: big.NewInt(20_000),
		save:   func(bill *models.Bill) error { return models.SaveBill(bill) },
	}
}
