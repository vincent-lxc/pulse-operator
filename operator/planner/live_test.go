//go:build integration

package planner

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestGatewayLive 只在显式打开时打一次真实网关。默认测试和 CI 不包含这个文件。
func TestGatewayLive(t *testing.T) {
	key := os.Getenv("AI_GATEWAY_API_KEY")
	if key == "" || os.Getenv("CONFIRM_LLM") != "1" {
		t.Skip("set AI_GATEWAY_API_KEY and CONFIRM_LLM=1 to call the gateway")
	}
	model := os.Getenv("AI_GATEWAY_MODEL")
	if model == "" {
		model = "openai/gpt-5.4-nano"
	}
	base := os.Getenv("AI_GATEWAY_BASE_URL")
	if base == "" {
		base = "https://ai-gateway.vercel.sh/v1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := (Gateway{BaseURL: base, APIKey: key, Model: model}).Decide(ctx, `{"kind":"bill","bill":{"domain":"example.xyz","quote_cents":204},"allowed_actions":["pay","defer","escalate","reject"],"hard_action":"pay","hard_reason":"within_policy","vault":{"remaining":"30"},"history":[]}`, "0xabc")
	if err != nil {
		t.Fatal(err)
	}
	if out.Rationale == "" || out.Action == "" {
		t.Fatalf("%+v", out)
	}
}
