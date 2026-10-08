package business

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/procurement"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

func servePorkbunBalance(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasSuffix(r.URL.Path, "/account/balance") {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"status":"SUCCESS","balance":4400}`)
	return true
}

func TestMerchantKeyRotatesOnlyAfterADeadAttempt(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "mismatch", status: http.StatusBadRequest, body: `{"status":"ERROR","code":"IDEMPOTENCY_KEY_MISMATCH","message":"body changed pk1_shouldnotappear"}`, want: "merchant_idempotency_mismatch"},
		{name: "funds", status: http.StatusBadRequest, body: `{"status":"ERROR","code":"INSUFFICIENT_FUNDS","message":"credit"}`, want: "merchant_insufficient_funds"},
		{name: "no402", status: http.StatusBadRequest, body: `{"status":"ERROR","code":"COST_MISMATCH","message":"quote"}`, want: "merchant_cost_mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, payer := testPayer(t)
			var keys []string
			pork := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				keys = append(keys, r.Header.Get("Idempotency-Key"))
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer pork.Close()
			base := balanceServer(t)
			defer base.Close()
			t.Setenv("BASE_RPC_URL", base.URL)
			bill := bridgedBill(t, "bill-key-"+strings.ReplaceAll(t.Name(), "/", "-"))
			deps := merchantDeps(t, pork.URL, payer)
			var vaults, burns int
			deps.vault = countVault(&vaults)
			deps.burn = countBurn(&burns)
			if err := AdvanceBill(context.Background(), bill, deps); bill.ReasonCode != tc.want || vaults != 0 || burns != 0 || len(keys) != 1 || keys[0] != merchantAttemptKey(bill.Code, 1) {
				t.Fatalf("code=%s keys=%v vaults=%d burns=%d err=%v", bill.ReasonCode, keys, vaults, burns, err)
			}
			if strings.Contains(bill.MerchantAttempts, "pk1_shouldnotappear") {
				t.Fatalf("secret in attempts %s", bill.MerchantAttempts)
			}
			exported, markdown, err := ExportBill(bill.Code)
			if err != nil || !strings.Contains(exported, keys[0]) || !strings.Contains(markdown, keys[0]) || strings.Contains(exported, "pk1_shouldnotappear") {
				t.Fatalf("export err=%v\n%s\n%s", err, exported, markdown)
			}
			if err = AdvanceBill(context.Background(), bill, deps); bill.ReasonCode != tc.want || len(keys) != 2 || keys[1] != merchantAttemptKey(bill.Code, 2) || keys[1] == keys[0] || vaults != 0 || burns != 0 {
				t.Fatalf("retry code=%s keys=%v vaults=%d burns=%d err=%v", bill.ReasonCode, keys, vaults, burns, err)
			}
		})
	}
}

func TestMerchantKeyReusedWhenTheAttemptHasNoAnswer(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	_, payer := testPayer(t)
	var keys []string
	var hits int
	pork := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		if hits == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, `{"status":"ERROR","message":"upstream"}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"SUCCESS","code":"PAYMENT_PENDING","checkoutId":"chk-same"}`)
	}))
	defer pork.Close()
	base := balanceServer(t)
	defer base.Close()
	t.Setenv("BASE_RPC_URL", base.URL)
	bill := bridgedBill(t, "bill-retry-"+strings.ReplaceAll(t.Name(), "/", "-"))
	deps := merchantDeps(t, pork.URL, payer)
	if err := AdvanceBill(context.Background(), bill, deps); err == nil || len(keys) != 1 {
		t.Fatalf("first keys=%v err=%v attempts=%s", keys, err, bill.MerchantAttempts)
	}
	if !strings.Contains(bill.MerchantAttempts, `"outcome":"started"`) {
		t.Fatalf("attempts %s", bill.MerchantAttempts)
	}
	if err := AdvanceBill(context.Background(), bill, deps); err != nil || len(keys) != 2 || keys[0] != keys[1] || keys[0] != merchantAttemptKey(bill.Code, 1) {
		t.Fatalf("retry keys=%v err=%v state=%s", keys, err, bill.State)
	}
	if strings.Count(bill.MerchantAttempts, `"n":`) != 1 {
		t.Fatalf("attempts %s", bill.MerchantAttempts)
	}
}

func TestLiveCheckoutDoesNotRotateOrResign(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	bill := bridgedBill(t, "bill-live-"+strings.ReplaceAll(t.Name(), "/", "-"))
	bill.PorkbunCheckoutID = "chk-live"
	bill.MerchantAttempts = `[{"n":3,"key":"` + merchantAttemptKey(bill.Code, 3) + `","at":"2026-10-08T00:00:00Z","outcome":"pending"}]`
	if err := models.SaveBill(bill); err != nil {
		t.Fatal(err)
	}
	var keys []string
	var signatures int
	pork := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"usdcCheckoutId":"chk-live"`) {
			t.Errorf("body %s", body)
		}
		if r.Header.Get("PAYMENT-SIGNATURE") != "" {
			signatures++
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"SUCCESS","code":"PAYMENT_PENDING","checkoutId":"chk-live"}`)
	}))
	defer pork.Close()
	deps := merchantDeps(t, pork.URL, "")
	var vaults, burns int
	deps.vault = countVault(&vaults)
	deps.burn = countBurn(&burns)
	if err := AdvanceBill(context.Background(), bill, deps); err != nil || signatures != 0 || vaults != 0 || burns != 0 || len(keys) != 1 || keys[0] != merchantAttemptKey(bill.Code, 3)+"-confirm" {
		t.Fatalf("keys=%v sig=%d vaults=%d burns=%d err=%v state=%s", keys, signatures, vaults, burns, err, bill.State)
	}
	if err := AdvanceBill(context.Background(), bill, deps); err != nil || signatures != 0 || len(keys) != 2 || keys[1] != keys[0] || strings.Count(bill.MerchantAttempts, `"n":`) != 1 {
		t.Fatalf("retry keys=%v sig=%d attempts=%s err=%v", keys, signatures, bill.MerchantAttempts, err)
	}
}

func TestExpiredCheckoutTakesANewKey(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	_, payer := testPayer(t)
	bill := bridgedBill(t, "bill-expired-"+strings.ReplaceAll(t.Name(), "/", "-"))
	bill.PorkbunCheckoutID = "chk-old"
	bill.MerchantAttempts = `[{"n":1,"key":"` + merchantAttemptKey(bill.Code, 1) + `","at":"2026-10-08T00:00:00Z","outcome":"pending","checkout":"chk-old"}]`
	if err := models.SaveBill(bill); err != nil {
		t.Fatal(err)
	}
	var keys []string
	var signatures int
	var hits int
	pork := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		if r.Header.Get("PAYMENT-SIGNATURE") != "" {
			signatures++
		}
		if hits == 1 {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"status":"ERROR","code":"PAYMENT_EXPIRED","message":"checkout expired"}`)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"status":"ERROR","code":"INSUFFICIENT_FUNDS","message":"credit"}`)
	}))
	defer pork.Close()
	base := balanceServer(t)
	defer base.Close()
	t.Setenv("BASE_RPC_URL", base.URL)
	deps := merchantDeps(t, pork.URL, payer)
	if err := AdvanceBill(context.Background(), bill, deps); bill.ReasonCode != "merchant_checkout_expired" || signatures != 0 || len(keys) != 1 || keys[0] != merchantAttemptKey(bill.Code, 1)+"-confirm" {
		t.Fatalf("first code=%s keys=%v sig=%d err=%v", bill.ReasonCode, keys, signatures, err)
	}
	if err := AdvanceBill(context.Background(), bill, deps); bill.ReasonCode != "merchant_insufficient_funds" || signatures != 0 || len(keys) != 2 || keys[1] != merchantAttemptKey(bill.Code, 2) || bill.PorkbunCheckoutID != "" {
		t.Fatalf("second code=%s keys=%v checkout=%s sig=%d err=%v", bill.ReasonCode, keys, bill.PorkbunCheckoutID, signatures, err)
	}
}

func TestLegacyBillUsesAFreshKeyAndSignsOnce(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	_, payer := testPayer(t)
	code := "bill-register-pulseoperator-top"
	bill := bridgedBill(t, code)
	bill.Domain = "pulseoperator.top"
	bill.State = "failed_merchant"
	bill.ReasonCode = "merchant_insufficient_funds"
	bill.Reason = "porkbun INSUFFICIENT_FUNDS: credit"
	bill.MerchantAttempts = ""
	bill.PorkbunCheckoutID = ""
	if err := models.SaveBill(bill); err != nil {
		t.Fatal(err)
	}
	var keys []string
	var signatures int
	var quotes int
	header := base64.StdEncoding.EncodeToString([]byte(`{"x402Version":2,"accepts":[{"scheme":"exact","network":"eip155:8453","amount":"8750000","asset":"` + procurement.BaseUSDC + `","payTo":"0x3333333333333333333333333333333333333333","maxTimeoutSeconds":600,"extra":{"name":"USDC","version":"2"}}]}`))
	pork := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if servePorkbunBalance(w, r) {
			return
		}
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		if r.Header.Get("PAYMENT-SIGNATURE") == "" {
			w.Header().Set("PAYMENT-REQUIRED", header)
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = io.WriteString(w, `{"status":"ERROR","code":"PAYMENT_REQUIRED","checkoutId":"chk-new"}`)
			return
		}
		signatures++
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"SUCCESS","orderId":"ord-1","checkoutId":"chk-new"}`)
	}))
	defer pork.Close()
	base := balanceServer(t)
	defer base.Close()
	t.Setenv("BASE_RPC_URL", base.URL)
	deps := merchantDeps(t, pork.URL, payer)
	prev := deps.quote
	deps.quote = func(ctx context.Context, row *models.Bill) (int64, *big.Int, error) {
		quotes++
		return prev(ctx, row)
	}
	var vaults, burns int
	deps.vault = countVault(&vaults)
	deps.burn = countBurn(&burns)
	if err := AdvanceBill(context.Background(), bill, deps); err != nil {
		t.Fatal(err)
	}
	want := merchantAttemptKey(code, 2)
	legacy := legacyMerchantKey(code)
	if quotes < 1 || vaults != 0 || burns != 0 || signatures != 1 || len(keys) != 2 || keys[0] != want || keys[1] != want {
		t.Fatalf("quotes=%d vaults=%d burns=%d sig=%d keys=%v err state=%s", quotes, vaults, burns, signatures, keys, bill.State)
	}
	if keys[0] == legacy {
		t.Fatalf("legacy key was sent")
	}
	if bill.State != "done" || bill.PorkbunOrderID != "ord-1" || bill.VaultTx == "" || bill.BaseMintTx == "" {
		t.Fatalf("state=%s order=%s vault=%s mint=%s", bill.State, bill.PorkbunOrderID, bill.VaultTx, bill.BaseMintTx)
	}
	if bill.ReasonCode != "" || bill.Reason != "" {
		t.Fatalf("stale failure reason code=%s reason=%s", bill.ReasonCode, bill.Reason)
	}
	exported, _, err := ExportBill(bill.Code)
	if err != nil || !strings.Contains(exported, want) || !strings.Contains(exported, legacy) {
		t.Fatalf("export %v %s", err, exported)
	}
}

