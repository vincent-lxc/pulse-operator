package business

import (
	"context"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/procurement"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

func TestBillGatewayPromptIncludesDomainQuoteAndBalance(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "openai/gpt-5.4-nano",
			"choices": []any{map[string]any{"message": map[string]any{
				"content": `{"action":"pay","rationale":"quote fits the vault balance","risk_notes":"none","confidence":"0.8"}`,
			}}},
		})
	}))
	defer srv.Close()
	t.Setenv("AI_GATEWAY_API_KEY", "test-key")

	bill := models.NewBill()
	bill.Code = "bill-prompt-" + t.Name()
	bill.Domain = "pulseoperator.dev"
	bill.Vendor = "porkbun"
	bill.Kind = "domain_register"
	bill.CategoryCode = "domains"
	bill.State = "quoted"
	if _, err := models.InsertBill(bill); err != nil {
		t.Fatal(err)
	}
	deps := generousDeps(t)
	deps.cfg.Planner = treasury.PlannerConfig{
		Driver: "gateway", Model: "openai/gpt-5.4-nano", BaseURL: srv.URL, APIKeyEnv: "AI_GATEWAY_API_KEY",
	}
	deps.plan = planFunc(deps.cfg)
	deps.vault = func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
		return vaultResult{TxHash: "dry-vault", Status: "paid"}, nil
	}
	deps.burn = func(context.Context, *models.Bill, *big.Int, *big.Int) (burnResult, error) {
		return burnResult{BurnTx: "dry-burn", MintTx: "dry-mint"}, nil
	}
	deps.merchant = func(context.Context, *models.Bill) (merchantResult, error) {
		return merchantResult{OrderID: "dry-order"}, nil
	}
	deps.facts = func(_ *models.Bill, _ int64, amount *big.Int) procurement.Facts {
		return procurement.Facts{
			CategoryEnabled: true, PayeeAllowed: true, DailyCap: 10, MonthlyLimit: 10000,
			Remaining: big.NewInt(30_000_000), PerTxCap: big.NewInt(15_000_000),
			Balance: big.NewInt(20_000_000), Amount: amount, QuoteCents: 875,
		}
	}
	if err := AdvanceBill(context.Background(), bill, deps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "pulseoperator.dev") || !strings.Contains(body, "875") {
		t.Fatalf("prompt missing domain or quote: %s", body)
	}
	if !strings.Contains(body, `balance\":\"20.000000`) && !strings.Contains(body, `"balance":"20.000000"`) {
		t.Fatalf("prompt balance: %s", body)
	}
	if bill.State == "closed" {
		t.Fatalf("compliant bill closed: %s", bill.Reason)
	}
}

