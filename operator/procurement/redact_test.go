package procurement

import (
	"strings"
	"testing"
)

func TestRedactSecretsAndPrivateKeys(t *testing.T) {
	const key = "pk_live_super_secret_value"
	const priv = "0x0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	text := "porkbun " + key + " sign " + priv + " tail"
	out := Redact(text, key)
	if strings.Contains(out, key) || strings.Contains(out, priv) || strings.Contains(strings.ToLower(out), "0123456789abcdef") {
		t.Fatalf("leaked: %s", out)
	}
	if !strings.Contains(out, "[redacted]") || !strings.Contains(out, "[redacted-key]") {
		t.Fatalf("markers missing: %s", out)
	}
}
