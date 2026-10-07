// 本文件是 TypeSafe Jev 的软复核。传输失败只记录，不推翻已经通过硬规则的付款。
// 明确的低分才会把 pay 降成 escalate。
package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vincent-lxc/pulse-operator/operator/procurement"
)

// Jev 调用 systemone。没有密钥时 Review 直接跳过。
type Jev struct {
	Endpoint  string
	APIKey    string
	Model     string
	Threshold float64
	HTTP      *http.Client
}

// Review 返回是否明确拒绝。transportErr 非空时调用方不得因此取消付款。
func (j Jev) Review(ctx context.Context, state string) (deny bool, note string, transportErr error) {
	if strings.TrimSpace(j.APIKey) == "" {
		return false, "jev_skipped", nil
	}
	threshold := j.Threshold
	if threshold <= 0 {
		threshold = 0.50
	}
	model := j.Model
	if model == "" {
		model = "jev-latest"
	}
	endpoint := strings.TrimRight(strings.TrimSpace(j.Endpoint), "/")
	if endpoint == "" {
		endpoint = "https://api.typesafe.ai/v1/systemone"
	}
	payload := map[string]any{
		"model": model,
		"state": state,
		"questions": map[string]any{
			"allow_action": map[string]any{
				"type":         "noul",
				"instructions": "This bill payment already passed the deterministic policy. Score whether it should proceed.",
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return false, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Authorization", "Bearer "+j.APIKey)
	req.Header.Set("Content-Type", "application/json")
	client := j.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("jev: %s", procurement.Redact(err.Error(), j.APIKey))
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return false, "", fmt.Errorf("jev http %d: %s", res.StatusCode, procurement.Redact(string(raw), j.APIKey))
	}
	var parsed struct {
		Answers map[string]struct {
			Noul *float64 `json:"noul"`
		} `json:"answers"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return false, "", fmt.Errorf("jev json: %w", err)
	}
	ans, ok := parsed.Answers["allow_action"]
	if !ok || ans.Noul == nil {
		return false, "jev_missing_allow_action", nil
	}
	if *ans.Noul < threshold {
		return true, fmt.Sprintf("jev_denied noul=%.3f threshold=%.2f", *ans.Noul, threshold), nil
	}
	return false, fmt.Sprintf("jev_pass noul=%.3f", *ans.Noul), nil
}
