// 本文件调用 Vercel AI Gateway 的 OpenAI 兼容接口，并校验结构化输出。
// 无效、超时或传输错误都返回 error，调用方按失败关闭处理。
package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vincent-lxc/pulse-operator/operator/procurement"
)

// Gateway 是 OpenAI 兼容的聊天补全客户端。
type Gateway struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

// Output 是校验过后的规划。Raw 是模型返回的结构化原文。
type Output struct {
	Action     string
	Rationale  string
	RiskNotes  string
	Confidence string
	Raw        string
	ModelID    string
	PromptHash string
	LatencyMS  int64
}

// Decide 发送提示并解析严格 JSON。任何不合格的响应都是错误。
func (g Gateway) Decide(ctx context.Context, prompt, promptHash string) (Output, error) {
	out := Output{ModelID: g.Model, PromptHash: promptHash}
	if strings.TrimSpace(g.APIKey) == "" {
		return out, fmt.Errorf("planner api key is empty")
	}
	if strings.TrimSpace(g.Model) == "" {
		return out, fmt.Errorf("planner model is empty")
	}
	body, err := json.Marshal(chatRequest{
		Model: g.Model,
		Messages: []chatMessage{
			{Role: "system", Content: SystemPrompt([]string{"pay", "defer", "escalate", "reject"})},
			{Role: "user", Content: prompt},
		},
		ResponseFormat: responseFormat(),
	})
	if err != nil {
		return out, err
	}
	started := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint(), bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+g.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	client := g.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	out.LatencyMS = time.Since(started).Milliseconds()
	if err != nil {
		return out, fmt.Errorf("planner: %s", procurement.Redact(err.Error(), g.APIKey))
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return out, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return out, fmt.Errorf("planner http %d: %s", res.StatusCode, procurement.Redact(string(raw), g.APIKey))
	}
	text, modelID, err := extractContent(raw)
	if err != nil {
		return out, err
	}
	if modelID != "" {
		out.ModelID = modelID
	}
	parsed, err := Parse(text)
	if err != nil {
		return out, err
	}
	out.Action = parsed.Action
	out.Rationale = parsed.Rationale
	out.RiskNotes = parsed.RiskNotes
	out.Confidence = parsed.Confidence
	out.Raw = text
	return out, nil
}

func (g Gateway) endpoint() string {
	base := strings.TrimRight(strings.TrimSpace(g.BaseURL), "/")
	if base == "" {
		base = "https://ai-gateway.vercel.sh/v1"
	}
	return base + "/chat/completions"
}

// Parse 校验模型原文。action 会从 escalate 映射成金库用的 escalate_to_human。
func Parse(text string) (Output, error) {
	text = strings.TrimSpace(text)
	var wire struct {
		Action     string `json:"action"`
		Rationale  string `json:"rationale"`
		RiskNotes  string `json:"risk_notes"`
		Confidence string `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(text), &wire); err != nil {
		return Output{}, fmt.Errorf("planner json: %w", err)
	}
	action := strings.ToLower(strings.TrimSpace(wire.Action))
	switch action {
	case "pay", "defer", "reject":
	case "escalate":
		action = "escalate_to_human"
	default:
		return Output{}, fmt.Errorf("planner action %q", wire.Action)
	}
	if strings.TrimSpace(wire.Rationale) == "" {
		return Output{}, fmt.Errorf("planner rationale is empty")
	}
	conf := strings.TrimSpace(wire.Confidence)
	n, err := strconv.ParseFloat(conf, 64)
	if err != nil || n < 0 || n > 1 {
		return Output{}, fmt.Errorf("planner confidence %q", wire.Confidence)
	}
	return Output{
		Action: action, Rationale: strings.TrimSpace(wire.Rationale),
		RiskNotes: strings.TrimSpace(wire.RiskNotes), Confidence: conf, Raw: text,
	}, nil
}

func responseFormat() map[string]any {
	return map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   "bill_decision",
			"strict": true,
			"schema": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"action":     map[string]any{"type": "string", "enum": []string{"pay", "defer", "escalate", "reject"}},
					"rationale":  map[string]any{"type": "string"},
					"risk_notes": map[string]any{"type": "string"},
					"confidence": map[string]any{"type": "string"},
				},
				"required": []string{"action", "rationale", "risk_notes", "confidence"},
			},
		},
	}
}

type chatRequest struct {
	Model          string         `json:"model"`
	Messages       []chatMessage  `json:"messages"`
	ResponseFormat map[string]any `json:"response_format"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func extractContent(raw []byte) (string, string, error) {
	var body struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", "", fmt.Errorf("planner response: %w", err)
	}
	if len(body.Choices) == 0 {
		return "", "", fmt.Errorf("planner response has no choices")
	}
	msg := body.Choices[0].Message
	for _, call := range msg.ToolCalls {
		if strings.TrimSpace(call.Function.Arguments) != "" {
			return call.Function.Arguments, body.Model, nil
		}
	}
	if strings.TrimSpace(msg.Content) == "" {
		return "", "", fmt.Errorf("planner response is empty")
	}
	return msg.Content, body.Model, nil
}
