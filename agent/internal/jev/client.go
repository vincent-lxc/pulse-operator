package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/vincent-lxc/pulse-on-monad/agent/internal/outcome"
	"github.com/vincent-lxc/pulse-on-monad/agent/internal/risk"
)

const (
	DefaultEndpoint  = "https://api.typesafe.ai/v1/systemone"
	DefaultModel     = "jev-latest"
	DefaultThreshold = 0.50
	QuestionAllow    = "allow_action"
	QuestionConf     = "confidence"
)

// SoftGate is the optional TypeSafe Jev soft gate. Hard risk stays in Go
// and must run first. Without an API key this is a CI-safe stub.
type SoftGate struct {
	Endpoint  string
	APIKey    string
	Model     string
	Threshold float64
	HTTP      *http.Client
}

type Question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

type APIRequest struct {
	Model     string              `json:"model"`
	State     string              `json:"state"`
	Questions map[string]Question `json:"questions"`
}

type APIResponse struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   *Usage            `json:"usage,omitempty"`
}

type Answer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Verdict is what the pipeline and audit store. Never includes the API key.
type Verdict struct {
	Pass             bool                `json:"pass"`
	Reason           string              `json:"reason"`
	Prompt           string              `json:"state"`
	Enabled          bool                `json:"enabled"`
	ModelID          string              `json:"model_id"`
	Source           string              `json:"source"` // stub | typesafe
	AllowNoul        *float64            `json:"allow_noul,omitempty"`
	ConfidenceScore  *float64            `json:"confidence_score,omitempty"`
	Questions        map[string]Question `json:"questions,omitempty"`
	Answers          map[string]Answer   `json:"answers,omitempty"`
}

