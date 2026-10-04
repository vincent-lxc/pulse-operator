// 本文件把升级通知发给日志或 Telegram。没有 bot token 时不发送。
package treasury

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Notifier 把升级和划转告诉人。实现不得抛出密钥。
type Notifier interface {
	Notify(ctx context.Context, n Notice) error
}

// LogNotifier 把通知留在报告里，不访问外部网络。
type LogNotifier struct {
	Items []Notice
}

// Notify 追加一条通知。
func (n *LogNotifier) Notify(_ context.Context, item Notice) error {
	n.Items = append(n.Items, item)
	return nil
}

// NopNotifier 在通知通道没有配置时什么都不做。
type NopNotifier struct{}

// Notify 直接返回。
func (NopNotifier) Notify(context.Context, Notice) error { return nil }

// TelegramNotifier 调用 Bot API sendMessage。Token 和 ChatID 由调用方从环境或 0600 文件读入。
type TelegramNotifier struct {
	Token  string
	ChatID string
	HTTP   *http.Client
	API    string
}

// Notify 发送一条消息。缺少 token 或 chat 时不发送。
func (t TelegramNotifier) Notify(ctx context.Context, n Notice) error {
	if strings.TrimSpace(t.Token) == "" || strings.TrimSpace(t.ChatID) == "" {
		return nil
	}
	base := strings.TrimRight(t.API, "/")
	if base == "" {
		base = "https://api.telegram.org"
	}
	payload, err := json.Marshal(map[string]string{
		"chat_id": t.ChatID,
		"text":    n.Text,
	})
	if err != nil {
		return err
	}
	endpoint := base + "/bot" + t.Token + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := t.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("telegram http %d", res.StatusCode)
	}
	var parsed struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal(body, &parsed) == nil && !parsed.OK {
		return fmt.Errorf("telegram send was not accepted")
	}
	return nil
}

func escalationText(chainID string, d Decision) string {
	text := fmt.Sprintf("escalation payable=%s amount=%s reason=%s request=%s decision=%s",
		d.PayableID, FormatUSDC(d.Amount), d.ReasonCode, d.RequestID, d.DecisionHash)
	if d.TxHash != "" {
		text += " " + explorerTx(chainID, d.TxHash)
	}
	return text
}

func explorerTx(chainID, txHash string) string {
	base := "https://testnet.arcscan.app"
	if chainID == "5042" {
		base = "https://arcscan.app"
	}
	return base + "/tx/" + url.PathEscape(txHash)
}
