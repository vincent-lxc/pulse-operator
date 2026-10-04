package business

import (
	"context"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	servertypes "github.com/digitalwayhk/core/pkg/server/types"
	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

func TestSettleApprovalMarksPayablePaidOrClosed(t *testing.T) {
	root := moduleRoot(t)
	cfg, err := treasury.LoadConfig(filepath.Join(root, "config", "dry-run.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg.AuditLog = filepath.Join(dir, "audit.jsonl")
	cfg.VaultFixture = filepath.Join(root, "testdata", "vault.yaml")
	cfg.Ledger = filepath.Join(root, "testdata", "ledger.yaml")
	state := `{
	  "balance": "20.00",
	  "pending": [
	    {"requestID":"7","category":"people","payee":"0x2222222222222222222222222222222222222222","amount":"0.70","decisionHash":"0x1111111111111111111111111111111111111111111111111111111111111111","status":"pending"},
	    {"requestID":"8","category":"people","payee":"0x3333333333333333333333333333333333333333","amount":"1.23","decisionHash":"0x2222222222222222222222222222222222222222222222222222222222222222","status":"pending"}
	  ]
	}`
	if err := os.WriteFile(filepath.Join(dir, "mock-state.json"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	if err := models.UpsertPayable("settle-bonus", "people", "0x2222222222222222222222222222222222222222", "0.700000", "2026-10-03T00:00:00Z", ""); err != nil {
		t.Fatal(err)
	}
	if err := models.UpdatePayableState("settle-bonus", "escalated"); err != nil {
		t.Fatal(err)
	}
	decision := models.NewDecisionRecord()
	decision.Code = "0x1111111111111111111111111111111111111111111111111111111111111111"
	decision.PayableCode = "settle-bonus"
	decision.Action = treasury.ActionEscalate
	if err := models.InsertDecision(decision); err != nil {
		t.Fatal(err)
	}
	if err := models.SaveApproval(decision.Code, "7", "people", "0x2222222222222222222222222222222222222222", "0.700000", decision.Code, "pending", "over_tx_cap", treasury.ProductWallets, "pay-tx", "COMPLETE"); err != nil {
		t.Fatal(err)
	}
	if err := models.UpsertPayable("settle-closed", "people", "0x3333333333333333333333333333333333333333", "1.230000", "2026-10-03T00:00:00Z", ""); err != nil {
		t.Fatal(err)
	}
	if err := models.UpdatePayableState("settle-closed", "escalated"); err != nil {
		t.Fatal(err)
	}
	if err := models.SaveApproval("chain:8", "8", "people", "0x3333333333333333333333333333333333333333", "1.230000", "", "pending", "over_budget", "", "", ""); err != nil {
		t.Fatal(err)
	}

	approved, err := SettleApproval(context.Background(), cfg, "7", "approve")
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != "simulated_approved" || approved.TxHash == "" {
		t.Fatalf("approve %+v", approved)
	}
	paid, err := models.FindPayable("settle-bonus")
	if err != nil {
		t.Fatal(err)
	}
	if paid == nil || paid.State != "paid" || paid.TxHash != approved.TxHash {
		t.Fatalf("payable after approve %+v", paid)
	}
	kept, err := models.FindApprovalByRequest("7")
	if err != nil || kept == nil || kept.CircleTxID != "pay-tx" || kept.CircleState != "COMPLETE" {
		t.Fatalf("approval circle tx %+v %v", kept, err)
	}

	if _, err := SettleApproval(context.Background(), cfg, "8", "reject"); err != nil {
		t.Fatal(err)
	}
	closed, err := models.FindPayable("settle-closed")
	if err != nil {
		t.Fatal(err)
	}
	if closed == nil || closed.State != "closed" {
		t.Fatalf("payable after reject %+v", closed)
	}
	raw, err := os.ReadFile(cfg.AuditLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"circle_tx_id"`) || !strings.Contains(string(raw), `"circle_state"`) {
		t.Fatalf("audit missing circle tx fields: %s", raw)
	}
}

func TestSyncChainApprovalsInsertsExternalPending(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	hash := "0x" + strings.Repeat("ab", 32)
	item := treasury.Approval{
		RequestID: "2", Category: "people", Payee: "0x2222222222222222222222222222222222222222",
		Amount: big.NewInt(700000), DecisionHash: hash, Reason: treasury.ReasonOverTxCap, Status: "pending",
	}
	if err := SyncChainApprovals([]treasury.Approval{item}); err != nil {
		t.Fatal(err)
	}
	if err := SyncChainApprovals([]treasury.Approval{item}); err != nil {
		t.Fatal(err)
	}
	rows, err := models.ListApprovals()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	var row *models.Approval
	for _, candidate := range rows {
		if candidate.RequestID == "2" {
			n++
			row = candidate
		}
	}
	if n != 1 || row == nil || row.Code != hash || row.State != "pending" || row.CircleProduct != "" {
		t.Fatalf("synced rows %d %+v", n, row)
	}
	if err := models.SaveApproval(row.Code, "2", row.CategoryCode, row.Payee, row.AmountUnits, row.DecisionHash, "simulated_approved", row.ReasonCode, treasury.ProductWallets, "circle-tx", "COMPLETE"); err != nil {
		t.Fatal(err)
	}
	if err := SyncChainApprovals([]treasury.Approval{item}); err != nil {
		t.Fatal(err)
	}
	again, err := models.FindApprovalByRequest("2")
	if err != nil {
		t.Fatal(err)
	}
	if again.State != "simulated_approved" || again.CircleProduct != treasury.ProductWallets || again.CircleTxID != "circle-tx" {
		t.Fatalf("sync flipped a settled approval: %+v", again)
	}
}

func TestDashboardCircleFollowsCurrentExecutor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raw-key.yaml")
	body := []byte("mode: dry-run\nchainID: \"5042002\"\nchainDriver: rpc\nexecutor: raw-key\nvault: \"0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01\"\nagent: \"0xb71c11F7b28D9A912e7C3ACfbe423f7DeA568DEa\"\nreserveFloorUSDC: \"8.00\"\nreserveTargetUSDC: \"10.00\"\n")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	cycle := models.NewCycleSnapshot()
	cycle.Code = "stale-circle-run"
	cycle.BalanceUnits = "1.000000"
	cycle.ObligationUnits = "0.700000"
	cycle.CircleProduct = treasury.ProductWallets
	cycle.ObservedAt = "2026-10-03T12:00:00Z"
	if err := models.InsertCycle(cycle); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPERATOR_CONFIG", path)
	t.Setenv("ARC_RPC_URL", "")
	if cfg, err := loadRuntimeConfig(); err != nil {
		t.Fatal(err)
	} else if cfg.ExecutorProduct() != treasury.ProductLocalKey {
		t.Fatalf("config executor %q product %s", cfg.Executor, cfg.ExecutorProduct())
	}
	view, err := LoadDashboard()
	if err != nil {
		t.Fatal(err)
	}
	if view.CircleProduct != treasury.ProductLocalKey {
		t.Fatalf("circle %s", view.CircleProduct)
	}
}

func TestClassifyRevertIsBusinessError(t *testing.T) {
	err := classifyChainError(&treasury.RevertError{Reason: "RequestNotPending(4)"})
	contract := servertypes.ResolvePublicError(err)
	if contract.HTTPStatus != 422 || contract.Message != "RequestNotPending(4)" {
		t.Fatalf("contract %+v err %v", contract, err)
	}
	if treasury.RevertReason(err) != "" {
		t.Fatal("wrapped error must not still look like a revert to the audit helper")
	}
}
