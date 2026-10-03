package treasury

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestEncryptEntitySecretRoundTrip(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	secret := bytes32()
	cipher, err := EncryptEntitySecret(&key.PublicKey, secret)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(cipher)
	if err != nil {
		t.Fatal(err)
	}
	got, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, key, raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(secret) {
		t.Fatal("ciphertext did not decrypt to the entity secret")
	}
	again, err := EncryptEntitySecret(&key.PublicKey, secret)
	if err != nil {
		t.Fatal(err)
	}
	if again == cipher {
		t.Fatal("entity secret ciphertext must be unique per request")
	}
}

func TestWalletsContractExecution(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	secret := bytes32()
	pemText := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: mustPKIX(t, &key.PublicKey)})
	var seen map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("auth %s", r.Header.Get("Authorization"))
		}
		if r.URL.Path == "/v1/w3s/config/entity/publicKey" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"publicKey": string(pemText)}})
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/w3s/developer/transactions/contractExecution" {
			t.Errorf("path %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Error(err)
		}
		cipher, _ := base64.StdEncoding.DecodeString(seen["entitySecretCiphertext"].(string))
		got, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, key, cipher, nil)
		if err != nil || string(got) != string(secret) {
			t.Errorf("entity secret decrypt: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{
			"id": "tx-1", "txHash": "0x" + strings.Repeat("ab", 32), "state": "CONFIRMED",
		}})
	}))
	defer srv.Close()
	client := &WalletsClient{BaseURL: srv.URL, APIKey: "test-key", EntitySecret: secret, HTTP: srv.Client()}
	hash := common.HexToHash("0x" + strings.Repeat("11", 32))
	params, err := PayArguments(PayCall{Category: "infra", Payee: "0x1111111111111111111111111111111111111111", Amount: big.NewInt(2_000000), DecisionHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := client.Execute(context.Background(), ContractExecution{
		IdempotencyKey: IdempotencyFromHash(hash),
		WalletID:       "wallet-1",
		Blockchain:     "ARC-TESTNET",
		Contract:       "0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01",
		Signature:      "pay(bytes32,address,uint256,bytes32)",
		Params:         params,
		GasPrice:       WeiString(50),
		PriorityFee:    WeiString(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if tx.TxHash == "" || seen["abiFunctionSignature"] != "pay(bytes32,address,uint256,bytes32)" {
		t.Fatalf("tx %+v body %#v", tx, seen)
	}
	if seen["gasPrice"] != WeiString(50) || seen["blockchain"] != "ARC-TESTNET" || seen["walletId"] != "wallet-1" {
		t.Fatalf("body %#v", seen)
	}
}

func TestCCTPParseAndFetch(t *testing.T) {
	body := []byte(`{"messages":[{"status":"complete","decodedMessage":{"destinationDomain":"26","decodedMessageBody":{"amount":"4000000","mintRecipient":"0x0000000000000000000000004face6592ba1adf83e35b01ccd93d8704d647c01"}}}]}`)
	in, err := ParseCCTPMessage(body, "0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01", "0x"+strings.Repeat("aa", 32))
	if err != nil || in.Product != ProductCCTP || in.Amount.Cmp(big.NewInt(4_000000)) != 0 {
		t.Fatalf("in %+v err %v", in, err)
	}
	if _, err := ParseCCTPMessage([]byte(`{"messages":[{"status":"pending","decodedMessage":{"destinationDomain":"26","decodedMessageBody":{"amount":"1","mintRecipient":"0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01"}}}]}`), "0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01", "0xabc"); err == nil {
		t.Fatal("pending attestation should not settle")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/v2/messages/0") {
			t.Errorf("path %s", r.URL.Path)
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	got, err := (CCTPClient{BaseURL: srv.URL, HTTP: srv.Client()}).FetchCCTP(context.Background(), 0, "0x"+strings.Repeat("aa", 32), "0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01")
	if err != nil || got.Product != ProductCCTP {
		t.Fatal(err, got)
	}
}

func TestGatewayParseAndSignature(t *testing.T) {
	raw := []byte(`{"notificationId":"n1","notificationType":"gateway.mint.finalized","notification":{"walletAddress":"0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01","domain":"26","tokenAddress":"0x3600000000000000000000000000000000000000","amount":"1.000000","from":"0xdddddddddddddddddddddddddddddddddddddddd","txHash":"0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}}`)
	in, err := ParseGateway(raw, "0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01", "0x3600000000000000000000000000000000000000")
	if err != nil || in.Product != ProductGateway || in.Amount.Cmp(big.NewInt(1_000000)) != 0 {
		t.Fatalf("%v %+v", err, in)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	sig, err := ecdsa.SignASN1(rand.Reader, key, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(sig)
	if err := VerifyCircleSignature(string(raw), encoded, &key.PublicKey); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCircleSignature(string(raw)+" ", encoded, &key.PublicKey); err == nil {
		t.Fatal("tampered body was accepted")
	}
}

func TestAgentCLIExecute(t *testing.T) {
	mock := NewMockChain(Snapshot{Balance: big.NewInt(0), Categories: map[string]Category{}})
	var got []string
	chain := NewAgentChain(mock, func(_ context.Context, name string, args []string) ([]byte, error) {
		got = append([]string{name}, args...)
		return []byte(`{"txHash":"0x` + strings.Repeat("ab", 32) + `"}`), nil
	}, "circle", "0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01", "ARC-TESTNET", "0xb71c11F7b28D9A912e7C3ACfbe423f7DeA568DEa", "")
	res, err := chain.Pay(context.Background(), PayCall{
		Category: "infra", Payee: "0x1111111111111111111111111111111111111111",
		Amount: big.NewInt(1), DecisionHash: common.HexToHash("0x" + strings.Repeat("22", 32)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Product != ProductAgent || res.Status != "circle_confirmed" {
		t.Fatalf("%+v", res)
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "wallet execute pay(bytes32,address,uint256,bytes32)") || !strings.Contains(joined, "--chain ARC-TESTNET") {
		t.Fatal(joined)
	}
	if _, err := chain.Sweep(context.Background(), SweepCall{Amount: big.NewInt(1), DecisionHash: common.HexToHash("0x" + strings.Repeat("33", 32))}); err == nil {
		t.Fatal("sweep without owner address should fail")
	}
}

func TestSpendingLimitsMainnetOnly(t *testing.T) {
	ok, note := SpendingLimits("ARC-TESTNET")
	if ok || !strings.Contains(note, "mainnet-only") {
		t.Fatal(ok, note)
	}
	ok, note = SpendingLimits("ARC")
	if !ok || !strings.Contains(note, "circle wallet limit set") || !strings.Contains(note, "--chain ARC") {
		t.Fatal(ok, note)
	}
}

func bytes32() []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i + 1)
	}
	return b
}

func mustPKIX(t *testing.T, pub *rsa.PublicKey) []byte {
	t.Helper()
	b, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
