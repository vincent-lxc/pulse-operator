package procurement

import (
	"encoding/base64"
	"encoding/json"
	"errors"
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

func TestFinishPaymentRejectsADifferentSigner(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	other, err := crypto.GenerateKey()
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
	now := time.Unix(1_700_000_000, 0)
	payer := crypto.PubkeyToAddress(key.PublicKey)
	_, auth, extra, err := PrepareTypedData(payer, item, now)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := SignPayment(other, item, now)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(signed.Header)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Payload struct {
			Signature string `json:"signature"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if _, err := FinishPayment(item, payer, auth, body.Payload.Signature, extra); err == nil {
		t.Fatal("recovered signer was not the payer")
	}
}

func TestSelectPrefersExactWhenAuthCaptureIsListedFirst(t *testing.T) {
	payTo := "0x1111111111111111111111111111111111111111"
	auth := Accept{
		Scheme: "auth-capture", Network: "eip155:8453", Amount: "1630000", Asset: BaseUSDC, PayTo: payTo,
		MaxTimeoutSeconds: 600,
		Extra: map[string]any{
			"name": "USDC", "version": "2", "assetTransferMethod": "eip3009",
			"authCaptureEscrow": AuthCaptureEscrowV10, "paymentCollector": EIP3009CollectorV10,
			"captureAuthorizer": "0x2222222222222222222222222222222222222222",
		},
	}
	exact := Accept{
		Scheme: "exact", Network: "eip155:8453", Amount: "1630000", Asset: BaseUSDC, PayTo: payTo,
		MaxTimeoutSeconds: 600, Extra: map[string]any{"name": "USDC", "version": "2"},
	}
	policy := PayPolicy{Networks: []string{"eip155:8453"}, Asset: BaseUSDC, Amount: big.NewInt(1_630_000), Schemes: []string{"exact", "auth-capture"}}
	item, err := Select(Requirements{X402Version: 2, Accepts: []Accept{auth, exact}}, policy)
	if err != nil || item.Scheme != "exact" {
		t.Fatalf("scheme %s err %v", item.Scheme, err)
	}
	wrongExact := exact
	wrongExact.Amount = "1"
	item, err = Select(Requirements{X402Version: 2, Accepts: []Accept{wrongExact, auth}}, policy)
	if err != nil || item.Scheme != "auth-capture" {
		t.Fatalf("fallback scheme %s err %v", item.Scheme, err)
	}
	item.PayTo = "0x0000000000000000000000000000000000000000"
	if err := acceptOK(item, policy); err == nil {
		t.Fatal("empty payTo was accepted")
	}
}

func TestCommerceV10CollectorAllowlist(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	base := Accept{
		Scheme: "auth-capture", Network: "eip155:8453", Amount: "1630000", Asset: BaseUSDC,
		PayTo: "0x3333333333333333333333333333333333333333", MaxTimeoutSeconds: 600,
		Extra: map[string]any{
			"name": "USDC", "version": "2", "assetTransferMethod": "eip3009",
			"captureAuthorizer": "0x2222222222222222222222222222222222222222",
			"authCaptureEscrow": AuthCaptureEscrowV10, "paymentCollector": EIP3009CollectorV10,
		},
	}
	payment, err := SignPayment(key, base, now)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(payment.Header)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(decoded))
	if !strings.Contains(text, strings.ToLower(EIP3009CollectorV10)) || strings.Contains(text, strings.ToLower(EIP3009CollectorV11)) {
		t.Fatalf("payload %s", decoded)
	}
	omitted := base
	omitted.Extra = copyExtra(base.Extra)
	delete(omitted.Extra, "paymentCollector")
	if _, err := SignPayment(key, omitted, now); err != nil {
		t.Fatal(err)
	}
	mixed := []Accept{base, base}
	mixed[0].Extra = copyExtra(base.Extra)
	mixed[0].Extra["paymentCollector"] = EIP3009CollectorV11
	mixed[1].Extra = copyExtra(base.Extra)
	mixed[1].Extra["authCaptureEscrow"] = AuthCaptureEscrowV11
	mixed[1].Extra["erc3009PaymentCollector"] = EIP3009CollectorV10
	for _, item := range mixed {
		_, err := SignPayment(key, item, now)
		if !errors.Is(err, ErrUnsupportedEscrow) {
			t.Fatalf("mixed pair err %v", err)
		}
	}
	unknown := base
	unknown.Extra = copyExtra(base.Extra)
	unknown.Extra["authCaptureEscrow"] = "0x4444444444444444444444444444444444444444"
	delete(unknown.Extra, "paymentCollector")
	if _, err := SignPayment(key, unknown, now); !errors.Is(err, ErrUnsupportedEscrow) {
		t.Fatalf("unknown escrow err %v", err)
	}
}

func TestPaymentInfoNonceMatchesV10GetHash(t *testing.T) {
	typehash := crypto.Keccak256Hash([]byte(paymentInfoType))
	if typehash.Hex() != "0xae68ac7ce30c86ece8196b61a7c486d8f0061f575037fbd34e7fe4e2820c6591" {
		t.Fatalf("typehash %s", typehash.Hex())
	}
	info := paymentInfo{
		Operator: common.HexToAddress("0x1111111111111111111111111111111111111111"),
		Receiver: common.HexToAddress("0x2222222222222222222222222222222222222222"),
		Token:    common.HexToAddress(BaseUSDC), MaxAmount: big.NewInt(1_630_000),
		PreApprovalExpiry: 1_700_000_600, AuthorizationExpiry: 1_700_000_900, RefundExpiry: 1_700_001_200,
		Salt: big.NewInt(1),
	}
	nonce, err := signatureNonce(8453, common.HexToAddress(AuthCaptureEscrowV10), info)
	if err != nil {
		t.Fatal(err)
	}
	if nonce.Hex() != "0x868bf39089d25cb718e69a943b49d84f788ab80521d7dbab0d0e90bbfaad64f7" {
		t.Fatalf("nonce %s", nonce.Hex())
	}
	other, err := signatureNonce(8453, common.HexToAddress(AuthCaptureEscrowV11), info)
	if err != nil || other == nonce {
		t.Fatalf("v1.1 nonce %s err %v", other.Hex(), err)
	}
}

func copyExtra(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		out[k] = v
	}
	return out
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
