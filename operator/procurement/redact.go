// 本文件从日志和错误里去掉密钥。私钥和 API key 都不能出现在审计或返回值里。
package procurement

import (
	"regexp"
	"strings"
)

var privKey = regexp.MustCompile(`(?i)\b0x[0-9a-f]{64}\b`)

// Redact 替换已知机密，并抹掉看起来像十六进制私钥的片段。
func Redact(text string, secrets ...string) string {
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if len(secret) < 6 {
			continue
		}
		text = strings.ReplaceAll(text, secret, "[redacted]")
		trimmed := strings.TrimPrefix(secret, "0x")
		if trimmed != secret && len(trimmed) >= 6 {
			text = strings.ReplaceAll(text, trimmed, "[redacted]")
		}
	}
	return privKey.ReplaceAllString(text, "[redacted-key]")
}
