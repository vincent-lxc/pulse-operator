package business

import (
	"context"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/procurement"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestExportAndMerchantResetReadSignedAttemptFromSQLite(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	payer := "0x3333333333333333333333333333333333333333"
	nonce := "0x" + strings.Repeat("ab", 32)
	past := int64(1_700_000_000)
	bill := bridgedBill(t, "bill-register-pulseoperator-top-load")
	bill.Domain = "pulseoperator.top"
	bill.State = "failed_merchant"
	bill.Mode = "mainnet"
	if err := models.SaveBill(bill); err != nil {
		t.Fatal(err)
	}
	attempts := `[{"n":2,"key":"bill-bill-register-pulseoperator-top-load-a2","at":"2026-10-08T09:23:00Z","outcome":"signed","reason":"INSUFFICIENT_FUNDS","checkout":"6ac741526bcab4fcd1cee1da","signed":true,"valid_before":1700000000,"nonce":"` + nonce + `","payer":"` + payer + `"}]`
	x402 := `{"x402Version":2,"accepts":[{"scheme":"auth-capture"}]}`
	db := openBillDB(t)
	if err := db.Exec(`UPDATE bill SET merchant_attempts=?, latency_ms=?, x402_required=?, risk_notes=?, cctp_message_hash='', forward_fee_units='' WHERE code=?`,
		attempts, 1855, x402, "Low risk", bill.Code).Error; err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`ALTER TABLE bill ADD COLUMN merchantAttempts TEXT`,
		`ALTER TABLE bill ADD COLUMN x402Required TEXT`,
		`ALTER TABLE bill ADD COLUMN riskNotes TEXT`,
		`ALTER TABLE bill ADD COLUMN latencyMS INTEGER`,
		`ALTER TABLE bill ADD COLUMN cctp_message TEXT`,
		`ALTER TABLE bill ADD COLUMN forward_fee TEXT`,
	} {
		if err := db.Exec(stmt).Error; err != nil && !strings.Contains(err.Error(), "duplicate column") {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`UPDATE bill SET merchantAttempts='', x402Required='', riskNotes='', latencyMS=0, cctp_message=?, forward_fee=? WHERE code=?`,
		"0xmessage", "54585", bill.Code).Error; err != nil {
		t.Fatal(err)
	}

	exported, markdown, err := ExportBill(bill.Code)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(exported, `"latency_ms": 1855`) || !strings.Contains(exported, "6ac741526bcab4fcd1cee1da") || !strings.Contains(exported, `"x402_required"`) || !strings.Contains(markdown, "latency_ms: 1855") || !strings.Contains(exported, "0xmessage") || !strings.Contains(exported, "54585") {
		t.Fatalf("export\n%s\n%s", markdown, exported)
	}

	listBody := `{"status":"SUCCESS","domains":[]}`
	cfg := resetServers(t, payer, big.NewInt(0), false, 200, `{"status":"SUCCESS","balance":0}`, 200, &listBody)
	future, err := merchantResetAt(context.Background(), cfg, bill.Code, true, int64Ptr(0), time.Unix(past-100, 0))
	if err == nil || !strings.Contains(err.Error(), "validBefore") || strings.Contains(err.Error(), "not signed") {
		t.Fatalf("future err=%v row=%v", err, future)
	}
	saved, err := models.FindBill(bill.Code)
	if err != nil || saved == nil || !strings.Contains(saved.MerchantAttempts, `"signed":true`) || strings.Contains(saved.MerchantAttempts, "opened by merchant-reset") {
		t.Fatalf("future mutated %v %s", err, saved.MerchantAttempts)
	}

	row, err := merchantResetAt(context.Background(), cfg, bill.Code, true, int64Ptr(0), time.Unix(past+100, 0))
	if err != nil {
		t.Fatal(err)
	}
	if row.VaultTx != bill.VaultTx || row.CCTPBurnTx != bill.CCTPBurnTx || row.BaseMintTx != bill.BaseMintTx || row.PorkbunCheckoutID != "" {
		t.Fatalf("money fields changed vault=%s burn=%s mint=%s checkout=%s", row.VaultTx, row.CCTPBurnTx, row.BaseMintTx, row.PorkbunCheckoutID)
	}
	if !strings.Contains(row.MerchantAttempts, `"outcome":"reset"`) || !strings.Contains(row.MerchantAttempts, "opened by merchant-reset") || !strings.Contains(row.MerchantAttempts, `"signed":true`) {
		t.Fatalf("attempts %s", row.MerchantAttempts)
	}
}

func int64Ptr(n int64) *int64 { return &n }

func openBillDB(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join("db", "operator", "operator.ldb")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("PRAGMA busy_timeout=30000").Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func resetServers(t *testing.T, payer string, auth *big.Int, authMissing bool, balanceCode int, balanceBody string, listCode int, listBody *string) treasury.Config {
	t.Helper()
	pork := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PAYMENT-SIGNATURE") != "" || strings.Contains(r.URL.Path, "/domain/create/") || strings.Contains(r.URL.Path, "/domain/renew/") {
			t.Errorf("merchant-reset called %s", r.URL.Path)
		}
		if strings.HasSuffix(r.URL.Path, "/account/balance") {
			w.WriteHeader(balanceCode)
			_, _ = w.Write([]byte(balanceBody))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/domain/listAll") {
			w.WriteHeader(listCode)
			_, _ = w.Write([]byte(*listBody))
			return
		}
		t.Errorf("unexpected porkbun %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(pork.Close)
	calls := map[string]string{}
	if !authMissing && auth != nil {
		calls[selectorHex("authorizationState(address,bytes32)")] = word(auth)
	}
	base := httptest.NewServer(scriptedChain("0x2105", calls, "0x0", &rpcLog{}))
	t.Cleanup(base.Close)
	t.Setenv("BASE_RPC_URL", base.URL)
	t.Setenv("PORKBUN_API_KEY", "pk1_resettest")
	t.Setenv("PORKBUN_SECRET_API_KEY", "sk1_resettest")
	return treasury.Config{
		Mode: "mainnet",
		Porkbun: treasury.PorkbunConfig{
			APIBase: pork.URL, APIKeyEnv: "PORKBUN_API_KEY", SecretEnv: "PORKBUN_SECRET_API_KEY",
		},
		Base:        treasury.BaseChainConfig{RPCEnv: "BASE_RPC_URL", USDC: procurement.BaseUSDC},
		Procurement: treasury.ProcurementConfig{Address: payer, BaseAddress: payer},
		AuditLog:    filepath.Join(t.TempDir(), "audit.jsonl"),
	}
}
