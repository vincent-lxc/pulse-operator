package treasury

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRetryTransportHonors429(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()
	client := &http.Client{Transport: &retryTransport{base: srv.Client().Transport}}
	res, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || string(body) != "ok" || hits.Load() != 3 {
		t.Fatalf("status %d body %s hits %d", res.StatusCode, body, hits.Load())
	}
}

func TestScanFromUsesLookback(t *testing.T) {
	c := &LiveChain{fromBlock: 100, lookback: 10}
	if got := c.scanFrom(1000); got != 991 {
		t.Fatal(got)
	}
	c.fullScan = true
	if got := c.scanFrom(1000); got != 100 {
		t.Fatal(got)
	}
}

func TestTelegramSendsRequestID(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		if !strings.Contains(r.URL.Path, "/botsecret/sendMessage") {
			t.Errorf("path %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()
	n := TelegramNotifier{Token: "secret", ChatID: "42", HTTP: srv.Client(), API: srv.URL}
	err := n.Notify(t.Context(), Notice{Kind: "escalation", Text: "request=7 https://testnet.arcscan.app/tx/0xabc"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "request=7") || !strings.Contains(body, `"chat_id":"42"`) {
		t.Fatal(body)
	}
	if err := (TelegramNotifier{}).Notify(t.Context(), Notice{Text: "x"}); err != nil {
		t.Fatal(err)
	}
}
