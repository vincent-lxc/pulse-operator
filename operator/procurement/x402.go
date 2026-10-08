// 本文件解析 x402 v2 的 PAYMENT-REQUIRED，并按 exact / auth-capture 组签名载荷。
// 网络、资产、金额、收款方任何一项对不上就拒绝签名。
package procurement

import (
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

// Requirements 是 PAYMENT-REQUIRED 解开后的内容。
type Requirements struct {
	X402Version int      `json:"x402Version"`
	Accepts     []Accept `json:"accepts"`
	Resource    any      `json:"resource,omitempty"`
}

// Accept 是一条可付的条款。
type Accept struct {
	Scheme            string         `json:"scheme"`
	Network           string         `json:"network"`
	Amount            string         `json:"amount"`
	Asset             string         `json:"asset"`
	PayTo             string         `json:"payTo"`
	MaxTimeoutSeconds int64          `json:"maxTimeoutSeconds"`
	Extra             map[string]any `json:"extra"`
}

// PayPolicy 是签名前的硬限制。金额必须和账单完全一致。
type PayPolicy struct {
	Networks []string
	Asset    string
	Amount   *big.Int
	Schemes  []string
}

// Payment 是准备放进 PAYMENT-SIGNATURE 的载荷。
type Payment struct {
	Header  string
	Scheme  string
	Network string
	Payer   string
	Amount  string
	Asset   string
	PayTo   string
}

// ParsePaymentRequired 解开 header。支持标准 base64 和 base64url，也接受已经是 JSON 的值。
func ParsePaymentRequired(header string) (Requirements, error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return Requirements{}, fmt.Errorf("PAYMENT-REQUIRED is empty")
	}
	raw := []byte(header)
	if !strings.HasPrefix(header, "{") {
		decoded, err := decodeB64(header)
		if err != nil {
			return Requirements{}, fmt.Errorf("PAYMENT-REQUIRED: %w", err)
		}
		raw = decoded
	}
	var req Requirements
	if err := json.Unmarshal(raw, &req); err != nil {
		return Requirements{}, fmt.Errorf("PAYMENT-REQUIRED json: %w", err)
	}
	if req.X402Version != 0 && req.X402Version != 2 {
		return Requirements{}, fmt.Errorf("unsupported x402 version %d", req.X402Version)
	}
	return req, nil
}

// Select 从 accepts 里挑一条符合策略的条款。
func Select(req Requirements, policy PayPolicy) (Accept, error) {
	if policy.Amount == nil || policy.Amount.Sign() <= 0 {
		return Accept{}, fmt.Errorf("bill amount is missing")
	}
	var reason string
	for _, item := range req.Accepts {
		if err := acceptOK(item, policy); err != nil {
			reason = err.Error()
			continue
		}
		return item, nil
	}
	if reason == "" {
		reason = "no accepts entries"
	}
	return Accept{}, fmt.Errorf("no acceptable x402 terms: %s", reason)
}

func acceptOK(item Accept, policy PayPolicy) error {
	scheme := strings.ToLower(strings.TrimSpace(item.Scheme))
	if len(policy.Schemes) > 0 && !containsFold(policy.Schemes, scheme) {
		return fmt.Errorf("scheme %s", item.Scheme)
	}
	if scheme != "exact" && scheme != "auth-capture" {
		return fmt.Errorf("scheme %s", item.Scheme)
	}
	if len(policy.Networks) > 0 && !containsFold(policy.Networks, item.Network) {
		return fmt.Errorf("network %s", item.Network)
	}
	if !sameAddr(item.Asset, policy.Asset) {
		return fmt.Errorf("asset %s", item.Asset)
	}
	amount, ok := new(big.Int).SetString(strings.TrimSpace(item.Amount), 10)
	if !ok || amount.Cmp(policy.Amount) != 0 {
		return fmt.Errorf("amount %s", item.Amount)
	}
	if !common.IsHexAddress(item.PayTo) || common.HexToAddress(item.PayTo) == (common.Address{}) {
		return fmt.Errorf("payTo")
	}
	if item.MaxTimeoutSeconds <= 0 || item.MaxTimeoutSeconds > 3600 {
		return fmt.Errorf("timeout")
	}
	return nil
}

// SignPayment 按条款签名。exact 用 EIP-3009 transferWithAuthorization。
// auth-capture 用 ReceiveWithAuthorization，nonce 按 x402 EVM 规范从 PaymentInfo 推出。
func SignPayment(key *ecdsa.PrivateKey, item Accept, now time.Time) (Payment, error) {
	if key == nil {
		return Payment{}, fmt.Errorf("signer is missing")
	}
	payer := crypto.PubkeyToAddress(key.PublicKey)
	_, auth, extra, err := PrepareTypedData(payer, item, now)
	if err != nil {
		return Payment{}, err
	}
	primary := "TransferWithAuthorization"
	if strings.EqualFold(item.Scheme, "auth-capture") {
		primary = "ReceiveWithAuthorization"
	}
	name, version, err := tokenDomain(item)
	if err != nil {
		return Payment{}, err
	}
	chainID, err := chainFromNetwork(item.Network)
	if err != nil {
		return Payment{}, err
	}
	asset := item.Asset
	sig, err := signEIP3009(key, primary, name, version, chainID, asset, auth)
	if err != nil {
		return Payment{}, err
	}
	sigHex := "0x" + common.Bytes2Hex(sig)
	if err := VerifyTypedSignature(item, payer, auth, sigHex); err != nil {
		return Payment{}, err
	}
	return packPayment(item, payer, map[string]any{
		"signature": sigHex, "authorization": auth,
	}, extra)
}

