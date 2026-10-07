// 本文件组装送给模型的公开上下文。私钥、API key 和 RPC 不在这里。
package planner

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/vincent-lxc/pulse-operator/operator/procurement"
)

// PublicInput 是模型能看到的账单、金库余量和近期付款。
type PublicInput struct {
	Kind       string           `json:"kind"`
	Bill       map[string]any   `json:"bill"`
	Vendor     string           `json:"vendor,omitempty"`
	Vault      map[string]any   `json:"vault"`
	History    []map[string]any `json:"history"`
	Allowed    []string         `json:"allowed_actions"`
	HardAction string           `json:"hard_action"`
	HardReason string           `json:"hard_reason"`
}

// Prompt 返回已经脱敏的 JSON，以及这段 JSON 的 keccak。
func Prompt(in PublicInput) (string, string, error) {
	if in.History == nil {
		in.History = []map[string]any{}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return "", "", err
	}
	text := procurement.Redact(string(raw))
	sum := crypto.Keccak256([]byte(text))
	return text, "0x" + fmt.Sprintf("%x", sum), nil
}

// SystemPrompt 告诉模型它只能在硬规则允许的动作里选择。
func SystemPrompt(allowed []string) string {
	return strings.TrimSpace(`
You are the bill planner for Pulse Operator. Choose exactly one action for this bill.
The deterministic rules and the PolicyVault contract stay in charge. You cannot raise a cap, add a payee, increase a budget, or pick an action outside the allowed list.
Allowed actions: ` + strings.Join(allowed, ", ") + `.
Return only the structured object: action, rationale, risk_notes, confidence.
action is one of pay, defer, escalate, reject.
confidence is a decimal string from 0 to 1.
Do not include secrets, private keys, or API keys.`)
}
