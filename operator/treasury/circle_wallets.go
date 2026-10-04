// 本文件调用 Circle Developer-Controlled Wallets 的合约执行接口。
// pay 和 sweepToReserve 都走 POST /v1/w3s/developer/transactions/contractExecution。
package treasury

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// WalletsClient 是 Circle Wallets HTTP 客户端。测试注入 BaseURL 和 HTTP。
type WalletsClient struct {
	BaseURL      string
	APIKey       string
	EntitySecret []byte
	HTTP         *http.Client
}

// CircleFee 是合约执行的手续费。默认只用 feeLevel。
// Explicit 为真时发送 gwei 单位的 maxFee、priorityFee 和 gasLimit，不再发送 feeLevel。
type CircleFee struct {
	Level       string
	Explicit    bool
	MaxFee      string
	PriorityFee string
	GasLimit    string
}

// ContractExecution 是一次由 Circle 钱包签名的合约调用。
type ContractExecution struct {
	IdempotencyKey string
	WalletID       string
	Blockchain     string
	Contract       string
	Signature      string
	Params         []string
	Fee            CircleFee
}

type circleTx struct {
	ID     string
	TxHash string
	State  string
	Reason string
}

// Execute 提交合约调用并等到 Circle 状态为 COMPLETE。
func (c *WalletsClient) Execute(ctx context.Context, call ContractExecution) (circleTx, error) {
	if c == nil || c.APIKey == "" || len(c.EntitySecret) != 32 {
		return circleTx{}, fmt.Errorf("circle wallets credentials are missing")
	}
	if call.WalletID == "" || call.Contract == "" || call.Signature == "" {
		return circleTx{}, fmt.Errorf("circle contract execution is incomplete")
	}
	pub, err := c.fetchPublicKey(ctx)
	if err != nil {
		return circleTx{}, err
	}
	cipher, err := EncryptEntitySecret(pub, c.EntitySecret)
	if err != nil {
		return circleTx{}, err
	}
	body := map[string]any{
		"idempotencyKey":         call.IdempotencyKey,
		"entitySecretCiphertext": cipher,
		"walletId":               call.WalletID,
		"blockchain":             call.Blockchain,
		"contractAddress":        call.Contract,
		"abiFunctionSignature":   call.Signature,
		"abiParameters":          call.Params,
	}
	if err := applyCircleFee(body, call.Fee); err != nil {
		return circleTx{}, err
	}
	raw, err := c.post(ctx, "/v1/w3s/developer/transactions/contractExecution", body)
	if err != nil {
		return circleTx{}, err
	}
	tx := parseCircleTx(raw)
	if txDone(tx) {
		return tx, nil
	}
	if txFailed(tx.State) {
		return tx, tx.failError()
	}
	if tx.ID == "" {
		return circleTx{}, fmt.Errorf("circle contract execution returned no transaction id")
	}
	deadline, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		polled, err := c.get(deadline, "/v1/w3s/transactions/"+tx.ID)
		if err != nil {
			return circleTx{}, err
		}
		tx = parseCircleTx(polled)
		if txDone(tx) {
			return tx, nil
		}
		if txFailed(tx.State) {
			return tx, tx.failError()
		}
		select {
		case <-deadline.Done():
			return tx, fmt.Errorf("circle transaction %s state %s was not COMPLETE", tx.ID, emptyState(tx.State))
		case <-ticker.C:
		}
	}
}

// applyCircleFee 写入手续费。feeLevel 与 maxFee/priorityFee/gasPrice 互斥。金额单位是 gwei。
func applyCircleFee(body map[string]any, fee CircleFee) error {
	if fee.Explicit {
		if fee.MaxFee == "" || fee.PriorityFee == "" || fee.GasLimit == "" {
			return fmt.Errorf("circle explicit gas needs maxFee, priorityFee (gwei) and gasLimit")
		}
		body["maxFee"] = fee.MaxFee
		body["priorityFee"] = fee.PriorityFee
		body["gasLimit"] = fee.GasLimit
		return nil
	}
	level := strings.ToUpper(strings.TrimSpace(fee.Level))
	if level == "" {
		level = "MEDIUM"
	}
	body["feeLevel"] = level
	return nil
}

