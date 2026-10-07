package planner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGatewayParsesStrictJSON(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("auth header missing")
		}
		body, _ := io.ReadAll(r.Body)
		seen = string(body)
		if strings.Contains(seen, "sk-test-key") {
			t.Errorf("request echoed the api key")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "openai/gpt-5.4-nano",
			"choices": []any{map[string]any{"message": map[string]any{
				"content": `{"action":"defer","rationale":"wait for the renewal window","risk_notes":"none","confidence":"0.7"}`,
			}}},
		})
	}))
	defer srv.Close()
	out, err := (Gateway{BaseURL: srv.URL, APIKey: "sk-test-key", Model: "openai/gpt-5.4-nano"}).Decide(context.Background(), `{"bill":"pulse.dev"}`, "0xabc")
	if err != nil {
		t.Fatal(err)
	}
	if out.Action != "defer" || out.ModelID != "openai/gpt-5.4-nano" || out.PromptHash != "0xabc" || out.LatencyMS < 0 {
		t.Fatalf("%+v", out)
	}
	if !strings.Contains(seen, "bill_decision") || !strings.Contains(seen, "json_schema") {
		t.Fatalf("schema missing: %s", seen)
	}
}

func TestGatewayAcceptsToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "openai/gpt-5.4-nano",
			"choices": []any{map[string]any{"message": map[string]any{
				"tool_calls": []any{map[string]any{"function": map[string]any{
					"name":      "bill_decision",
					"arguments": `{"action":"escalate","rationale":"price moved","risk_notes":"drift","confidence":"0.4"}`,
				}}},
			}}},
		})
	}))
	defer srv.Close()
	out, err := (Gateway{BaseURL: srv.URL, APIKey: "k", Model: "m"}).Decide(context.Background(), "{}", "0x1")
	if err != nil || out.Action != "escalate_to_human" {
		t.Fatalf("%v %+v", err, out)
	}
}

func TestGatewayRejectsInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"not-json"}}]}`))
	}))
	defer srv.Close()
	_, err := (Gateway{BaseURL: srv.URL, APIKey: "k", Model: "m"}).Decide(context.Background(), "{}", "0x1")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGatewayTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := (Gateway{BaseURL: srv.URL, APIKey: "k", Model: "m", HTTP: srv.Client()}).Decide(ctx, "{}", "0x1")
	if err == nil {
		t.Fatal("expected timeout")
	}
}

func TestPromptDropsPrivateKey(t *testing.T) {
	const key = "0x0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	text, hash, err := Prompt(PublicInput{
		Kind: "bill", Bill: map[string]any{"memo": "leak " + key},
		Allowed: []string{"pay", "defer"}, HardAction: "pay",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, key) || strings.Contains(text, "0123456789abcdef") {
		t.Fatalf("prompt leaked key: %s", text)
	}
	if !strings.HasPrefix(hash, "0x") || len(hash) != 66 {
		t.Fatalf("hash %s", hash)
	}
}

func TestJevDenyAndTransport(t *testing.T) {
	denySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		noul := 0.1
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{"allow_action": map[string]any{"noul": noul}}})
	}))
	defer denySrv.Close()
	deny, note, err := (Jev{Endpoint: denySrv.URL, APIKey: "jev-secret", Model: "jev-latest"}).Review(context.Background(), "pay the domain")
	if err != nil || !deny || !strings.Contains(note, "jev_denied") {
		t.Fatalf("deny=%v note=%s err=%v", deny, note, err)
	}
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "jev-secret boom", http.StatusBadGateway)
	}))
	defer down.Close()
	deny, _, err = (Jev{Endpoint: down.URL, APIKey: "jev-secret"}).Review(context.Background(), "pay")
	if err == nil || deny || strings.Contains(err.Error(), "jev-secret") {
		t.Fatalf("deny=%v err=%v", deny, err)
	}
	deny, note, err = (Jev{}).Review(context.Background(), "pay")
	if err != nil || deny || note != "jev_skipped" {
		t.Fatalf("skip deny=%v note=%s err=%v", deny, note, err)
	}
}