func TestSignedRequestFailureSignsOnce(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		status    int
		body      string
		wantState string
		wantCode  string
	}{
		{name: "http500", status: http.StatusInternalServerError, body: `{"status":"ERROR","message":"upstream"}`, wantState: "failed_merchant", wantCode: "x402_rejected"},
		{name: "in_use", status: http.StatusConflict, body: `{"status":"ERROR","code":"IDEMPOTENCY_KEY_IN_USE","message":"in flight","checkoutId":"chk-1"}`, wantState: "bridged", wantCode: "payment_pending"},
		{name: "mismatch", status: http.StatusConflict, body: `{"status":"ERROR","code":"PAYMENT_MISMATCH","message":"different price","checkoutId":"chk-1"}`, wantState: "failed_merchant", wantCode: "merchant_payment_mismatch"},
		{name: "funds", status: http.StatusBadRequest, body: `{"status":"ERROR","code":"INSUFFICIENT_FUNDS","message":"credit","checkoutId":"chk-1"}`, wantState: "failed_merchant", wantCode: "merchant_insufficient_funds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, payer := testPayer(t)
			var keys []string
			var signatures int
			code := "bill-signed-" + strings.ReplaceAll(t.Name(), "/", "-")
			pork := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if servePorkbunBalance(w, r) {
					return
				}
				keys = append(keys, r.Header.Get("Idempotency-Key"))
				raw, _ := io.ReadAll(r.Body)
				body := string(raw)
				sig := r.Header.Get("PAYMENT-SIGNATURE")
				if sig != "" {
					signatures++
					stored, err := models.FindBill(code)
					if err != nil || stored == nil || stored.PorkbunCheckoutID != "chk-1" || !strings.Contains(stored.MerchantAttempts, `"signed":true`) || !strings.Contains(stored.MerchantAttempts, `"valid_before":`) || !strings.Contains(stored.MerchantAttempts, `"nonce":"0x`) || !strings.Contains(stored.MerchantAttempts, `"balance_known":true`) || !strings.Contains(stored.MerchantAttempts, `"balance_cents":4400`) {
						attempts := ""
						checkout := ""
						if stored != nil {
							attempts = stored.MerchantAttempts
							checkout = stored.PorkbunCheckoutID
						}
						t.Errorf("signed marker missing before post checkout=%s attempts=%s err=%v", checkout, attempts, err)
					}
					if stored != nil && (strings.Contains(stored.MerchantAttempts, sig) || strings.Contains(stored.MerchantAttempts, `"signature"`)) {
						t.Errorf("signature stored in %s", stored.MerchantAttempts)
					}
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, tc.body)
					return
				}
				if strings.Contains(body, `"usdcCheckoutId"`) {
					if !strings.Contains(body, `"usdcCheckoutId":"chk-1"`) {
						t.Errorf("poll body %s", body)
					}
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, tc.body)
					return
				}
				w.Header().Set("PAYMENT-REQUIRED", exactOfferHeader())
				w.WriteHeader(http.StatusPaymentRequired)
				_, _ = io.WriteString(w, `{"status":"ERROR","code":"PAYMENT_REQUIRED","checkoutId":"chk-1"}`)
			}))
			defer pork.Close()
			base := balanceServer(t)
			defer base.Close()
			t.Setenv("BASE_RPC_URL", base.URL)
			bill := bridgedBill(t, code)
			deps := merchantDeps(t, pork.URL, payer)
			var vaults, burns int
			deps.vault = countVault(&vaults)
			deps.burn = countBurn(&burns)
			err := AdvanceBill(context.Background(), bill, deps)
			if tc.wantCode == "payment_pending" && err != nil {
				t.Fatalf("in progress err=%v", err)
			}
			if tc.wantCode != "payment_pending" && err == nil {
				t.Fatal("expected the signed request to fail")
			}
			if bill.State != tc.wantState || bill.ReasonCode != tc.wantCode || signatures != 1 || bill.PorkbunCheckoutID != "chk-1" || vaults != 0 || burns != 0 {
				t.Fatalf("state=%s code=%s checkout=%s sig=%d vaults=%d burns=%d err=%v attempts=%s", bill.State, bill.ReasonCode, bill.PorkbunCheckoutID, signatures, vaults, burns, err, bill.MerchantAttempts)
			}
			err = AdvanceBill(context.Background(), bill, deps)
			if tc.wantCode == "payment_pending" && err != nil {
				t.Fatalf("retry err=%v", err)
			}
			if tc.wantCode != "payment_pending" && err == nil {
				t.Fatal("expected the poll to fail")
			}
			if signatures != 1 || bill.State != tc.wantState || bill.ReasonCode != tc.wantCode || bill.PorkbunCheckoutID != "chk-1" || vaults != 0 || burns != 0 {
				t.Fatalf("retry state=%s code=%s checkout=%s sig=%d keys=%v err=%v attempts=%s", bill.State, bill.ReasonCode, bill.PorkbunCheckoutID, signatures, keys, err, bill.MerchantAttempts)
			}
			want := merchantAttemptKey(bill.Code, 1)
			confirm := want + "-confirm"
			if len(keys) != 3 || keys[0] != want || keys[1] != want || keys[2] != confirm {
				t.Fatalf("keys=%v", keys)
			}
			if strings.Count(bill.MerchantAttempts, `"n":`) != 1 || !strings.Contains(bill.MerchantAttempts, `"signed":true`) {
				t.Fatalf("attempts %s", bill.MerchantAttempts)
			}
		})
	}
}

