package procurement

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPorkbunCreatePaysOnceAndKeepsIdempotency(t *testing.T) {
	var keys []string
	var signed int
	required := base64.StdEncoding.EncodeToString([]byte(`{"x402Version":2,"accepts":[{"scheme":"exact","network":"eip155:84532","amount":"2040000","asset":"0x036CbD53842c5426634e7929541eC2318f3dCF7e","payTo":"0x1111111111111111111111111111111111111111","maxTimeoutSeconds":300,"extra":{"name":"USDC","version":"2"}}]}`))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "secret-value") && strings.Contains(r.URL.Path, "nope") {
			t.Errorf("unexpected path")
		}
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		if r.Header.Get("PAYMENT-SIGNATURE") == "" {
			w.Header().Set("PAYMENT-REQUIRED", required)
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write([]byte(`{"status":"ERROR","code":"PAYMENT_REQUIRED","checkoutId":"chk-1","x402Url":"https://pay.example/x","message":"sign","next_action":"pay"}`))
			return
		}
		signed++
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		if payload["usdcCheckoutId"] != "chk-1" && payload["payWith"] != "usdc" {
			t.Errorf("retry body %#v", payload)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"SUCCESS","orderId":"ord-9"}`))
	}))
	defer srv.Close()
	client := &Porkbun{BaseURL: srv.URL, APIKey: "key", Secret: "secret-value", MinInterval: -1}
	order, err := Collect(context.Background(), client, CollectInput{
		Domain: "pulse.xyz", Kind: "domain_register", CostCents: 204, Years: 1,
		Idempotency: "bill-pulse-xyz", Network: "eip155:84532",
		Asset: "0x036CbD53842c5426634e7929541eC2318f3dCF7e", Now: time.Unix(1_700_000_000, 0),
		Sign: func(item Accept, now time.Time) (Payment, error) {
			if item.Network != "eip155:84532" {
				t.Fatalf("network %s", item.Network)
			}
			return Payment{Header: "signed-header"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.OrderID != "ord-9" || signed != 1 || len(keys) != 2 || keys[0] != "bill-pulse-xyz" || keys[1] != keys[0] {
		t.Fatalf("order=%+v signed=%d keys=%v", order, signed, keys)
	}
}

func TestPorkbunInProgressDoesNotSignAgain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"PAYMENT_IN_PROGRESS","message":"wait","next_action":"poll"}`))
	}))
	defer srv.Close()
	client := &Porkbun{BaseURL: srv.URL, MinInterval: -1}
	order, err := Collect(context.Background(), client, CollectInput{
		Domain: "pulse.xyz", CostCents: 204, CheckoutID: "chk-1",
		Sign: func(Accept, time.Time) (Payment, error) {
			t.Fatal("signed during PAYMENT_IN_PROGRESS")
			return Payment{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.Code != "PAYMENT_IN_PROGRESS" {
		t.Fatalf("%+v", order)
	}
}

func TestStoredCheckoutDoesNotSignWhenPaymentRequiredAgain(t *testing.T) {
	var calls, signed int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("PAYMENT-SIGNATURE") != "" {
			signed++
		}
		if r.Header.Get("Idempotency-Key") != "bill-pulse-xyz" {
			t.Errorf("idempotency %q", r.Header.Get("Idempotency-Key"))
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"usdcCheckoutId":"chk-1"`) {
			t.Errorf("body %s", body)
		}
		w.Header().Set("PAYMENT-REQUIRED", "again")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"status":"ERROR","code":"PAYMENT_REQUIRED","checkoutId":"chk-1"}`))
	}))
	defer srv.Close()
	client := &Porkbun{BaseURL: srv.URL, MinInterval: -1}
	order, err := Collect(context.Background(), client, CollectInput{
		Domain: "pulse.xyz", Kind: "domain_register", CostCents: 204, CheckoutID: "chk-1",
		Idempotency: "bill-pulse-xyz",
		Sign: func(Accept, time.Time) (Payment, error) {
			t.Fatal("signed a second x402 payload")
			return Payment{}, nil
		},
	})
	if !errors.Is(err, ErrCheckoutNeedsReview) || order.CheckoutID != "chk-1" || calls != 1 || signed != 0 {
		t.Fatalf("err %v order %+v calls %d signed %d", err, order, calls, signed)
	}
}

func TestPorkbunRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"SUCCESS"}`))
	}))
	defer srv.Close()
	client := &Porkbun{BaseURL: srv.URL, MinInterval: 40 * time.Millisecond}
	start := time.Now()
	if _, err := client.CheckDomain(context.Background(), "pulse.xyz"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CheckDomain(context.Background(), "pulse.xyz"); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Fatal("second call ignored the interval")
	}
}

func TestPorkbunErrorRedactsSecret(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"VERIFICATION_REQUIRED","message":"secret-value rejected","next_action":"verify"}`))
	}))
	defer srv.Close()
	client := &Porkbun{BaseURL: srv.URL, APIKey: "secret-value", Secret: "secret-value", MinInterval: -1}
	_, err := client.Create(context.Background(), "pulse.dev", 875, 1, false, "", "bill-1", "")
	if err == nil || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(err.Error(), "VERIFICATION_REQUIRED") {
		t.Fatalf("%v", err)
	}
}

func TestPorkbunDryRunQuote(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"dryRun":true`) || !strings.Contains(string(body), `"agreeToTerms":"yes"`) {
			t.Fatalf("body %s", body)
		}
		_, _ = w.Write([]byte(`{"status":"SUCCESS","cost":875}`))
	}))
	defer srv.Close()
	client := &Porkbun{BaseURL: srv.URL, MinInterval: -1}
	order, err := client.Create(context.Background(), "pulse.dev", 0, 1, true, "", "", "")
	if err != nil || order.CostCents != 875 {
		t.Fatalf("%v %+v", err, order)
	}
}

func TestAgreeToTermsOnQuoteAndPurchase(t *testing.T) {
	cases := []struct {
		name     string
		renew    bool
		dry      bool
		checkout string
		cost     int64
	}{
		{name: "create-quote", dry: true},
		{name: "create-buy", cost: 204},
		{name: "create-checkout", checkout: "chk-9", cost: 204},
		{name: "renew-quote", renew: true, dry: true},
		{name: "renew-buy", renew: true, cost: 204},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				got = string(raw)
				_, _ = w.Write([]byte(`{"status":"SUCCESS","cost":204}`))
			}))
			defer srv.Close()
			client := &Porkbun{BaseURL: srv.URL, APIKey: "pk1_test", Secret: "sk1_test", MinInterval: -1}
			var err error
			if tc.renew {
				_, err = client.Renew(context.Background(), "pulse.dev", tc.cost, 1, tc.dry, tc.checkout, "bill-1", "")
			} else {
				_, err = client.Create(context.Background(), "pulse.dev", tc.cost, 1, tc.dry, tc.checkout, "bill-1", "")
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(got, `"agreeToTerms":"yes"`) {
				t.Fatalf("missing agreeToTerms: %s", got)
			}
			if tc.dry {
				if !strings.Contains(got, `"dryRun":true`) || !strings.Contains(got, `"cost":0`) {
					t.Fatalf("quote body %s", got)
				}
			} else if strings.Contains(got, `"dryRun"`) || !strings.Contains(got, fmt.Sprintf(`"cost":%d`, tc.cost)) {
				t.Fatalf("purchase body %s", got)
			}
			if tc.checkout != "" && !strings.Contains(got, `"usdcCheckoutId":"`+tc.checkout+`"`) {
				t.Fatalf("checkout body %s", got)
			}
			if tc.checkout == "" && !tc.dry && !strings.Contains(got, `"payWith":"usdc"`) {
				t.Fatalf("payWith body %s", got)
			}
		})
	}
}
