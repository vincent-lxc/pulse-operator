package business

import (
	"context"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/procurement"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

func TestStoredCheckoutIgnoresBaseBalance(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		checkout  string
		status    int
		body      string
		wantState string
		wantCode  string
		wantOrder string
		wantErr   string
		posts     int
	}{
		{
			name: "done", checkout: "chk-pending", status: http.StatusOK,
			body:      `{"status":"SUCCESS","orderId":"ord-done","checkoutId":"chk-pending"}`,
			wantState: "done", wantOrder: "ord-done", posts: 1,
		},
		{
			name: "pending", checkout: "chk-pending", status: http.StatusOK,
			body:      `{"status":"SUCCESS","code":"PAYMENT_PENDING","checkoutId":"chk-pending"}`,
			wantState: "bridged", wantCode: "payment_pending", posts: 1,
		},
		{
			name: "review", checkout: "chk-pending", status: http.StatusPaymentRequired,
			body:      `{"status":"ERROR","code":"PAYMENT_REQUIRED","checkoutId":"chk-pending"}`,
			wantState: "failed_merchant", wantCode: "checkout_needs_review", wantErr: "checkout_needs_review", posts: 1,
		},
		{
			name: "low", checkout: "", status: http.StatusOK,
			body:      `{"status":"SUCCESS","orderId":"should-not"}`,
			wantState: "bridged", wantCode: "awaiting_mint", posts: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var posts, signatures, baseCalls int
			pork := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts++
				body, _ := io.ReadAll(r.Body)
				if r.Header.Get("PAYMENT-SIGNATURE") != "" {
					signatures++
				}
				if tc.checkout != "" {
					if !strings.Contains(string(body), `"usdcCheckoutId":"`+tc.checkout+`"`) {
						t.Errorf("body %s", body)
					}
					if !strings.HasPrefix(r.Header.Get("Idempotency-Key"), "bill-") {
						t.Errorf("idempotency %q", r.Header.Get("Idempotency-Key"))
					}
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer pork.Close()
			base := httptest.NewServer(scriptedChain("0x2105", map[string]string{
				selectorHex("balanceOf(address)"): word(big.NewInt(0)),
			}, "0x0", &rpcLog{}))
			defer base.Close()
			probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				baseCalls++
				writeRPC(w, nil, nil, "balance should not be read")
			}))
			defer probe.Close()
			if tc.checkout == "" {
				t.Setenv("BASE_RPC_URL", base.URL)
			} else {
				t.Setenv("BASE_RPC_URL", probe.URL)
			}
			tag := strings.ReplaceAll(t.Name(), "/", "-")
			bill := models.NewBill()
			bill.Code = "bill-checkout-" + tag
			bill.Domain = tag + ".xyz"
			bill.Vendor = "porkbun"
			bill.Kind = "domain_register"
			bill.CategoryCode = "domains"
			bill.Years = 1
			bill.State = "bridged"
			bill.Action = treasury.ActionPay
			bill.DecisionHash = "0x" + strings.Repeat("11", 32)
			bill.VaultTx = "0x" + strings.Repeat("aa", 32)
			bill.BaseMintTx = "0x" + strings.Repeat("bb", 32)
			bill.PorkbunCheckoutID = tc.checkout
			bill.QuoteCents = 204
			if _, err := models.InsertBill(bill); err != nil {
				t.Fatal(err)
			}
			var vaults, burns int
			deps := generousDeps(t)
			deps.cfg.Mode = "mainnet"
			deps.cfg.Base = treasury.BaseChainConfig{RPCEnv: "BASE_RPC_URL", USDC: procurement.BaseUSDC}
			deps.cfg.Procurement = treasury.ProcurementConfig{Address: "0x3333333333333333333333333333333333333333"}
			deps.vault = func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
				vaults++
				return vaultResult{}, nil
			}
			deps.burn = func(context.Context, *models.Bill, *big.Int, *big.Int) (burnResult, error) {
				burns++
				return burnResult{}, nil
			}
			client := &procurement.Porkbun{BaseURL: pork.URL, APIKey: "pk", Secret: "ps", MinInterval: -1}
			deps.merchant = func(ctx context.Context, row *models.Bill) (merchantResult, error) {
				return liveMerchant(ctx, deps.cfg, client, row)
			}
			err := AdvanceBill(context.Background(), bill, deps)
			if bill.State != tc.wantState || bill.ReasonCode != tc.wantCode || bill.PorkbunOrderID != tc.wantOrder || posts != tc.posts || signatures != 0 || vaults != 0 || burns != 0 {
				t.Fatalf("state=%s code=%s order=%s posts=%d sig=%d vaults=%d burns=%d err=%v", bill.State, bill.ReasonCode, bill.PorkbunOrderID, posts, signatures, vaults, burns, err)
			}
			if tc.checkout != "" && baseCalls != 0 {
				t.Fatalf("base balance was read %d times", baseCalls)
			}
			if tc.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err %v", err)
			}
			if tc.checkout != "" && bill.PorkbunCheckoutID != tc.checkout {
				t.Fatalf("checkout %s", bill.PorkbunCheckoutID)
			}
		})
	}
}

func TestVaultReceiptUnknownDoesNotBurn(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	bill := models.NewBill()
	tag := strings.ReplaceAll(t.Name(), "/", "-")
	bill.Code = "bill-receipt-" + tag
	bill.Domain = tag + ".xyz"
	bill.Vendor = "porkbun"
	bill.Kind = "domain_register"
	bill.CategoryCode = "domains"
	bill.Years = 1
	bill.State = "quoted"
	if _, err := models.InsertBill(bill); err != nil {
		t.Fatal(err)
	}
	hash := "0x" + strings.Repeat("cd", 32)
	var vaults, burns, orders int
	pending := big.NewInt(0)
	deps := generousDeps(t)
	deps.cfg.Mode = "mainnet"
	deps.pendingSpend = pending
	deps.vault = func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
		vaults++
		return vaultResult{TxHash: hash, Status: "paid"}, context.DeadlineExceeded
	}
	deps.burn = func(context.Context, *models.Bill, *big.Int, *big.Int) (burnResult, error) {
		burns++
		return burnResult{BurnTx: "should-not"}, nil
	}
	deps.merchant = func(context.Context, *models.Bill) (merchantResult, error) {
		orders++
		return merchantResult{OrderID: "should-not"}, nil
	}
	err := AdvanceBill(context.Background(), bill, deps)
	if bill.State != "failed_vault" || bill.ReasonCode != "vault_receipt_unknown" || bill.VaultTx != hash || vaults != 1 || burns != 0 || orders != 0 || pending.Sign() == 0 {
		t.Fatalf("state=%s code=%s tx=%s vaults=%d burns=%d orders=%d pending=%s err=%v", bill.State, bill.ReasonCode, bill.VaultTx, vaults, burns, orders, pending, err)
	}
	if err == nil || !strings.Contains(err.Error(), "vault_receipt_unknown") {
		t.Fatal(err)
	}
	if err = AdvanceBill(context.Background(), bill, deps); err == nil || !strings.Contains(err.Error(), "vault_receipt_unknown") {
		t.Fatal(err)
	}
	if vaults != 1 || burns != 0 || orders != 0 || bill.VaultTx != hash || pending.Sign() == 0 {
		t.Fatalf("retry vaults=%d burns=%d orders=%d tx=%s pending=%s", vaults, burns, orders, bill.VaultTx, pending)
	}
}