func NewFromEnv() *SoftGate {
	key := firstEnv("TYPESAFE_API_KEY", "JEV_API_KEY")
	g := &SoftGate{
		Endpoint:  strings.TrimSpace(os.Getenv("TYPESAFE_JEV_URL")),
		APIKey:    key,
		Model:     strings.TrimSpace(os.Getenv("TYPESAFE_JEV_MODEL")),
		Threshold: DefaultThreshold,
	}
	if v := strings.TrimSpace(os.Getenv("TYPESAFE_JEV_THRESHOLD")); v != "" {
		var f float64
		if _, err := fmt.Sscanf(v, "%f", &f); err == nil && f >= 0 && f <= 1 {
			g.Threshold = f
		}
	}
	return g
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func (g *SoftGate) Enabled() bool { return g != nil && strings.TrimSpace(g.APIKey) != "" }

func (g *SoftGate) endpoint() string {
	if g != nil && strings.TrimSpace(g.Endpoint) != "" {
		return strings.TrimRight(g.Endpoint, "/")
	}
	return DefaultEndpoint
}

func (g *SoftGate) model() string {
	if g != nil && strings.TrimSpace(g.Model) != "" {
		return g.Model
	}
	return DefaultModel
}

func (g *SoftGate) threshold() float64 {
	if g == nil || g.Threshold <= 0 {
		return DefaultThreshold
	}
	return g.Threshold
}

func (g *SoftGate) http() *http.Client {
	if g != nil && g.HTTP != nil {
		return g.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

// PulseQuestions is the soft-gate set: noul allow_action + optional score.
func PulseQuestions() map[string]Question {
	return map[string]Question{
		QuestionAllow: {
			Type:         "noul",
			Instructions: "This proposed Pulse trade action should be allowed to proceed after hard risk already passed.",
			Criteria: map[string]string{
				"true":  "The action is coherent with recent outcomes and not reckless.",
				"false": "The action looks poorly timed, contradicts recent losses, or should be held.",
			},
		},
		QuestionConf: {
			Type:         "score",
			Instructions: "How confident should Pulse be in taking this action?",
			Criteria:     []string{"Do not take it", "Weak / marginal", "Reasonable", "Strong", "Very strong"},
		},
	}
}

// OutcomeSummary is a compact recap injected into the next soft-gate state.
func OutcomeSummary(recent []outcome.Outcome) string {
	if len(recent) == 0 {
		return "no prior outcomes"
	}
	var b strings.Builder
	b.WriteString("recent outcomes (oldest→newest):\n")
	for _, o := range recent {
		fmt.Fprintf(&b, "- run=%s label=%s kind=%s pnl=%.4f gate=%s reason=%s\n",
			o.RunID, o.Label, o.Kind, o.PnL, o.Gate, o.Reason)
	}
	return strings.TrimSpace(b.String())
}

// BuildState is the Jev `state` string (intent + recent outcomes).
func BuildState(in risk.Intent, recent []outcome.Outcome) string {
	return fmt.Sprintf(
		"Pulse trade intent after hard-risk code gate.\naction=%s symbol=%s size_usd=%.2f rule=%s\nplanner_reason=%s\n\n%s",
		in.Action, in.Symbol, in.SizeUSD, in.Rule, in.Reason, OutcomeSummary(recent),
	)
}

// Evaluate stubs when no key; otherwise POSTs systemone. Never logs the key.
func (g *SoftGate) Evaluate(ctx context.Context, in risk.Intent, recent []outcome.Outcome) Verdict {
	state := BuildState(in, recent)
	qs := PulseQuestions()
	if !g.Enabled() {
		return Verdict{
			Pass:      true,
			Reason:    "jev_disabled_stub",
			Prompt:    state,
			Enabled:   false,
			ModelID:   "jev-stub",
			Source:    "stub",
			Questions: qs,
		}
	}
	resp, err := g.Call(ctx, state, qs)
	if err != nil {
		return Verdict{
			Pass:      false,
			Reason:    redact(fmt.Sprintf("jev_http_error: %v", err), g.APIKey),
			Prompt:    state,
			Enabled:   true,
			ModelID:   g.model(),
			Source:    "typesafe",
			Questions: qs,
		}
	}
	return decide(resp, state, qs, g.threshold(), g.APIKey)
}

func decide(resp *APIResponse, state string, qs map[string]Question, threshold float64, key string) Verdict {
	v := Verdict{
		Prompt:    state,
		Enabled:   true,
		ModelID:   resp.Model,
		Source:    "typesafe",
		Questions: qs,
		Answers:   resp.Answers,
	}
	if v.ModelID == "" {
		v.ModelID = DefaultModel
	}
	ans, ok := resp.Answers[QuestionAllow]
	if !ok || ans.Noul == nil {
		v.Pass = false
		v.Reason = "jev_missing_allow_action"
		return v
	}
	noul := *ans.Noul
	v.AllowNoul = &noul
	if conf, ok := resp.Answers[QuestionConf]; ok && conf.Score != nil {
		s := *conf.Score
		v.ConfidenceScore = &s
	}
	v.Pass = noul >= threshold
	v.Reason = redact(fmt.Sprintf("allow_action noul=%.3f threshold=%.2f", noul, threshold), key)
	if v.ConfidenceScore != nil {
		v.Reason += fmt.Sprintf(" confidence=%.2f", *v.ConfidenceScore)
	}
	if !v.Pass {
		v.Reason += " → block"
	}
	return v
}

// Call POSTs /v1/systemone. The Authorization header is never returned.
func (g *SoftGate) Call(ctx context.Context, state string, questions map[string]Question) (*APIResponse, error) {
	if !g.Enabled() {
		return nil, fmt.Errorf("jev: no API key")
	}
	payload := APIRequest{Model: g.model(), State: state, Questions: questions}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+g.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := g.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("jev: request: %s", redact(err.Error(), g.APIKey))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	raw = []byte(redact(string(raw), g.APIKey))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("jev: HTTP %d: %s", resp.StatusCode, trimBody(raw))
	}
	var out APIResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("jev: decode: %w", err)
	}
	return &out, nil
}

func redact(s, key string) string {
	if key == "" || !strings.Contains(s, key) {
		return s
	}
	return strings.ReplaceAll(s, key, "[redacted]")
}

// AuditSafe is the step output: question I/O summary, no secrets.
func (v Verdict) AuditSafe() map[string]any {
	return map[string]any{
		"pass":              v.Pass,
		"reason":            v.Reason,
		"model_id":          v.ModelID,
		"source":            v.Source,
		"allow_noul":        v.AllowNoul,
		"confidence_score":  v.ConfidenceScore,
		"questions":         questionSummary(v.Questions),
		"answers":           answerSummary(v.Answers),
	}
}

func questionSummary(qs map[string]Question) map[string]string {
	out := map[string]string{}
	for id, q := range qs {
		out[id] = q.Type
	}
	return out
}

func answerSummary(ans map[string]Answer) map[string]any {
	out := map[string]any{}
	for id, a := range ans {
		item := map[string]any{"type": a.Type}
		if a.Noul != nil {
			item["noul"] = *a.Noul
		}
		if a.Score != nil {
			item["score"] = *a.Score
		}
		if a.Choice != "" {
			item["choice"] = a.Choice
		}
		out[id] = item
	}
	return out
}

func trimBody(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 240 {
		return s[:240] + "…"
	}
	return s
}
