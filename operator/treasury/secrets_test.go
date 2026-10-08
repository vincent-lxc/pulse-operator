package treasury

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
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

func TestJSONKeyFileRejectsMismatchedAddressAndGroupRead(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	hexKey := hex.EncodeToString(crypto.FromECDSA(key))
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	raw := []byte(`{"address":"0x1111111111111111111111111111111111111111","private_key":"0x` + hexKey + `"}`)
	if err := os.WriteFile(bad, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = LoadPrivateKey("", bad)
	if err == nil || !strings.Contains(err.Error(), "does not match") || strings.Contains(err.Error(), hexKey) {
		t.Fatalf("got %v", err)
	}
	okPath := filepath.Join(dir, "ok.json")
	okRaw := []byte(`{"address":"` + addr + `","private_key":"0x` + hexKey + `","purpose":"test"}`)
	if err := os.WriteFile(okPath, okRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPrivateKey("", okPath)
	if err != nil {
		t.Fatal(err)
	}
	if crypto.PubkeyToAddress(got.PublicKey).Hex() != addr {
		t.Fatalf("address %s", crypto.PubkeyToAddress(got.PublicKey).Hex())
	}
	group := filepath.Join(dir, "group.json")
	if err := os.WriteFile(group, okRaw, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPrivateKey("", group); err == nil || !strings.Contains(err.Error(), "group") {
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
