package business

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "operator-sqlite")
	if err != nil {
		panic(err)
	}
	if err := os.Chdir(dir); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func TestRunSampleLedgerPersistsDecisions(t *testing.T) {
	root := moduleRoot(t)
	cfg, err := treasury.LoadConfig(filepath.Join(root, "config", "dry-run.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Ledger = filepath.Join(root, "testdata", "ledger.yaml")
	cfg.VaultFixture = filepath.Join(root, "testdata", "vault.yaml")
	cfg.AuditLog = filepath.Join(t.TempDir(), "audit.jsonl")
	var buf stringsBuilder
	report, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteReport(&buf, report); err != nil {
		t.Fatal(err)
	}
	text := buf.String()
	for _, needle := range []string{"action=pay", "action=defer", "action=sweep_to_reserve", "action=escalate_to_human"} {
		if !contains(text, needle) {
			t.Fatalf("missing %s in %s", needle, text)
		}
	}
	rows, err := models.ListDecisions()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 4 {
		t.Fatalf("stored decisions %d", len(rows))
	}
	view, err := LoadDashboard()
	if err != nil {
		t.Fatal(err)
	}
	if view.Balance == "" || len(view.Approvals) == 0 || len(view.Revenues) == 0 {
		t.Fatalf("dashboard %+v", view)
	}
	if view.CircleProduct != treasury.ProductWallets || !contains(text, "circle=circle:wallets") || !contains(text, "circle=circle:cctp") || !contains(text, "circle=circle:gateway") {
		t.Fatalf("circle tags missing in dashboard %+v\n%s", view.CircleProduct, text)
	}
	if view.Approvals[0].CircleProduct != treasury.ProductWallets {
		t.Fatalf("approval circle %+v", view.Approvals[0])
	}
	again, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	var sweep treasury.Decision
	for _, d := range again.Decisions {
		if d.PayableID == "cycle" {
			sweep = d
		}
	}
	if sweep.Action != treasury.ActionDefer || sweep.ReasonCode != treasury.ReasonNoSurplus {
		t.Fatalf("second mock run swept again: %+v", sweep)
	}
	if payableState(treasury.Decision{Action: treasury.ActionPay, Outcome: "dry_run_paid"}) != "" {
		t.Fatal("dry-run pay must not mark the payable paid")
	}
	if payableState(treasury.Decision{Action: treasury.ActionPay, Outcome: "paid"}) != "paid" {
		t.Fatal("live pay should mark the payable paid")
	}
	if err := RecordRevenue("manual-1", "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "1.00", "wire"); err != nil {
		t.Fatal(err)
	}
}

type stringsBuilder struct{ b []byte }

func (s *stringsBuilder) Write(p []byte) (int, error) {
	s.b = append(s.b, p...)
	return len(p), nil
}
func (s *stringsBuilder) String() string { return string(s.b) }

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Dir(filepath.Dir(file))
}
