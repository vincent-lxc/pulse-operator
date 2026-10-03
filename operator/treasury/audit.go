// 本文件把观察、决策、交易哈希和结果追加到 JSONL，不改历史行。
package treasury

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AuditEvent 是审计日志中的一行。
type AuditEvent struct {
	RunID        string          `json:"run_id"`
	TS           int64           `json:"ts"`
	Kind         string          `json:"kind"`
	DecisionHash string          `json:"decision_hash,omitempty"`
	TxHash       string          `json:"tx_hash,omitempty"`
	Outcome      string          `json:"outcome,omitempty"`
	Circle       string          `json:"circle,omitempty"`
	Payload      json.RawMessage `json:"payload"`
}

// AuditLog 是追加写的 JSONL 文件。
type AuditLog struct {
	path string
	mu   sync.Mutex
}

// OpenAudit 创建或打开审计文件，目录权限 0755，文件权限 0644。
func OpenAudit(path string) (*AuditLog, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return &AuditLog{path: path}, nil
}

// Append 追加一行 JSON。
func (a *AuditLog) Append(ev AuditEvent) error {
	if ev.TS == 0 {
		ev.TS = time.Now().UTC().Unix()
	}
	if len(ev.Payload) == 0 {
		ev.Payload = json.RawMessage(`{}`)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	f, err := os.OpenFile(a.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	return enc.Encode(ev)
}

// Path 返回日志路径。
func (a *AuditLog) Path() string { return a.path }