func TestFresh402AfterSignedNeedsReview(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	_, payer := testPayer(t)
	var keys []string
	var signatures int
	pork := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if servePorkbunBalance(w, r) {
			return
		}
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		if r.Header.Get("PAYMENT-SIGNATURE") != "" {
			signatures++
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"status":"ERROR","message":"upstream","checkoutId":"chk-1"}`)
			return
		}
		if strings.Contains(body, `"usdcCheckoutId"`) {
			w.Header().Set("PAYMENT-REQUIRED", exactOfferHeader())
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = io.WriteString(w, `{"status":"ERROR","code":"PAYMENT_REQUIRED","checkoutId":"chk-1"}`)
			return
		}
		w.Header().Set("PAYMENT-REQUIRED", exactOfferHeader())
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = io.WriteString(w, `{"status":"ERROR","code":"PAYMENT_REQUIRED","checkoutId":"chk-1"}`)
	}))
	defer pork.Close()
	base := balanceServer(t)
	defer base.Close()
	t.Setenv("BASE_RPC_URL", base.URL)
	bill := bridgedBill(t, "bill-review-"+strings.ReplaceAll(t.Name(), "/", "-"))
	deps := merchantDeps(t, pork.URL, payer)
	if err := AdvanceBill(context.Background(), bill, deps); err == nil || signatures != 1 || bill.PorkbunCheckoutID != "chk-1" {
		t.Fatalf("first sig=%d checkout=%s err=%v attempts=%s", signatures, bill.PorkbunCheckoutID, err, bill.MerchantAttempts)
	}
	err := AdvanceBill(context.Background(), bill, deps)
	want := merchantAttemptKey(bill.Code, 1)
	if err == nil || !strings.Contains(err.Error(), "checkout_needs_review") || bill.ReasonCode != "checkout_needs_review" || bill.State != "failed_merchant" || signatures != 1 || bill.PorkbunCheckoutID != "chk-1" {
		t.Fatalf("state=%s code=%s checkout=%s sig=%d err=%v", bill.State, bill.ReasonCode, bill.PorkbunCheckoutID, signatures, err)
	}
	if len(keys) != 3 || keys[0] != want || keys[1] != want || keys[2] != want+"-confirm" {
		t.Fatalf("keys=%v", keys)
	}
	if strings.Count(bill.MerchantAttempts, `"n":`) != 1 {
		t.Fatalf("attempts %s", bill.MerchantAttempts)
	}
}

func TestSignedThenExpiredAllowsOneNewAttempt(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	_, payer := testPayer(t)
	var keys []string
	var signatures int
	phase := "fail"
	pork := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if servePorkbunBalance(w, r) {
			return
		}
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		if r.Header.Get("PAYMENT-SIGNATURE") != "" {
			signatures++
			if phase != "fail" && phase != "new" {
				t.Errorf("signed during %s", phase)
			}
			if phase == "new" {
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, `{"status":"SUCCESS","orderId":"ord-2","checkoutId":"chk-2"}`)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"status":"ERROR","message":"upstream"}`)
			return
		}
		if phase == "expired" {
			if !strings.Contains(body, `"usdcCheckoutId":"chk-1"`) {
				t.Errorf("poll body %s", body)
			}
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"status":"ERROR","code":"PAYMENT_EXPIRED","message":"checkout expired","checkoutId":"chk-1"}`)
			return
		}
		w.Header().Set("PAYMENT-REQUIRED", exactOfferHeader())
		w.WriteHeader(http.StatusPaymentRequired)
		checkout := "chk-1"
		if phase == "new" {
			checkout = "chk-2"
		}
		_, _ = io.WriteString(w, `{"status":"ERROR","code":"PAYMENT_REQUIRED","checkoutId":"`+checkout+`"}`)
	}))
	defer pork.Close()
	base := balanceServer(t)
	defer base.Close()
	t.Setenv("BASE_RPC_URL", base.URL)
	bill := bridgedBill(t, "bill-lapsed-"+strings.ReplaceAll(t.Name(), "/", "-"))
	deps := merchantDeps(t, pork.URL, payer)
	var vaults, burns int
	deps.vault = countVault(&vaults)
	deps.burn = countBurn(&burns)
	if err := AdvanceBill(context.Background(), bill, deps); err == nil || signatures != 1 || bill.PorkbunCheckoutID != "chk-1" || !strings.Contains(bill.MerchantAttempts, `"signed":true`) {
		t.Fatalf("sign sig=%d checkout=%s err=%v attempts=%s", signatures, bill.PorkbunCheckoutID, err, bill.MerchantAttempts)
	}
	attempts, err := parseMerchantAttempts(bill.MerchantAttempts)
	if err != nil || len(attempts) != 1 || attempts[0].ValidBefore <= time.Now().Unix() || attempts[0].Nonce == "" {
		t.Fatalf("marker %+v err=%v", attempts, err)
	}
	phase = "expired"
	confirm := merchantAttemptKey(bill.Code, 1) + "-confirm"
	if err = AdvanceBill(context.Background(), bill, deps); bill.ReasonCode != "merchant_checkout_expired" || signatures != 1 || bill.PorkbunCheckoutID != "chk-1" || strings.Count(bill.MerchantAttempts, `"n":`) != 1 || keys[len(keys)-1] != confirm {
		t.Fatalf("future expiry code=%s checkout=%s sig=%d keys=%v err=%v attempts=%s", bill.ReasonCode, bill.PorkbunCheckoutID, signatures, keys, err, bill.MerchantAttempts)
	}
	patchValidBefore(t, bill, time.Now().Unix()-30)
	if err = AdvanceBill(context.Background(), bill, deps); bill.ReasonCode != "merchant_checkout_expired" || signatures != 1 || strings.Count(bill.MerchantAttempts, `"n":`) != 1 || !strings.Contains(bill.MerchantAttempts, `"outcome":"expired"`) || keys[len(keys)-1] != confirm {
		t.Fatalf("lapsed expiry code=%s sig=%d keys=%v err=%v attempts=%s", bill.ReasonCode, signatures, err, keys, bill.MerchantAttempts)
	}
	phase = "new"
	if err = AdvanceBill(context.Background(), bill, deps); err != nil || signatures != 2 || vaults != 0 || burns != 0 || bill.State != "done" || bill.PorkbunOrderID != "ord-2" {
		t.Fatalf("new state=%s order=%s sig=%d vaults=%d burns=%d err=%v attempts=%s", bill.State, bill.PorkbunOrderID, signatures, vaults, burns, err, bill.MerchantAttempts)
	}
	if keys[len(keys)-1] != merchantAttemptKey(bill.Code, 2) || strings.Count(bill.MerchantAttempts, `"n":`) != 2 {
		t.Fatalf("keys=%v attempts=%s", keys, bill.MerchantAttempts)
	}
	if bill.ReasonCode != "" || bill.Reason != "" {
		t.Fatalf("stale reason code=%s reason=%s", bill.ReasonCode, bill.Reason)
	}
}

func TestLapsedAuthorizationWithoutPaymentExpiredDoesNotRotate(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	_, payer := testPayer(t)
	var signatures int
	var keys []string
	pork := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if servePorkbunBalance(w, r) {
			return
		}
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		raw, _ := io.ReadAll(r.Body)
		if r.Header.Get("PAYMENT-SIGNATURE") != "" {
			signatures++
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"status":"ERROR","message":"upstream"}`)
			return
		}
		if strings.Contains(string(raw), `"usdcCheckoutId"`) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"status":"ERROR","message":"still settling"}`)
			return
		}
		w.Header().Set("PAYMENT-REQUIRED", exactOfferHeader())
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = io.WriteString(w, `{"status":"ERROR","code":"PAYMENT_REQUIRED","checkoutId":"chk-1"}`)
	}))
	defer pork.Close()
	base := balanceServer(t)
	defer base.Close()
	t.Setenv("BASE_RPC_URL", base.URL)
	bill := bridgedBill(t, "bill-not-expired-"+strings.ReplaceAll(t.Name(), "/", "-"))
	deps := merchantDeps(t, pork.URL, payer)
	if err := AdvanceBill(context.Background(), bill, deps); err == nil || signatures != 1 {
		t.Fatalf("sig=%d err=%v", signatures, err)
	}
	patchValidBefore(t, bill, time.Now().Unix()-30)
	if err := AdvanceBill(context.Background(), bill, deps); err == nil || signatures != 1 || bill.PorkbunCheckoutID != "chk-1" || strings.Count(bill.MerchantAttempts, `"n":`) != 1 {
		t.Fatalf("poll sig=%d checkout=%s err=%v attempts=%s", signatures, bill.PorkbunCheckoutID, err, bill.MerchantAttempts)
	}
	if err := AdvanceBill(context.Background(), bill, deps); err == nil || signatures != 1 || strings.Count(bill.MerchantAttempts, `"outcome":"expired"`) != 0 {
		t.Fatalf("retry sig=%d err=%v attempts=%s keys=%v", signatures, err, bill.MerchantAttempts, keys)
	}
	want := merchantAttemptKey(bill.Code, 1)
	confirm := want + "-confirm"
	if len(keys) != 4 || keys[0] != want || keys[1] != want || keys[2] != confirm || keys[3] != confirm {
		t.Fatalf("keys=%v", keys)
	}
}

func TestExpirySubstringDoesNotOpenANewAttempt(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "checkout_expired", body: `{"status":"ERROR","code":"CHECKOUT_EXPIRED","message":"checkout expired"}`, want: "merchant_checkout_expired"},
		{name: "message", body: `{"status":"ERROR","code":"COST_MISMATCH","message":"quote will EXPIRE tomorrow"}`, want: "merchant_cost_mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bill := bridgedBill(t, "bill-substr-"+strings.ReplaceAll(t.Name(), "/", "-"))
			bill.PorkbunCheckoutID = "chk-old"
			bill.MerchantAttempts = `[{"n":1,"key":"` + merchantAttemptKey(bill.Code, 1) + `","at":"2026-10-08T00:00:00Z","outcome":"pending","checkout":"chk-old"}]`
			if err := models.SaveBill(bill); err != nil {
				t.Fatal(err)
			}
			var keys []string
			var signatures int
			pork := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				keys = append(keys, r.Header.Get("Idempotency-Key"))
				if r.Header.Get("PAYMENT-SIGNATURE") != "" {
					signatures++
				}
				w.WriteHeader(http.StatusConflict)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer pork.Close()
			deps := merchantDeps(t, pork.URL, "")
			if err := AdvanceBill(context.Background(), bill, deps); bill.ReasonCode != tc.want || signatures != 0 || len(keys) != 1 || bill.PorkbunCheckoutID != "chk-old" || strings.Contains(bill.MerchantAttempts, `"outcome":"expired"`) {
				t.Fatalf("first code=%s checkout=%s sig=%d err=%v attempts=%s", bill.ReasonCode, bill.PorkbunCheckoutID, signatures, err, bill.MerchantAttempts)
			}
			if err := AdvanceBill(context.Background(), bill, deps); bill.ReasonCode != tc.want || signatures != 0 || len(keys) != 2 || keys[1] != keys[0] || bill.PorkbunCheckoutID != "chk-old" {
				t.Fatalf("second code=%s keys=%v checkout=%s sig=%d err=%v", bill.ReasonCode, keys, bill.PorkbunCheckoutID, signatures, err)
			}
		})
	}
}

func TestAccountBalanceReadFailureDoesNotSign(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	_, payer := testPayer(t)
	code := "bill-balance-" + strings.ReplaceAll(t.Name(), "/", "-")
	var keys []string
	var signatures int
	var balanceReads int
	pork := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/account/balance") {
			balanceReads++
			w.Header().Set("Content-Type", "application/json")
			if balanceReads == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, `{"status":"ERROR","message":"down"}`)
				return
			}
			_, _ = io.WriteString(w, `{"status":"SUCCESS","balance":4400}`)
			return
		}
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		if r.Header.Get("PAYMENT-SIGNATURE") != "" {
			signatures++
			stored, err := models.FindBill(code)
			if err != nil || stored == nil || !strings.Contains(stored.MerchantAttempts, `"balance_known":true`) || !strings.Contains(stored.MerchantAttempts, `"balance_cents":4400`) || strings.Contains(stored.MerchantAttempts, r.Header.Get("PAYMENT-SIGNATURE")) {
				attempts := ""
				if stored != nil {
					attempts = stored.MerchantAttempts
				}
				t.Errorf("balance missing before signed post attempts=%s err=%v", attempts, err)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"status":"SUCCESS","orderId":"ord-bal","checkoutId":"chk-bal"}`)
			return
		}
		w.Header().Set("PAYMENT-REQUIRED", exactOfferHeader())
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = io.WriteString(w, `{"status":"ERROR","code":"PAYMENT_REQUIRED","checkoutId":"chk-bal"}`)
	}))
	defer pork.Close()
	base := balanceServer(t)
	defer base.Close()
	t.Setenv("BASE_RPC_URL", base.URL)
	bill := bridgedBill(t, code)
	deps := merchantDeps(t, pork.URL, payer)
	err := AdvanceBill(context.Background(), bill, deps)
	want := merchantAttemptKey(bill.Code, 1)
	if err == nil || !strings.Contains(err.Error(), "account balance") || signatures != 0 || len(keys) != 1 || keys[0] != want || strings.Contains(bill.MerchantAttempts, `"signed":true`) || bill.PorkbunCheckoutID != "" {
		t.Fatalf("err=%v sig=%d keys=%v checkout=%s attempts=%s", err, signatures, keys, bill.PorkbunCheckoutID, bill.MerchantAttempts)
	}
	if err = AdvanceBill(context.Background(), bill, deps); err != nil || signatures != 1 || len(keys) != 3 || keys[1] != want || keys[2] != want || strings.Count(bill.MerchantAttempts, `"n":`) != 1 || !strings.Contains(bill.MerchantAttempts, `"signed":true`) || !strings.Contains(bill.MerchantAttempts, `"balance_known":true`) || !strings.Contains(bill.MerchantAttempts, `"balance_cents":4400`) {
		t.Fatalf("retry err=%v sig=%d keys=%v attempts=%s", err, signatures, keys, bill.MerchantAttempts)
	}
}

