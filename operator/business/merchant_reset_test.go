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
)

func TestMerchantResetRefusesUntilEveryCheckPasses(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	payer := "0x3333333333333333333333333333333333333333"
	nonce := "0x" + strings.Repeat("ab", 32)
	past := int64(1_700_000_000)
	now := time.Unix(past+100, 0)
	cases := []struct {
		name        string
		yes         bool
		signed      bool
		validBefore int64
		when        time.Time
		known       bool
		auth        *big.Int
		authMissing bool
		balanceCode int
		balanceBody string
		listCode    int
		listBody    string
		want        string
	}{
		{name: "needs yes", yes: false, signed: true, validBefore: past, when: now, known: true, auth: big.NewInt(0), balanceCode: 200, balanceBody: `{"status":"SUCCESS","balance":4400}`, listCode: 200, listBody: `{"status":"SUCCESS","domains":[]}`, want: "requires --yes"},
		{name: "not signed", yes: true, signed: false, validBefore: past, when: now, known: true, auth: big.NewInt(0), balanceCode: 200, balanceBody: `{"status":"SUCCESS","balance":4400}`, listCode: 200, listBody: `{"status":"SUCCESS","domains":[]}`, want: "current attempt is not signed"},
		{name: "validBefore future", yes: true, signed: true, validBefore: past + 1000, when: now, known: true, auth: big.NewInt(0), balanceCode: 200, balanceBody: `{"status":"SUCCESS","balance":4400}`, listCode: 200, listBody: `{"status":"SUCCESS","domains":[]}`, want: "validBefore has not passed"},
		{name: "validBefore equal", yes: true, signed: true, validBefore: past + 100, when: now, known: true, auth: big.NewInt(0), balanceCode: 200, balanceBody: `{"status":"SUCCESS","balance":4400}`, listCode: 200, listBody: `{"status":"SUCCESS","domains":[]}`, want: "validBefore has not passed"},
		{name: "authorization used", yes: true, signed: true, validBefore: past, when: now, known: true, auth: big.NewInt(1), balanceCode: 200, balanceBody: `{"status":"SUCCESS","balance":4400}`, listCode: 200, listBody: `{"status":"SUCCESS","domains":[]}`, want: "authorization was already used"},
		{name: "authorization query failed", yes: true, signed: true, validBefore: past, when: now, known: true, authMissing: true, balanceCode: 200, balanceBody: `{"status":"SUCCESS","balance":4400}`, listCode: 200, listBody: `{"status":"SUCCESS","domains":[]}`, want: "authorizationState"},
		{name: "balance not recorded", yes: true, signed: true, validBefore: past, when: now, known: false, auth: big.NewInt(0), balanceCode: 200, balanceBody: `{"status":"SUCCESS","balance":4400}`, listCode: 200, listBody: `{"status":"SUCCESS","domains":[]}`, want: "account balance was not recorded"},
		{name: "balance changed", yes: true, signed: true, validBefore: past, when: now, known: true, auth: big.NewInt(0), balanceCode: 200, balanceBody: `{"status":"SUCCESS","balance":1}`, listCode: 200, listBody: `{"status":"SUCCESS","domains":[]}`, want: "account balance changed"},
		{name: "balance unread", yes: true, signed: true, validBefore: past, when: now, known: true, auth: big.NewInt(0), balanceCode: 500, balanceBody: `{"status":"ERROR","message":"down"}`, listCode: 200, listBody: `{"status":"SUCCESS","domains":[]}`, want: "account balance"},
		{name: "domain registered", yes: true, signed: true, validBefore: past, when: now, known: true, auth: big.NewInt(0), balanceCode: 200, balanceBody: `{"status":"SUCCESS","balance":4400}`, listCode: 200, listBody: "", want: "already in the Porkbun account"},
		{name: "domain list failed", yes: true, signed: true, validBefore: past, when: now, known: true, auth: big.NewInt(0), balanceCode: 200, balanceBody: `{"status":"SUCCESS","balance":4400}`, listCode: 500, listBody: `{"status":"ERROR","code":"LIST_FAILED","message":"down"}`, want: "domain list"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			listBody := tc.listBody
			bill, cfg, vault, burn := resetBill(t, payer, nonce, tc.signed, tc.known, tc.validBefore, tc.auth, tc.authMissing, tc.balanceCode, tc.balanceBody, tc.listCode, &listBody)
			before := bill.MerchantAttempts
			checkout := bill.PorkbunCheckoutID
			row, err := merchantResetAt(context.Background(), cfg, bill.Code, tc.yes, tc.when)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v", err)
			}
			if row != nil && row.PorkbunCheckoutID == "" && strings.Contains(row.MerchantAttempts, `"opened by merchant-reset"`) {
				t.Fatalf("reset opened an attempt: %s", row.MerchantAttempts)
			}
			saved, err := models.FindBill(bill.Code)
			if err != nil || saved == nil || saved.MerchantAttempts != before || saved.PorkbunCheckoutID != checkout || saved.VaultTx != vault || saved.CCTPBurnTx != burn {
				t.Fatalf("bill changed attempts=%s checkout=%s vault=%s err=%v", saved.MerchantAttempts, saved.PorkbunCheckoutID, saved.VaultTx, err)
			}
			if text, readErr := os.ReadFile(cfg.AuditLog); readErr == nil && strings.Contains(string(text), "merchant_reset") {
				t.Fatalf("audit recorded a refused reset: %s", text)
			}
		})
	}
}

