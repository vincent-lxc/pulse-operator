package business

import (
	"strings"
	"testing"
)

func TestAdminBillApproveAllowedOnlyInDryRun(t *testing.T) {
	if !AdminBillApproveAllowed("") || !AdminBillApproveAllowed("dry-run") {
		t.Fatal("dry-run should keep the button")
	}
	for _, mode := range []string{"testnet", "live", "mainnet"} {
		if AdminBillApproveAllowed(mode) {
			t.Fatalf("%s still exposes bill approve", mode)
		}
	}
}

func TestViewPortFromArgs(t *testing.T) {
	if got := ViewPortFromArgs(nil); got != 80 {
		t.Fatalf("default %d", got)
	}
	if got := ViewPortFromArgs([]string{"pulse"}); got != 80 {
		t.Fatalf("binary only %d", got)
	}
	if got := ViewPortFromArgs([]string{"pulse", "-view", "0"}); got != 0 {
		t.Fatalf("disabled %d", got)
	}
	if got := ViewPortFromArgs([]string{"pulse", "--view=43123"}); got != 43123 {
		t.Fatalf("equals %d", got)
	}
}

func TestGuardExposedView(t *testing.T) {
	err := GuardExposedView("mainnet", 80, false)
	if err == nil || !strings.Contains(err.Error(), "testtoken") || !strings.Contains(err.Error(), "-view 0") {
		t.Fatal(err)
	}
	if err := GuardExposedView("mainnet", 0, false); err != nil {
		t.Fatal(err)
	}
	if err := GuardExposedView("mainnet", 80, true); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"", "dry-run", "testnet", "live"} {
		if err := GuardExposedView(mode, 80, false); err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
	}
}
