package procurement

import (
	"math/big"
	"strings"
	"testing"
)

func TestMainnetGuardRejectsTestnetVaultAndMissingFlags(t *testing.T) {
	g := Gate{
		Mode: "mainnet", ArcChainID: big.NewInt(5042), BaseChainID: big.NewInt(8453),
		Vault: TestnetPolicyVault, VaultCodeOK: true, ConfirmMainnetEnv: true,
		PorkbunKey: true, Procurement: "0x1111111111111111111111111111111111111111",
		MaxSpend: big.NewInt(15_000_000), MaxBill: big.NewInt(15_000_000),
	}
	err := Check(g, Flags{UnderstandRealMoney: true, Yes: true, BillCount: 1})
	if err == nil || !strings.Contains(err.Error(), "PulseReceipt") {
		t.Fatal(err)
	}
	g.Vault = "0x2222222222222222222222222222222222222222"
	if err := Check(g, Flags{UnderstandRealMoney: true, BillCount: 1}); err == nil {
		t.Fatal("expected --yes or --auto")
	}
	g.ConfirmMainnetEnv = false
	if err := Check(g, Flags{UnderstandRealMoney: true, Yes: true, BillCount: 1}); err == nil {
		t.Fatal("expected confirm env")
	}
}

func TestDryRunGuardIsOpen(t *testing.T) {
	if err := Check(Gate{Mode: "dry-run"}, Flags{}); err != nil {
		t.Fatal(err)
	}
}
