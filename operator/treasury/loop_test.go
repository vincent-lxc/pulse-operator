package treasury

import (
	"context"
	"math/big"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestDryRunSampleProducesPayDeferSweepEscalation(t *testing.T) {
	root := moduleRoot(t)
	cfg, err := LoadConfig(filepath.Join(root, "config", "dry-run.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := cfg.PolicyFrom(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	payables, err := LoadLedger(filepath.Join(root, cfg.Ledger))
	if err != nil {
		t.Fatal(err)
	}
	snap, err := LoadFixture(filepath.Join(root, cfg.VaultFixture))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	audit, err := OpenAudit(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := Run(context.Background(), LoopInput{
		Policy:   policy,
		Payables: payables,
		Chain:    NewMockChain(snap),
		Audit:    audit,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Decision{}
	for _, d := range report.Decisions {
		got[d.PayableID] = d
	}
	if got["cloud-oct"].Action != ActionPay || got["cloud-oct"].Outcome != "simulated_paid" {
		t.Fatalf("cloud-oct: %+v", got["cloud-oct"])
	}
	if got["cloud-extra"].Action != ActionDefer || got["cloud-extra"].ReasonCode != ReasonCooldown {
		t.Fatalf("cloud-extra: %+v", got["cloud-extra"])
	}
	if got["contractor-bonus"].Action != ActionEscalate || got["contractor-bonus"].ReasonCode != ReasonOverTxCap || got["contractor-bonus"].Outcome != "simulated_approval" {
		t.Fatalf("contractor: %+v", got["contractor-bonus"])
	}
	if got["unknown-vendor"].Action != ActionEscalate || got["unknown-vendor"].ReasonCode != ReasonPayeeNotAllowed || got["unknown-vendor"].TxHash != "" {
		t.Fatalf("unknown: %+v", got["unknown-vendor"])
	}
	if got["conference"].Action != ActionDefer || got["conference"].ReasonCode != ReasonNotDue {
		t.Fatalf("conference: %+v", got["conference"])
	}
	sweep := got["cycle"]
	if sweep.Action != ActionSweep || sweep.Outcome != "simulated_swept" || FormatUSDC(sweep.Amount) != "4.400000" {
		t.Fatalf("sweep: %+v amount %s", sweep, FormatUSDC(sweep.Amount))
	}
	if len(report.Inflows) != 2 || report.Inflows[0].Product != ProductCCTP || report.Inflows[1].Product != ProductGateway {
		t.Fatalf("inflows: %+v", report.Inflows)
	}
	sum := new(big.Int).Add(report.Inflows[0].Amount, report.Inflows[1].Amount)
	if FormatUSDC(sum) != "5.000000" {
		t.Fatalf("inflow sum %s", FormatUSDC(sum))
	}
	if FormatUSDC(report.OpeningBalance) != "20.000000" || FormatUSDC(report.OpeningLiquidity.Surplus) != "4.400000" {
		t.Fatalf("opening balance %s surplus %s", FormatUSDC(report.OpeningBalance), FormatUSDC(report.OpeningLiquidity.Surplus))
	}
	if FormatUSDC(report.Balance) != "13.600000" {
		t.Fatalf("closing balance %s", FormatUSDC(report.Balance))
	}
}

func TestDryRunPaidUpdatesWorkingBalance(t *testing.T) {
	payee := NormalizeAddress("0x2222222222222222222222222222222222222222")
	snap := Snapshot{
		Balance: big.NewInt(20_000000),
		Reserve: "0x1111111111111111111111111111111111111111",
		Categories: map[string]Category{
			"infra": {
				Name: "infra", Enabled: true,
				Budget: big.NewInt(50_000000), PerTxCap: big.NewInt(10_000000), Remaining: big.NewInt(50_000000),
				Payees: map[string]bool{payee: true},
			},
		},
	}
	policy := Policy{
		AgentID: "pulse-operator", ChainID: "5042002", Vault: "0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01",
		ReserveFloor: big.NewInt(8_000000), ReserveTarget: big.NewInt(10_000000),
		Horizon: 168 * time.Hour, Now: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC),
	}
	report, err := Run(context.Background(), LoopInput{
		Policy: policy,
		Payables: []Payable{{
			ID: "one", Category: "infra", Payee: payee, Amount: big.NewInt(2_000000),
			Due: policy.Now.Add(-time.Hour),
		}},
		Chain: &statusChain{snap: snap, payStatus: "dry_run_paid"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if FormatUSDC(report.Decisions[0].Amount) != "2.000000" || report.Decisions[0].Outcome != "dry_run_paid" {
		t.Fatalf("pay %+v", report.Decisions[0])
	}
	sweep := report.Decisions[len(report.Decisions)-1]
	if sweep.Action != ActionSweep || FormatUSDC(sweep.Amount) != "8.000000" {
		t.Fatalf("sweep %+v amount %s", sweep, FormatUSDC(sweep.Amount))
	}
}

func TestMaxSpendPerRunDefers(t *testing.T) {
	payee := NormalizeAddress("0x2222222222222222222222222222222222222222")
	snap := Snapshot{
		Balance: big.NewInt(20_000000),
		Reserve: "0x1111111111111111111111111111111111111111",
		Categories: map[string]Category{
			"infra": {
				Name: "infra", Enabled: true,
				Budget: big.NewInt(50_000000), PerTxCap: big.NewInt(10_000000), Remaining: big.NewInt(50_000000),
				Payees: map[string]bool{payee: true},
			},
		},
	}
	policy := Policy{
		AgentID: "pulse-operator", ChainID: "5042002", Vault: "0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01",
		ReserveFloor: big.NewInt(1), ReserveTarget: big.NewInt(1),
		Horizon: 168 * time.Hour, Now: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC),
		MaxSpendPerRun: big.NewInt(1_000000),
	}
	report, err := Run(context.Background(), LoopInput{
		Policy: policy,
		Payables: []Payable{{
			ID: "big", Category: "infra", Payee: payee, Amount: big.NewInt(2_000000),
			Due: policy.Now.Add(-time.Hour),
		}},
		Chain: NewMockChain(snap),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decisions[0].Action != ActionDefer || report.Decisions[0].ReasonCode != ReasonMaxSpend {
		t.Fatalf("%+v", report.Decisions[0])
	}
}

func TestSettledPayablesDropOutOfObligations(t *testing.T) {
	payee := NormalizeAddress("0x2222222222222222222222222222222222222222")
	policy := Policy{
		AgentID: "pulse-operator", ChainID: "5042002", Vault: "0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01",
		ReserveFloor: big.NewInt(8_000000), ReserveTarget: big.NewInt(10_000000),
		Horizon: 168 * time.Hour, Now: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC),
	}
	due := policy.Now.Add(-time.Hour)
	report, err := Run(context.Background(), LoopInput{
		Policy: policy,
		Payables: []Payable{
			{ID: "done", Category: "infra", Payee: payee, Amount: big.NewInt(5_000000), Due: due, Status: "paid"},
			{ID: "shut", Category: "infra", Payee: payee, Amount: big.NewInt(1_000000), Due: due, Status: "closed"},
			{ID: "wait", Category: "people", Payee: payee, Amount: big.NewInt(700000), Due: due, Status: "escalated"},
		},
		Chain: NewMockChain(Snapshot{Balance: big.NewInt(20_000000), Reserve: "0x1111111111111111111111111111111111111111"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if FormatUSDC(report.OpeningLiquidity.Obligations) != "0.700000" {
		t.Fatalf("obligations %s", FormatUSDC(report.OpeningLiquidity.Obligations))
	}
	for _, d := range report.Decisions {
		if d.PayableID == "done" || d.PayableID == "shut" || d.PayableID == "wait" {
			t.Fatalf("settled or escalated payable was decided again: %+v", d)
		}
	}
}

type statusChain struct {
	snap      Snapshot
	payStatus string
}

func (s *statusChain) Observe(context.Context) (Snapshot, error) { return cloneSnapshot(s.snap), nil }
func (s *statusChain) Pay(context.Context, PayCall) (ExecResult, error) {
	return ExecResult{Status: s.payStatus}, nil
}
func (s *statusChain) Sweep(context.Context, SweepCall) (ExecResult, error) {
	return ExecResult{Status: "dry_run"}, nil
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Dir(filepath.Dir(file))
}
