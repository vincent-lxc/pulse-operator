// 本文件调用 Porkbun JSON API。密钥只放在请求体里，错误文本会先脱敏。
package procurement

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Porkbun 是带每秒一次限速的客户端。
type Porkbun struct {
	BaseURL     string
	APIKey      string
	Secret      string
	HTTP        *http.Client
	MinInterval time.Duration
	mu          sync.Mutex
	last        time.Time
}

// Order 是一次 create 或 renew 的结果。HTTP 402 不是传输失败。
type Order struct {
	HTTPStatus   int
	Status       string
	Code         string
	Message      string
	NextAction   string
	CheckoutID   string
	X402URL      string
	PayURL       string
	Required     string
	OrderID      string
	KeptAsCredit bool
	BalanceCents int64
	CostCents    int64
	raw          []byte
}

// CheckDomain 查询域名是否可注册。
func (p *Porkbun) CheckDomain(ctx context.Context, domain string) (Order, error) {
	return p.post(ctx, "/domain/checkDomain/"+domain, map[string]any{}, "")
}

// Pricing 拉取价目表。
func (p *Porkbun) Pricing(ctx context.Context) (map[string]any, error) {
	order, err := p.post(ctx, "/pricing/get", map[string]any{}, "")
	if err != nil {
		return nil, err
	}
	if order.Status != "" && order.Status != "SUCCESS" && order.HTTPStatus >= 400 {
		return nil, order.Err()
	}
	var body map[string]any
	if err := json.Unmarshal(order.raw, &body); err != nil {
		return nil, err
	}
	return body, nil
}

// Create 注册域名。dryRun 为真时不扣款。signature 是 PAYMENT-SIGNATURE 头。
func (p *Porkbun) Create(ctx context.Context, domain string, costCents int64, years int, dryRun bool, checkoutID, idempotency, signature string) (Order, error) {
	body := p.payBody(costCents, years, dryRun, checkoutID)
	return p.post(ctx, "/domain/create/"+domain, body, signature, idempotency)
}

// Renew 续费域名。
func (p *Porkbun) Renew(ctx context.Context, domain string, costCents int64, years int, dryRun bool, checkoutID, idempotency, signature string) (Order, error) {
	body := p.payBody(costCents, years, dryRun, checkoutID)
	return p.post(ctx, "/domain/renew/"+domain, body, signature, idempotency)
}

func (p *Porkbun) payBody(costCents int64, years int, dryRun bool, checkoutID string) map[string]any {
	if years <= 0 {
		years = 1
	}
	body := map[string]any{"years": years}
	if dryRun {
		body["dryRun"] = true
		body["cost"] = 0
		return body
	}
	body["cost"] = costCents
	body["agreeToTerms"] = "yes"
	if checkoutID != "" {
		body["usdcCheckoutId"] = checkoutID
		return body
	}
	body["payWith"] = "usdc"
	return body
}

func (p *Porkbun) post(ctx context.Context, path string, body map[string]any, signature string, idempotency ...string) (Order, error) {
	if err := p.wait(ctx); err != nil {
		return Order{}, err
	}
	payload := map[string]any{"apikey": p.APIKey, "secretapikey": p.Secret}
	for k, v := range body {
		payload[k] = v
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Order{}, err
	}
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = "https://api.porkbun.com/api/json/v3"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(raw))
	if err != nil {
		return Order{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if signature != "" {
		req.Header.Set("PAYMENT-SIGNATURE", signature)
	}
	if len(idempotency) > 0 && idempotency[0] != "" {
		req.Header.Set("Idempotency-Key", idempotency[0])
	}
	client := p.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return Order{}, fmt.Errorf("porkbun: %s", Redact(err.Error(), p.APIKey, p.Secret))
	}
	defer res.Body.Close()
	buf, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return Order{}, err
	}
	order := decodeOrder(res.StatusCode, res.Header.Get("PAYMENT-REQUIRED"), buf)
	order.raw = buf
	order.Message = Redact(order.Message, p.APIKey, p.Secret)
	if res.StatusCode >= 400 && res.StatusCode != http.StatusPaymentRequired {
		return order, order.Err()
	}
	return order, nil
}

func (p *Porkbun) wait(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	interval := p.MinInterval
	if interval == 0 {
		interval = time.Second
	}
	if interval < 0 {
		return nil
	}
	wait := interval - time.Since(p.last)
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	p.last = time.Now()
	return nil
}

func decodeOrder(status int, required string, raw []byte) Order {
	var body struct {
		Status     string `json:"status"`
		Code       string `json:"code"`
		Message    string `json:"message"`
		NextAction string `json:"next_action"`
		CheckoutID string `json:"checkoutId"`
		X402URL    string `json:"x402Url"`
		PayURL     string `json:"payUrl"`
		OrderID    any    `json:"orderId"`
		Cost       any    `json:"cost"`
		Payment    struct {
			KeptAsCredit bool  `json:"keptAsCredit"`
			BalanceCents int64 `json:"balance_cents"`
		} `json:"payment"`
	}
	_ = json.Unmarshal(raw, &body)
	orderID := strings.TrimSpace(fmt.Sprint(body.OrderID))
	if orderID == "<nil>" {
		orderID = ""
	}
	return Order{
		HTTPStatus: status, Status: body.Status, Code: body.Code, Message: body.Message,
		NextAction: body.NextAction, CheckoutID: body.CheckoutID, X402URL: body.X402URL,
		PayURL: body.PayURL, Required: required, OrderID: orderID,
		KeptAsCredit: body.Payment.KeptAsCredit, BalanceCents: body.Payment.BalanceCents,
		CostCents: centsFrom(body.Cost),
	}
}

func centsFrom(v any) int64 {
	switch n := v.(type) {
	case float64:
		if n != float64(int64(n)) {
			return int64(n * 100)
		}
		return int64(n)
	case string:
		n = strings.TrimSpace(n)
		if strings.Contains(n, ".") {
			var f float64
			if _, err := fmt.Sscanf(n, "%f", &f); err == nil {
				return int64(f * 100)
			}
			return 0
		}
		var i int64
		if _, err := fmt.Sscanf(n, "%d", &i); err == nil {
			return i
		}
	}
	return 0
}

// Err 把供应商错误变成可记录的文本，并去掉密钥。
func (o Order) Err() error {
	code := o.Code
	if code == "" {
		code = o.Status
	}
	if code == "" {
		code = fmt.Sprintf("http_%d", o.HTTPStatus)
	}
	return fmt.Errorf("porkbun %s: %s next=%s", code, Redact(o.Message), o.NextAction)
}