func exactOfferHeader() string {
	return base64.StdEncoding.EncodeToString([]byte(`{"x402Version":2,"accepts":[{"scheme":"exact","network":"eip155:8453","amount":"8750000","asset":"` + procurement.BaseUSDC + `","payTo":"0x3333333333333333333333333333333333333333","maxTimeoutSeconds":600,"extra":{"name":"USDC","version":"2"}}]}`))
}

func patchValidBefore(t *testing.T, bill *models.Bill, unix int64) {
	t.Helper()
	attempts, err := parseMerchantAttempts(bill.MerchantAttempts)
	if err != nil || len(attempts) == 0 || !attempts[len(attempts)-1].Signed {
		t.Fatalf("attempts %s err=%v", bill.MerchantAttempts, err)
	}
	attempts[len(attempts)-1].ValidBefore = unix
	if err = writeMerchantAttempts(bill, attempts); err != nil {
		t.Fatal(err)
	}
	if err = models.SaveBill(bill); err != nil {
		t.Fatal(err)
	}
}

func bridgedBill(t *testing.T, code string) *models.Bill {
	t.Helper()
	bill := models.NewBill()
	bill.Code = code
	bill.Domain = code + ".xyz"
	bill.Vendor = "porkbun"
	bill.Kind = "domain_register"
	bill.CategoryCode = "domains"
	bill.Years = 1
	bill.QuoteCents = 875
	bill.State = "bridged"
	bill.Action = treasury.ActionPay
	bill.ReasonCode = "within_policy"
	bill.DecisionHash = "0x" + strings.Repeat("11", 32)
	bill.VaultTx = "0x" + strings.Repeat("aa", 32)
	bill.BaseMintTx = "0x" + strings.Repeat("bb", 32)
	if _, err := models.InsertBill(bill); err != nil {
		t.Fatal(err)
	}
	return bill
}

