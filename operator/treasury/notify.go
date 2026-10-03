// 本文件定义人工升级的通知钩子。Telegram 只保留接口，未接真实发送。
package treasury

import (
	"context"
	"errors"
	"os"
)

// ErrNotifierUnconfigured 表示通知通道还没有密钥。
var ErrNotifierUnconfigured = errors.New("notifier is not configured")

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

// TelegramNotifier 是后续接入 Telegram Bot 的占位实现。
type TelegramNotifier struct{}

// Notify 在缺少环境变量时返回未配置，有变量时仍拒绝发送。
func (TelegramNotifier) Notify(context.Context, Notice) error {
	if os.Getenv("TELEGRAM_BOT_TOKEN") == "" || os.Getenv("TELEGRAM_CHAT_ID") == "" {
		return ErrNotifierUnconfigured
	}
	return errors.New("telegram notifier is a stub")
}
