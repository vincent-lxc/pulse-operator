package treasury

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSignTypedDataPostsCiphertextNotSecret(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	secret := bytes32()
	pemText := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: mustPKIX(t, &key.PublicKey)})
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/w3s/config/entity/publicKey" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"publicKey": string(pemText)}})
			return
		}
		if r.URL.Path != "/v1/w3s/developer/sign/typedData" {
			t.Errorf("path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var raw map[string]any
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(raw)
		body = string(encoded)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"signature": "0xabc"}})
	}))
	defer srv.Close()
	client := &WalletsClient{BaseURL: srv.URL, APIKey: "test-key", EntitySecret: secret, HTTP: srv.Client()}
	sig, err := client.SignTypedData(context.Background(), "wallet-1", "", "BASE", `{"primaryType":"TransferWithAuthorization"}`)
	if err != nil {
		t.Fatal(err)
	}
	if sig != "0xabc" {
		t.Fatal(sig)
	}
	if strings.Contains(body, string(secret)) || !strings.Contains(body, "entitySecretCiphertext") || !strings.Contains(body, "wallet-1") {
		t.Fatalf("body leaked or incomplete: %s", body)
	}
}
