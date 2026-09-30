package payment

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocalClientRejectsWrongChainBeforeSending(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"0x1"}`)
	}))
	defer server.Close()
	if _, err := DialLocal(context.Background(), server.URL, 0); err == nil {
		t.Fatal("mainnet chain accepted")
	}
	if calls != 1 {
		t.Fatalf("unexpected RPCs after wrong chain: %d", calls)
	}
}
func TestLocalRPCDoesNotFollowRedirects(t *testing.T) {
	redirected := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected++ }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	if _, err := DialLocal(context.Background(), server.URL, 0); err == nil {
		t.Fatal("redirect accepted")
	}
	if redirected != 0 {
		t.Fatal("RPC redirect reached destination")
	}
}