func merchantDeps(t *testing.T, porkbunURL, payer string) billDeps {
	t.Helper()
	deps := generousDeps(t)
	deps.cfg.Mode = "mainnet"
	deps.cfg.Base = treasury.BaseChainConfig{RPCEnv: "BASE_RPC_URL", USDC: procurement.BaseUSDC}
	deps.cfg.Procurement = treasury.ProcurementConfig{KeyEnv: "PROCUREMENT_PRIVATE_KEY", Address: payer}
	client := &procurement.Porkbun{BaseURL: porkbunURL, APIKey: "pk", Secret: "ps", MinInterval: -1}
	deps.merchant = func(ctx context.Context, row *models.Bill) (merchantResult, error) {
		return liveMerchant(ctx, deps.cfg, client, row)
	}
	return deps
}

func balanceServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(scriptedChain("0x2105", map[string]string{
		selectorHex("balanceOf(address)"): word(big.NewInt(20_000_000)),
	}, "0x0", &rpcLog{}))
}

func testPayer(t *testing.T) (string, string) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	raw := hex.EncodeToString(crypto.FromECDSA(key))
	t.Setenv("PROCUREMENT_PRIVATE_KEY", raw)
	return raw, crypto.PubkeyToAddress(key.PublicKey).Hex()
}

func countVault(n *int) func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
	return func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
		*n++
		return vaultResult{}, nil
	}
}

func countBurn(n *int) func(context.Context, *models.Bill, *big.Int, *big.Int) (burnResult, error) {
	return func(context.Context, *models.Bill, *big.Int, *big.Int) (burnResult, error) {
		*n++
		return burnResult{}, nil
	}
}
