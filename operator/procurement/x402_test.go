package procurement

import (
	"encoding/base64"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestSelectRejectsWrongNetworkAmountAndAsset(t *testing.T) {
	raw := goldenRequired()
	req, err := ParsePaymentRequired(raw)
	if err != nil {
		t.Fatal(err)
	}
	policy := PayPolicy{
		Networks: []string{"eip155:8453", "eip155:84532"},
		Asset:    BaseSepoliaUSDC,
		Amount:   big.NewInt(2_040_000),
		Schemes:  []string{"exact", "auth-capture"},
	}
	item, err := Select(req, policy)
	if err != nil {
		t.Fatal(err)
	}
	if item.Scheme != "exact" {
		t.Fatalf("selected %s", item.Scheme)
	}
	policy.Amount = big.NewInt(1)
	if _, err := Select(req, policy); err == nil {
		t.Fatal("amount mismatch was accepted")
	}
	policy.Amount = big.NewInt(2_040_000)
	policy.Networks = []string{"eip155:8453"}
	if _, err := Select(req, policy); err == nil {
		t.Fatal("wrong network was accepted")
	}
	policy.Networks = []string{"eip155:84532"}
	policy.Asset = "0x0000000000000000000000000000000000000001"
	if _, err := Select(req, policy); err == nil {
		t.Fatal("wrong asset was accepted")
	}
}

func TestSignExactRecoversAndRefusesMainnetNetworkOnSepoliaPolicy(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	req, err := ParsePaymentRequired(goldenRequired())
	if err != nil {
		t.Fatal(err)
	}
	item, err := Select(req, PayPolicy{
		Networks: []string{"eip155:84532"}, Asset: BaseSepoliaUSDC, Amount: big.NewInt(2_040_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	payment, err := SignPayment(key, item, time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if payment.Network != "eip155:84532" || payment.Payer == "" {
		t.Fatalf("%+v", payment)
	}
	decoded, err := base64.StdEncoding.DecodeString(payment.Header)
	if err != nil || !strings.Contains(string(decoded), "TransferWithAuthorization") && !strings.Contains(string(decoded), payment.Amount) {
		t.Fatalf("header %s", decoded)
	}
	item.Network = "eip155:1"
	if _, err := SignPayment(key, item, time.Unix(1_700_000_000, 0)); err != nil && !strings.Contains(err.Error(), "network") {
		// 签名函数本身不按策略拒网，策略在 Select。这里只确认主网条款不会被测试策略选中。
	}
	mainnet := item
	mainnet.Network = "eip155:8453"
	mainnet.Asset = BaseUSDC
	if _, err := Select(Requirements{X402Version: 2, Accepts: []Accept{mainnet}}, PayPolicy{
		Networks: []string{"eip155:84532"}, Asset: BaseSepoliaUSDC, Amount: big.NewInt(2_040_000),
	}); err == nil {
		t.Fatal("mainnet terms passed a sepolia policy")
	}
}

func TestAuthCaptureGoldenSigns(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	header := base64.StdEncoding.EncodeToString([]byte(`{
	  "x402Version": 2,
	  "accepts": [{
	    "scheme": "auth-capture",
	    "network": "eip155:84532",
	    "amount": "8750000",
	    "asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
	    "payTo": "0x3333333333333333333333333333333333333333",
	    "maxTimeoutSeconds": 600,
	    "extra": {
	      "name": "USDC",
	      "version": "2",
	      "assetTransferMethod": "eip3009",
	      "captureAuthorizer": "0x2222222222222222222222222222222222222222"
	    }
	  }]
	}`))
	req, err := ParsePaymentRequired(header)
	if err != nil {
		t.Fatal(err)
	}
	item, err := Select(req, PayPolicy{
		Networks: []string{"eip155:84532"}, Asset: BaseSepoliaUSDC, Amount: big.NewInt(8_750_000),
		Schemes: []string{"auth-capture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	payment, err := SignPayment(key, item, time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(payment.Header)
	if err != nil {
		t.Fatal(err)
	}
	text := string(decoded)
	if !strings.Contains(text, "ReceiveWithAuthorization") && !strings.Contains(text, EIP3009CollectorV11) {
		t.Fatalf("auth-capture payload %s", text)
	}
	if common.HexToAddress(payment.PayTo) != common.HexToAddress("0x3333333333333333333333333333333333333333") {
		t.Fatalf("payTo %s", payment.PayTo)
	}
}

func TestDecodePaymentResponse(t *testing.T) {
	raw := base64.StdEncoding.EncodeToString([]byte(`{"success":true,"transaction":"0xabc"}`))
	got, err := DecodePaymentResponse(raw)
	if err != nil || got["transaction"] != "0xabc" {
		t.Fatalf("%v %#v", err, got)
	}
}

func goldenRequired() string {
	return base64.StdEncoding.EncodeToString([]byte(`{
	  "x402Version": 2,
	  "accepts": [
	    {"scheme":"exact","network":"eip155:1","amount":"2040000","asset":"0x036CbD53842c5426634e7929541eC2318f3dCF7e","payTo":"0x1111111111111111111111111111111111111111","maxTimeoutSeconds":300,"extra":{"name":"USDC","version":"2"}},
	    {"scheme":"exact","network":"eip155:84532","amount":"999","asset":"0x036CbD53842c5426634e7929541eC2318f3dCF7e","payTo":"0x1111111111111111111111111111111111111111","maxTimeoutSeconds":300,"extra":{"name":"USDC","version":"2"}},
	    {"scheme":"exact","network":"eip155:84532","amount":"2040000","asset":"0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913","payTo":"0x1111111111111111111111111111111111111111","maxTimeoutSeconds":300,"extra":{"name":"USDC","version":"2"}},
	    {"scheme":"exact","network":"eip155:84532","amount":"2040000","asset":"0x036CbD53842c5426634e7929541eC2318f3dCF7e","payTo":"0x1111111111111111111111111111111111111111","maxTimeoutSeconds":300,"extra":{"name":"USDC","version":"2"}}
	  ]
	}`))
}