func TestModelRejectOfCompliantBillEscalates(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	bill := models.NewBill()
	bill.Code = "bill-reject-" + t.Name()
	bill.Domain = "pulseoperator.dev"
	bill.Vendor = "porkbun"
	bill.Kind = "domain_register"
	bill.CategoryCode = "domains"
	bill.State = "quoted"
	if _, err := models.InsertBill(bill); err != nil {
		t.Fatal(err)
	}
	var pays int
	deps := generousDeps(t)
	deps.plan = func(context.Context, treasury.PlanRequest) (treasury.PlanOutput, error) {
		return treasury.PlanOutput{
			Action: treasury.ActionReject, Rationale: "vault looks empty", Source: "gateway",
			ModelID: "openai/gpt-5.4-nano", Confidence: "0.4", RiskNotes: "none",
		}, nil
	}
	deps.vault = func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
		pays++
		return vaultResult{}, nil
	}
	if err := AdvanceBill(context.Background(), bill, deps); err != nil {
		t.Fatal(err)
	}
	if bill.State == "closed" || bill.Action != treasury.ActionEscalate || pays != 0 {
		t.Fatalf("state=%s action=%s pays=%d", bill.State, bill.Action, pays)
	}
	if !strings.Contains(bill.RiskNotes, "llm_chose:reject") || !strings.Contains(bill.RiskNotes, "llm_disagreed") {
		t.Fatalf("notes %s", bill.RiskNotes)
	}
	if bill.Planner != "gateway" {
		t.Fatalf("planner %s", bill.Planner)
	}
	again, err := treasury.DecisionHash(treasury.Canonical{
		V: 2, AgentID: deps.policy.AgentID, ChainID: deps.policy.ChainID, Vault: deps.policy.Vault,
		PayableID: bill.Code, Action: bill.Action, Category: bill.CategoryCode, Payee: deps.payee,
		AmountUnits: bill.AmountUnits, ReasonCode: bill.ReasonCode, Planner: bill.Planner,
		ModelID: bill.ModelID, PlannerAction: bill.PlannerAction, Rationale: bill.Rationale,
		PromptHash: bill.PromptHash, RiskNotes: bill.RiskNotes, Confidence: bill.Confidence,
		Disagree: strings.Contains(bill.RiskNotes, "llm_disagreed"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if again != bill.DecisionHash {
		t.Fatalf("stored notes do not recompute\nstored %s\nrebuilt %s\nnotes %s", bill.DecisionHash, again, bill.RiskNotes)
	}
	row, err := models.FindDecision(bill.DecisionHash)
	if err != nil || row == nil || row.Rationale != bill.Rationale {
		t.Fatalf("decision view %+v %v", row, err)
	}
}

func TestBillAuditRecordsDisagreeAndLatency(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log, err := treasury.OpenAudit(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	bill := models.NewBill()
	bill.Code = "bill-audit-" + t.Name()
	bill.Domain = "pulse.xyz"
	bill.Vendor = "porkbun"
	bill.Kind = "domain_register"
	bill.CategoryCode = "domains"
	bill.State = "quoted"
	if _, err := models.InsertBill(bill); err != nil {
		t.Fatal(err)
	}
	deps := generousDeps(t)
	deps.audit = log
	deps.plan = func(context.Context, treasury.PlanRequest) (treasury.PlanOutput, error) {
		return treasury.PlanOutput{
			Action: treasury.ActionDefer, Rationale: "wait", Source: "gateway",
			ModelID: "openai/gpt-5.4-nano", Confidence: "0.5", LatencyMS: 1234,
		}, nil
	}
	if err := AdvanceBill(context.Background(), bill, deps); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, `"kind":"planner_disagree"`) || !strings.Contains(text, `"latency_ms":1234`) {
		t.Fatalf("audit %s", text)
	}
	if !strings.Contains(bill.Evidence, `"vault_chain": "5042002"`) || !strings.Contains(bill.Evidence, `"latency_ms": 1234`) {
		t.Fatalf("evidence %s", bill.Evidence)
	}
	if !strings.Contains(EvidenceMarkdown(bill), "latency_ms: 1234") {
		t.Fatal(EvidenceMarkdown(bill))
	}
}

func TestRunSpendAccumulatesAcrossBills(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	mk := func(code string) *models.Bill {
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
		return bill
	}
	first := mk("bill-run-a-" + t.Name())
	second := mk("bill-run-b-" + t.Name())
	deps := generousDeps(t)
	deps.runSpent = big.NewInt(0)
	cap := big.NewInt(10_000_000)
	deps.facts = func(_ *models.Bill, _ int64, amount *big.Int) procurement.Facts {
		return procurement.Facts{
			CategoryEnabled: true, PayeeAllowed: true, DailyCap: 10, MonthlyLimit: 100000,
			Remaining: big.NewInt(50_000_000), PerTxCap: big.NewInt(20_000_000), Amount: amount,
			MaxSpend: cap, RunSpent: new(big.Int).Set(deps.runSpent),
		}
	}
	if err := AdvanceBill(context.Background(), first, deps); err != nil {
		t.Fatal(err)
	}
	if first.Action != treasury.ActionPay {
		t.Fatalf("first %s %s", first.Action, first.ReasonCode)
	}
	if err := AdvanceBill(context.Background(), second, deps); err != nil {
		t.Fatal(err)
	}
	if second.ReasonCode != "max_spend_per_run" || second.State != "escalated" {
		t.Fatalf("second %s %s", second.ReasonCode, second.State)
	}
}

func TestDryFactsReadDomainsCategory(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	cfg := treasury.Config{
		VaultFixture: filepath.Join(filepath.Dir(file), "..", "testdata", "vault.yaml"),
		ChainID:      "5042002",
		Porkbun:      treasury.PorkbunConfig{MonthlyLimitCents: 10000, DailyCap: 10},
	}
	facts := dryFacts(&models.Bill{Code: "none", CategoryCode: "domains"}, 875, big.NewInt(1), cfg, nil)
	if !facts.CategoryEnabled || facts.PerTxCap.Cmp(big.NewInt(15_000_000)) != 0 {
		t.Fatalf("cap %s", facts.PerTxCap)
	}
	if facts.Balance.Cmp(big.NewInt(20_000_000)) != 0 || facts.Remaining.Cmp(big.NewInt(30_000_000)) != 0 {
		t.Fatalf("balance %s remaining %s", facts.Balance, facts.Remaining)
	}
	spent := dryFacts(&models.Bill{Code: "none", CategoryCode: "domains"}, 875, big.NewInt(1), cfg, big.NewInt(8_000_000))
	if spent.Balance.Cmp(big.NewInt(12_000_000)) != 0 || spent.Remaining.Cmp(big.NewInt(22_000_000)) != 0 {
		t.Fatalf("after spend balance %s remaining %s", spent.Balance, spent.Remaining)
	}
}

func TestBillCodeSeparatesRegisterAndRenew(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	reg, err := AddBill("porkbun", "pulseoperator.dev", "domain_register", 1)
	if err != nil {
		t.Fatal(err)
	}
	renew, err := AddBill("porkbun", "pulseoperator.dev", "domain_renew", 1)
	if err != nil {
		t.Fatal(err)
	}
	if reg.Code == renew.Code || reg.Code != "bill-register-pulseoperator-dev" || renew.Code != "bill-renew-pulseoperator-dev" {
		t.Fatalf("reg %s renew %s", reg.Code, renew.Code)
	}
	if reg.Years != 1 || renew.Years != 1 {
		t.Fatalf("years %d %d", reg.Years, renew.Years)
	}
	if _, err := AddBill("porkbun", "pulseoperator.dev", "domain_register", 2); err == nil {
		t.Fatal("years 2 was accepted")
	}
}

func TestPorkbunGateAcceptsSecretFile(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key")
	secretFile := filepath.Join(dir, "secret")
	if err := os.WriteFile(keyFile, []byte("pk1_test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretFile, []byte("sk1_test"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := treasury.Config{Porkbun: treasury.PorkbunConfig{APIKeyFile: keyFile, SecretFile: secretFile}}
	ok, err := porkbunReady(cfg)
	if err != nil || !ok {
		t.Fatalf("ready %v %v", ok, err)
	}
	if err := os.Chmod(secretFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := porkbunReady(cfg); err == nil {
		t.Fatal("wide file was accepted")
	}
}