// VerifyTypedSignature 用 ecrecover 核对签名者就是 payer。
func VerifyTypedSignature(item Accept, payer common.Address, auth map[string]any, sigHex string) error {
	primary := "TransferWithAuthorization"
	if strings.EqualFold(item.Scheme, "auth-capture") {
		primary = "ReceiveWithAuthorization"
	}
	name, version, err := tokenDomain(item)
	if err != nil {
		return err
	}
	chainID, err := chainFromNetwork(item.Network)
	if err != nil {
		return err
	}
	td := apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": {
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
			primary: {
				{Name: "from", Type: "address"},
				{Name: "to", Type: "address"},
				{Name: "value", Type: "uint256"},
				{Name: "validAfter", Type: "uint256"},
				{Name: "validBefore", Type: "uint256"},
				{Name: "nonce", Type: "bytes32"},
			},
		},
		PrimaryType: primary,
		Domain: apitypes.TypedDataDomain{
			Name: name, Version: version, ChainId: math.NewHexOrDecimal256(chainID),
			VerifyingContract: common.HexToAddress(item.Asset).Hex(),
		},
		Message: auth,
	}
	hash, _, err := apitypes.TypedDataAndHash(td)
	if err != nil {
		return err
	}
	sig := common.FromHex(strings.TrimSpace(sigHex))
	if len(sig) != 65 {
		return fmt.Errorf("signature length %d", len(sig))
	}
	sig = append([]byte(nil), sig...)
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	pub, err := crypto.SigToPub(hash, sig)
	if err != nil {
		return fmt.Errorf("recover signer: %w", err)
	}
	got := crypto.PubkeyToAddress(*pub)
	if got != payer {
		return fmt.Errorf("recovered signer %s is not payer %s", got.Hex(), payer.Hex())
	}
	return nil
}

func packPayment(item Accept, payer common.Address, payload map[string]any, extra map[string]any) (Payment, error) {
	for k, v := range extra {
		payload[k] = v
	}
	body := map[string]any{
		"x402Version": 2,
		"accepted":    item,
		"payload":     payload,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Payment{}, err
	}
	return Payment{
		Header:  base64.StdEncoding.EncodeToString(raw),
		Scheme:  item.Scheme,
		Network: item.Network,
		Payer:   payer.Hex(),
		Amount:  item.Amount,
		Asset:   common.HexToAddress(item.Asset).Hex(),
		PayTo:   common.HexToAddress(item.PayTo).Hex(),
	}, nil
}

func signEIP3009(key *ecdsa.PrivateKey, primary, name, version string, chainID int64, asset string, auth map[string]any) ([]byte, error) {
	td := apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": {
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
			primary: {
				{Name: "from", Type: "address"},
				{Name: "to", Type: "address"},
				{Name: "value", Type: "uint256"},
				{Name: "validAfter", Type: "uint256"},
				{Name: "validBefore", Type: "uint256"},
				{Name: "nonce", Type: "bytes32"},
			},
		},
		PrimaryType: primary,
		Domain: apitypes.TypedDataDomain{
			Name:              name,
			Version:           version,
			ChainId:           math.NewHexOrDecimal256(chainID),
			VerifyingContract: common.HexToAddress(asset).Hex(),
		},
		Message: auth,
	}
	hash, _, err := apitypes.TypedDataAndHash(td)
	if err != nil {
		return nil, err
	}
	sig, err := crypto.Sign(hash, key)
	if err != nil {
		return nil, err
	}
	sig[64] += 27
	return sig, nil
}

func tokenDomain(item Accept) (string, string, error) {
	name := extraString(item, "name")
	version := extraString(item, "version")
	if name == "" || version == "" {
		return "", "", fmt.Errorf("x402 extra.name and extra.version are required")
	}
	return name, version, nil
}

func chainFromNetwork(network string) (int64, error) {
	network = strings.ToLower(strings.TrimSpace(network))
	var id int64
	if _, err := fmt.Sscanf(network, "eip155:%d", &id); err != nil || id <= 0 {
		return 0, fmt.Errorf("network %s", network)
	}
	return id, nil
}

func extraString(item Accept, key string) string {
	if item.Extra == nil {
		return ""
	}
	v, ok := item.Extra[key]
	if !ok || v == nil {
		return ""
	}
	switch n := v.(type) {
	case string:
		return strings.TrimSpace(n)
	case float64:
		return fmt.Sprintf("%.0f", n)
	default:
		return strings.TrimSpace(fmt.Sprint(n))
	}
}

func extraInt(item Accept, key string, fallback int64) int64 {
	text := extraString(item, key)
	if text == "" {
		return fallback
	}
	n, ok := new(big.Int).SetString(text, 10)
	if !ok || !n.IsInt64() {
		return fallback
	}
	return n.Int64()
}

func mustInt(s string) *big.Int {
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return big.NewInt(0)
	}
	return n
}

func decodeB64(s string) ([]byte, error) {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := base64.URLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawURLEncoding.DecodeString(s)
}

func containsFold(list []string, want string) bool {
	for _, item := range list {
		if strings.EqualFold(item, want) {
			return true
		}
	}
	return false
}

// DecodePaymentResponse 解开 PAYMENT-RESPONSE。没有交易哈希时不编造一个。
func DecodePaymentResponse(header string) (map[string]any, error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return map[string]any{}, nil
	}
	raw := []byte(header)
	if !strings.HasPrefix(header, "{") {
		decoded, err := decodeB64(header)
		if err != nil {
			return nil, err
		}
		raw = decoded
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