func (c *WalletsClient) fetchPublicKey(ctx context.Context) (*rsa.PublicKey, error) {
	raw, err := c.get(ctx, "/v1/w3s/config/entity/publicKey")
	if err != nil {
		return nil, err
	}
	var body struct {
		Data struct {
			PublicKey string `json:"publicKey"`
		} `json:"data"`
		PublicKey string `json:"publicKey"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	pemText := body.Data.PublicKey
	if pemText == "" {
		pemText = body.PublicKey
	}
	pub, err := ParseCirclePublicKey(pemText)
	if err != nil {
		return nil, err
	}
	return pub, nil
}

func (c *WalletsClient) post(ctx context.Context, path string, payload any) ([]byte, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(path), bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	c.decorate(req)
	return c.do(req)
}

func (c *WalletsClient) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url(path), nil)
	if err != nil {
		return nil, err
	}
	c.decorate(req)
	return c.do(req)
}

func (c *WalletsClient) decorate(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
}

func (c *WalletsClient) do(req *http.Request) ([]byte, error) {
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
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
		return nil, fmt.Errorf("circle %s %s: %s", req.Method, req.URL.Path, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (c *WalletsClient) url(path string) string {
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = "https://api.circle.com"
	}
	return base + path
}

func parseCircleTx(raw []byte) circleTx {
	var body struct {
		Data struct {
			ID              string `json:"id"`
			TxHash          string `json:"txHash"`
			TransactionHash string `json:"transactionHash"`
			State           string `json:"state"`
			ErrorReason     string `json:"errorReason"`
			ErrorDetails    string `json:"errorDetails"`
			Reason          string `json:"reason"`
			Transaction     struct {
				ID              string `json:"id"`
				TxHash          string `json:"txHash"`
				TransactionHash string `json:"transactionHash"`
				State           string `json:"state"`
				ErrorReason     string `json:"errorReason"`
				ErrorDetails    string `json:"errorDetails"`
				Reason          string `json:"reason"`
			} `json:"transaction"`
		} `json:"data"`
	}
	_ = json.Unmarshal(raw, &body)
	tx := circleTx{
		ID:     firstNonEmpty(body.Data.Transaction.ID, body.Data.ID),
		TxHash: firstNonEmpty(body.Data.Transaction.TxHash, body.Data.Transaction.TransactionHash, body.Data.TxHash, body.Data.TransactionHash),
		State:  firstNonEmpty(body.Data.Transaction.State, body.Data.State),
		Reason: joinReason(body.Data.Transaction.ErrorReason, body.Data.Transaction.ErrorDetails, body.Data.Transaction.Reason, body.Data.ErrorReason, body.Data.ErrorDetails, body.Data.Reason),
	}
	return tx
}

// joinReason 保留每一段非空原因。errorReason 经常只写 "execution reverted"，选择器在 errorDetails 里。
func joinReason(values ...string) string {
	seen := map[string]bool{}
	parts := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		parts = append(parts, v)
	}
	return strings.Join(parts, " ")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func txDone(tx circleTx) bool {
	return strings.EqualFold(tx.State, "COMPLETE") && tx.TxHash != ""
}

func txFailed(state string) bool {
	switch strings.ToUpper(state) {
	case "FAILED", "DENIED", "CANCELLED", "CANCELED":
		return true
	default:
		return false
	}
}

func (tx circleTx) failError() error {
	reason := tx.Reason
	if reason == "" {
		reason = "no reason"
	}
	return fmt.Errorf("circle transaction %s state %s: %s", tx.ID, tx.State, reason)
}

func emptyState(state string) string {
	if strings.TrimSpace(state) == "" {
		return "UNKNOWN"
	}
	return state
}

// IdempotencyFromHash 把 decisionHash 变成 Circle 接受的 UUID 形状。同一决策得到同一键。
func IdempotencyFromHash(hash common.Hash) string {
	b := hash.Bytes()
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// PayArguments 是 PolicyVault.pay 的 ABI 参数。
func PayArguments(call PayCall) ([]string, error) {
	word, err := CategoryWord(call.Category)
	if err != nil {
		return nil, err
	}
	amount := "0"
	if call.Amount != nil {
		amount = call.Amount.String()
	}
	return []string{
		"0x" + hex.EncodeToString(word[:]),
		NormalizeAddress(call.Payee),
		amount,
		call.DecisionHash.Hex(),
	}, nil
}

// SweepArguments 是 sweepToReserve 的 ABI 参数。
func SweepArguments(call SweepCall) []string {
	amount := "0"
	if call.Amount != nil {
		amount = call.Amount.String()
	}
	return []string{amount, call.DecisionHash.Hex()}
}
