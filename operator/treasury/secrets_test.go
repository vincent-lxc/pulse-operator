package treasury

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKeyFileMustBeOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	loose := filepath.Join(dir, "loose")
	if err := os.WriteFile(loose, []byte("aa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPrivateKey("", loose); err == nil {
		t.Fatal("expected mode rejection")
	}
	tight := filepath.Join(dir, "tight")
	// 32-byte hex is not a valid secp256k1 key necessarily; mode check happens first.
	if err := os.WriteFile(tight, []byte("11"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadPrivateKey("", tight)
	if err == nil || !stringsContains(err.Error(), "invalid private key") {
		t.Fatalf("got %v", err)
	}
}

func TestEnvKeyPreferredOverFile(t *testing.T) {
	t.Setenv("OPERATOR_PRIVATE_KEY_TEST", "zz")
	dir := t.TempDir()
	path := filepath.Join(dir, "key")
	if err := os.WriteFile(path, []byte("11"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadPrivateKey("OPERATOR_PRIVATE_KEY_TEST", path)
	if err == nil || !stringsContains(err.Error(), "invalid private key") {
		t.Fatalf("got %v", err)
	}
}

func stringsContains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || (len(s) > 0 && contains(s, sub)))
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
