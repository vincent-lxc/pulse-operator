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
// checkoutID 只用于轮询已经在别处付过的 checkout，并且会和 payWith:usdc 一起发送。
// 签过名的重试不能带 checkoutID：它必须和拿到 402 的那次请求体相同。
func (p *Porkbun) Create(ctx context.Context, domain string, costCents int64, years int, dryRun bool, checkoutID, idempotency, signature string) (Order, error) {
	raw, err := p.encodePurchase(costCents, years, dryRun, checkoutID)
	if err != nil {
		return Order{}, err
	}
	return p.postBytes(ctx, "/domain/create/"+domain, raw, signature, idempotency)
}

// Renew 续费域名。checkoutID 的规则和 Create 相同。
func (p *Porkbun) Renew(ctx context.Context, domain string, costCents int64, years int, dryRun bool, checkoutID, idempotency, signature string) (Order, error) {
	raw, err := p.encodePurchase(costCents, years, dryRun, checkoutID)
	if err != nil {
		return Order{}, err
	}
	return p.postBytes(ctx, "/domain/renew/"+domain, raw, signature, idempotency)
}

// purchaseBody 的字段顺序是固定的，同一次购买的两次编码字节相同。
// 报价不带 payWith。真正购买带 payWith:usdc。
// 轮询再追加 usdcCheckoutId，并且仍然带 payWith:usdc。
type purchaseBody struct {
	APIKey       string `json:"apikey"`
	SecretAPIKey string `json:"secretapikey"`
	Years        int    `json:"years"`
	AgreeToTerms string `json:"agreeToTerms"`
	Cost         int64  `json:"cost"`
	DryRun       bool   `json:"dryRun,omitempty"`
	PayWith      string `json:"payWith,omitempty"`
	CheckoutID   string `json:"usdcCheckoutId,omitempty"`
}

func (p *Porkbun) encodePurchase(costCents int64, years int, dryRun bool, checkoutID string) ([]byte, error) {
	_ = years
	// /domain/create 把 agreeToTerms 列为必填，dry-run 示例同样带 "yes"。
	// /domain/renew 的文档没有单独要求它；报价和真实请求共用这份请求体，所以续费也带上。
	body := purchaseBody{
		APIKey: p.APIKey, SecretAPIKey: p.Secret,
		Years: 1, AgreeToTerms: "yes",
	}
	if dryRun {
		body.DryRun = true
		body.Cost = 0
		return json.Marshal(body)
	}
	body.Cost = costCents
	body.PayWith = "usdc"
	body.CheckoutID = strings.TrimSpace(checkoutID)
	return json.Marshal(body)
}

func (p *Porkbun) post(ctx context.Context, path string, body map[string]any, signature string, idempotency ...string) (Order, error) {
	payload := map[string]any{"apikey": p.APIKey, "secretapikey": p.Secret}
	for k, v := range body {
		payload[k] = v
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Order{}, err
	}
	return p.postBytes(ctx, path, raw, signature, idempotency...)
}

func (p *Porkbun) postBytes(ctx context.Context, path string, raw []byte, signature string, idempotency ...string) (Order, error) {
	if err := p.wait(ctx); err != nil {
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

// AccountBalance 读取账户余额（美分）。规格是 GET /account/balance，密钥放在头里。
func (p *Porkbun) AccountBalance(ctx context.Context) (int64, error) {
	buf, status, err := p.get(ctx, "/account/balance")
	if err != nil {
		return 0, err
	}
	var body struct {
		Status  string `json:"status"`
		Balance *int64 `json:"balance"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(buf, &body); err != nil {
		return 0, fmt.Errorf("porkbun balance: %s", Redact(err.Error(), p.APIKey, p.Secret))
	}
	if status >= 400 || (body.Status != "" && body.Status != "SUCCESS") || body.Balance == nil {
		code := body.Code
		if code == "" {
			code = body.Status
		}
		if code == "" {
			code = fmt.Sprintf("http_%d", status)
		}
		return 0, fmt.Errorf("porkbun balance %s: %s", code, Redact(body.Message, p.APIKey, p.Secret))
	}
	return *body.Balance, nil
}

// AccountHasDomain 查看 /domain/listAll 里有没有这个域名。查询失败时不把“没有”当成结论。
func (p *Porkbun) AccountHasDomain(ctx context.Context, domain string) (bool, error) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return false, fmt.Errorf("domain is missing")
	}
	start := 0
	for page := 0; page < 20; page++ {
		names, err := p.listDomainPage(ctx, domain, start)
		if err != nil {
			return false, err
		}
		for _, name := range names {
			if strings.EqualFold(strings.TrimSpace(name), domain) {
				return true, nil
			}
		}
		if len(names) < 1000 {
			return false, nil
		}
		start += len(names)
	}
	return false, fmt.Errorf("domain list was truncated")
}

func (p *Porkbun) listDomainPage(ctx context.Context, domain string, start int) ([]string, error) {
	raw, err := json.Marshal(struct {
		APIKey       string `json:"apikey"`
		SecretAPIKey string `json:"secretapikey"`
		Start        int    `json:"start"`
		Domain       string `json:"domain"`
	}{APIKey: p.APIKey, SecretAPIKey: p.Secret, Start: start, Domain: domain})
	if err != nil {
		return nil, err
	}
	order, err := p.postBytes(ctx, "/domain/listAll", raw, "")
	if err != nil {
		return nil, err
	}
	var body struct {
		Status  string `json:"status"`
		Domains []struct {
			Domain string `json:"domain"`
		} `json:"domains"`
	}
	if err := json.Unmarshal(order.raw, &body); err != nil {
		return nil, fmt.Errorf("porkbun domain list: %s", Redact(err.Error(), p.APIKey, p.Secret))
	}
	if body.Status != "" && body.Status != "SUCCESS" {
		return nil, fmt.Errorf("porkbun domain list %s", body.Status)
	}
	names := make([]string, 0, len(body.Domains))
	for _, item := range body.Domains {
		names = append(names, item.Domain)
	}
	return names, nil
}

func (p *Porkbun) get(ctx context.Context, path string) ([]byte, int, error) {
	if err := p.wait(ctx); err != nil {
		return nil, 0, err
	}
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = "https://api.porkbun.com/api/json/v3"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("X-API-Key", p.APIKey)
	req.Header.Set("X-Secret-API-Key", p.Secret)
	client := p.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("porkbun: %s", Redact(err.Error(), p.APIKey, p.Secret))
	}
	defer res.Body.Close()
	buf, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, res.StatusCode, err
	}
	return buf, res.StatusCode, nil
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
