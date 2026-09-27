package jev

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vincent-lxc/pulse-on-monad/agent/internal/outcome"
	"github.com/vincent-lxc/pulse-on-monad/agent/internal/risk"
)

func sampleIntent() risk.Intent {
	return risk.Intent{Symbol: "BTC", Action: "buy", SizeUSD: 100, Rule: "momentum", Reason: "change_24h=2.10"}
}

func sampleRecent() []outcome.Outcome {
	return []outcome.Outcome{{
		RunID:  "r1",
		Kind:   outcome.KindPnL,
		PnL:    1.2,
		Label:  outcome.LabelPositive,
		Reason: "sim",
	}}
}

func TestStubWhenNoKey(t *testing.T) {
	g := &SoftGate{}
	v := g.Evaluate(context.Background(), sampleIntent(), sampleRecent())
	if !v.Pass || v.Enabled || v.Source != "stub" {
		t.Fatalf("%+v", v)
	}
	if !strings.Contains(v.Prompt, "run=r1") || !strings.Contains(v.Prompt, "label=positive") {
		t.Fatalf("state missing outcomes:\n%s", v.Prompt)
	}
	if strings.Contains(v.Reason, "sk-") || v.ModelID != "jev-stub" {
		t.Fatalf("bad stub verdict %+v", v)
	}
}

func TestNewFromEnvPrefersTypeSafeKey(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "ts-key")
	t.Setenv("JEV_API_KEY", "jev-alias")
	g := NewFromEnv()
	if g.APIKey != "ts-key" {
		t.Fatalf("key %q", g.APIKey)
	}
	t.Setenv("TYPESAFE_API_KEY", "")
	g = NewFromEnv()
	if g.APIKey != "jev-alias" {
		t.Fatalf("alias %q", g.APIKey)
	}
}

func mockSystemOne(t *testing.T, status int, body string, check func(*http.Request, []byte)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		raw, _ := io.ReadAll(r.Body)
		if check != nil {
			check(r, raw)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestLiveAllowPasses(t *testing.T) {
	const key = "test-secret-key-do-not-leak"
	srv := mockSystemOne(t, 200, `{
		"model": "jev-1.13.0",
		"answers": {
			"allow_action": {"type": "noul", "noul": 0.91},
			"confidence": {"type": "score", "score": 3.2, "confidence": 0.8}
		},
		"usage": {"input_tokens": 40, "output_tokens": 8}
	}`, func(r *http.Request, raw []byte) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+key {
			t.Errorf("auth %q", got)
		}
		var req APIRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatal(err)
		}
		if req.Model != "jev-latest" {
			t.Errorf("model %s", req.Model)
		}
		if req.Questions[QuestionAllow].Type != "noul" || req.Questions[QuestionConf].Type != "score" {
			t.Errorf("questions %+v", req.Questions)
		}
		if !strings.Contains(req.State, "action=buy") || !strings.Contains(req.State, "run=r1") {
			t.Errorf("state %s", req.State)
		}
		if strings.Contains(string(raw), key) {
			t.Error("request body leaked API key")
		}
	})
	defer srv.Close()

	g := &SoftGate{Endpoint: srv.URL, APIKey: key, HTTP: srv.Client()}
	v := g.Evaluate(context.Background(), sampleIntent(), sampleRecent())
	if !v.Pass || !v.Enabled || v.Source != "typesafe" || v.ModelID != "jev-1.13.0" {
		t.Fatalf("%+v", v)
	}
	if v.AllowNoul == nil || *v.AllowNoul < 0.9 {
		t.Fatalf("noul %+v", v.AllowNoul)
	}
	blob, _ := json.Marshal(v)
	if strings.Contains(string(blob), key) {
		t.Fatalf("verdict leaked key: %s", blob)
	}
}

func TestLiveLowNoulBlocks(t *testing.T) {
	srv := mockSystemOne(t, 200, `{
		"model": "jev-latest",
		"answers": {"allow_action": {"type": "noul", "noul": 0.12}}
	}`, nil)
	defer srv.Close()
	g := &SoftGate{Endpoint: srv.URL, APIKey: "k", HTTP: srv.Client()}
	v := g.Evaluate(context.Background(), sampleIntent(), nil)
	if v.Pass {
		t.Fatal("low noul must block")
	}
	if !strings.Contains(v.Reason, "block") {
		t.Fatalf("reason %s", v.Reason)
	}
}

func TestLiveHTTPErrorFailClosedNoKeyLeak(t *testing.T) {
	const key = "super-secret-jev-key"
	srv := mockSystemOne(t, 401, `unauthorized `+key, nil)
	defer srv.Close()
	g := &SoftGate{Endpoint: srv.URL, APIKey: key, HTTP: srv.Client()}
	v := g.Evaluate(context.Background(), sampleIntent(), nil)
	if v.Pass {
		t.Fatal("HTTP error must fail-closed")
	}
	if strings.Contains(v.Reason, key) {
		t.Fatalf("reason leaked key: %s", v.Reason)
	}
	if !strings.Contains(v.Reason, "jev_http_error") {
		t.Fatalf("reason %s", v.Reason)
	}
}

func TestCallRequiresKey(t *testing.T) {
	_, err := (&SoftGate{}).Call(context.Background(), "s", PulseQuestions())
	if err == nil {
		t.Fatal("expected error")
	}
}
