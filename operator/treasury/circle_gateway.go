// 本文件解析 Circle Gateway 的 v2 webhook，并校验 ECDSA-SHA256 签名。
// 公钥来自 GET /v2/notifications/publicKey/{keyId}。测试可以直接传入公钥。
package treasury

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// GatewayNotice 是 Circle v2 通知里和入账有关的字段。
type GatewayNotice struct {
	NotificationID   string `json:"notificationId"`
	NotificationType string `json:"notificationType"`
	Notification     struct {
		ID            string     `json:"id"`
		WalletAddress string     `json:"walletAddress"`
		Domain        flexString `json:"domain"`
		TokenAddress  string     `json:"tokenAddress"`
		Amount        string     `json:"amount"`
		From          string     `json:"from"`
		TxHash        string     `json:"txHash"`
		Env           string     `json:"env"`
	} `json:"notification"`
}

type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	*f = flexString(b)
	return nil
}

// ParseGateway 接受 gateway.deposit.finalized 和 gateway.mint.finalized。
// 只收下 Arc domain 26、并且收款地址是金库的 USDC。
func ParseGateway(raw []byte, vault, usdc string) (Inflow, error) {
	var note GatewayNotice
	if err := json.Unmarshal(raw, &note); err != nil {
		return Inflow{}, err
	}
	switch note.NotificationType {
	case "gateway.deposit.finalized", "gateway.mint.finalized", "gateway.mint.forwarded":
	default:
		return Inflow{}, fmt.Errorf("unsupported gateway notification %s", note.NotificationType)
	}
	if string(note.Notification.Domain) != "" && string(note.Notification.Domain) != ArcCCTPDomain {
		return Inflow{}, fmt.Errorf("gateway domain %s is not Arc", note.Notification.Domain)
	}
	if usdc != "" && note.Notification.TokenAddress != "" && !sameAddress(note.Notification.TokenAddress, usdc) {
		return Inflow{}, fmt.Errorf("gateway token is not Arc USDC")
	}
	if !sameAddress(note.Notification.WalletAddress, vault) {
		return Inflow{}, fmt.Errorf("gateway wallet is not the vault")
	}
	amount, err := ParseUSDC(note.Notification.Amount)
	if err != nil {
		return Inflow{}, err
	}
	ref := note.Notification.TxHash
	if ref == "" {
		ref = note.NotificationID
	}
	return Inflow{
		TxHash:  strings.ToLower(ref),
		From:    NormalizeAddress(note.Notification.From),
		Amount:  amount,
		Source:  ProductGateway,
		Product: ProductGateway,
	}, nil
}

// VerifyCircleSignature 用 ECDSA-SHA256 校验 webhook 原文。签名是 base64 的 ASN.1。
func VerifyCircleSignature(body, signatureB64 string, pub *ecdsa.PublicKey) error {
	if pub == nil {
		return fmt.Errorf("circle webhook public key is missing")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signatureB64))
	if err != nil {
		return fmt.Errorf("circle webhook signature is not base64")
	}
	sum := sha256.Sum256([]byte(body))
	if !ecdsa.VerifyASN1(pub, sum[:], sig) {
		return fmt.Errorf("circle webhook signature does not match")
	}
	return nil
}

// ParseECDSAPEM 解析 Circle 通知公钥接口返回的 PEM。
func ParseECDSAPEM(pemText string) (*ecdsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, fmt.Errorf("circle notification key is not PEM")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("circle notification key is not ECDSA")
	}
	return key, nil
}

// FetchNotificationKey 读取 Circle 用来签 webhook 的公钥。
func FetchNotificationKey(ctx context.Context, apiBase, apiKey, keyID string, httpClient *http.Client) (*ecdsa.PublicKey, error) {
	if apiKey == "" || keyID == "" {
		return nil, fmt.Errorf("circle webhook verification needs CIRCLE_API_KEY and X-Circle-Key-Id")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	url := strings.TrimRight(apiBase, "/") + "/v2/notifications/publicKey/" + keyID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("circle notification key: %s", strings.TrimSpace(string(body)))
	}
	var payload struct {
		Data struct {
			PublicKey string `json:"publicKey"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return ParseECDSAPEM(payload.Data.PublicKey)
}
