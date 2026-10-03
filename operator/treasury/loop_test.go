package treasury

import (
	"context"
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
	if len(report.Inflows) != 1 || FormatUSDC(report.Inflows[0].Amount) != "5.000000" {
		t.Fatalf("inflows: %+v", report.Inflows)
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Dir(filepath.Dir(file))
}