func TestMerchantResetOpensTheNextAttemptWhenEveryCheckPasses(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	payer := "0x3333333333333333333333333333333333333333"
	nonce := "0x" + strings.Repeat("ab", 32)
	past := int64(1_700_000_000)
	listBody := `{"status":"SUCCESS","domains":[]}`
	bill, cfg, vault, burn := resetBill(t, payer, nonce, true, true, past, big.NewInt(0), false, 200, `{"status":"SUCCESS","balance":4400}`, 200, &listBody)
	mint := bill.BaseMintTx
	row, err := merchantResetAt(context.Background(), cfg, bill.Code, true, time.Unix(past+100, 0))
	if err != nil {
		t.Fatal(err)
	}
	want := merchantAttemptKey(bill.Code, 3)
	if row.PorkbunCheckoutID != "" || row.VaultTx != vault || row.CCTPBurnTx != burn || row.BaseMintTx != mint {
		t.Fatalf("checkout=%s vault=%s burn=%s mint=%s", row.PorkbunCheckoutID, row.VaultTx, row.CCTPBurnTx, row.BaseMintTx)
	}
	if !strings.Contains(row.MerchantAttempts, `"outcome":"reset"`) || !strings.Contains(row.MerchantAttempts, want) || !strings.Contains(row.MerchantAttempts, "opened by merchant-reset") || strings.Count(row.MerchantAttempts, `"n":`) != 2 {
		t.Fatalf("attempts %s", row.MerchantAttempts)
	}
	if strings.Contains(row.MerchantAttempts, "pk1_resettest") || strings.Contains(row.MerchantAttempts, "sk1_resettest") {
		t.Fatalf("secret in attempts %s", row.MerchantAttempts)
	}
	text, err := os.ReadFile(cfg.AuditLog)
	if err != nil || !strings.Contains(string(text), `"kind":"merchant_reset"`) || !strings.Contains(string(text), want) || strings.Contains(string(text), "pk1_resettest") || strings.Contains(string(text), nonce) {
		t.Fatalf("audit %v %s", err, text)
	}
}

func resetBill(t *testing.T, payer, nonce string, signed, known bool, validBefore int64, auth *big.Int, authMissing bool, balanceCode int, balanceBody string, listCode int, listBody *string) (*models.Bill, treasury.Config, string, string) {
	t.Helper()
	bill := bridgedBill(t, "bill-reset-"+strings.ReplaceAll(t.Name(), "/", "-"))
	bill.PorkbunCheckoutID = "chk-reset"
	attempt := merchantAttempt{
		N: 2, Key: merchantAttemptKey(bill.Code, 2), At: "2026-10-08T09:22:00Z",
		Outcome: "signed", Checkout: "chk-reset", Signed: signed, ValidBefore: validBefore,
		Nonce: nonce, Payer: payer, BalanceCents: 4400, BalanceKnown: known,
	}
	if !signed {
		attempt.Outcome = "pending"
	}
	if err := writeMerchantAttempts(bill, []merchantAttempt{attempt}); err != nil {
		t.Fatal(err)
	}
	if err := models.SaveBill(bill); err != nil {
		t.Fatal(err)
	}
	domain := bill.Domain
	if listBody != nil && *listBody == "" {
		*listBody = `{"status":"SUCCESS","domains":[{"domain":"` + domain + `"}]}`
	}
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
	cfg := treasury.Config{
		Mode: "mainnet",
		Porkbun: treasury.PorkbunConfig{
			APIBase: pork.URL, APIKeyEnv: "PORKBUN_API_KEY", SecretEnv: "PORKBUN_SECRET_API_KEY",
		},
		Base:        treasury.BaseChainConfig{RPCEnv: "BASE_RPC_URL", USDC: procurement.BaseUSDC},
		Procurement: treasury.ProcurementConfig{Address: payer, BaseAddress: payer},
		AuditLog:    filepath.Join(t.TempDir(), "audit.jsonl"),
	}
	return bill, cfg, bill.VaultTx, bill.CCTPBurnTx
}
