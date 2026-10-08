// 本文件把 Porkbun 的 402 收成一次 x402 签名。正在处理的 checkout 不会再签一次。
package procurement

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"time"
)

// ErrCheckoutNeedsReview 表示这个 checkout 已经签过，Porkbun 却又要求一笔新的 x402。
// 不能再签。需要人核对这笔付款。
var ErrCheckoutNeedsReview = errors.New("checkout_needs_review")

// SignedCheckout 是已经签好、还没提交的 checkout。没有签名，也没有密钥。
type SignedCheckout struct {
	CheckoutID  string
	ValidBefore int64
	Nonce       string
	Payer       string
}

// CollectInput 是一次域名付款。
// BeforeSign 在签名之前调用。它返回错误时不签名，也不提交。
// OnSigned 在签名成功后、带 PAYMENT-SIGNATURE 的请求发出前调用。
// 它返回错误时不再提交签名。
type CollectInput struct {
	Domain      string
	Kind        string
	CostCents   int64
	Years       int
	CheckoutID  string
	Idempotency string
	Network     string
	Asset       string
	Now         time.Time
	Sign        func(Accept, time.Time) (Payment, error)
	BeforeSign  func() error
	OnSigned    func(SignedCheckout) error
}

// Collect 先下单，遇到 PAYMENT-REQUIRED 再签名并重试。
func Collect(ctx context.Context, client *Porkbun, in CollectInput) (Order, error) {
	if client == nil {
		return Order{}, errString("porkbun client is missing")
	}
	renew := strings.Contains(in.Kind, "renew")
	call := func(checkout, signature, idem string) (Order, error) {
		if renew {
			return client.Renew(ctx, in.Domain, in.CostCents, in.Years, false, checkout, idem, signature)
		}
		return client.Create(ctx, in.Domain, in.CostCents, in.Years, false, checkout, idem, signature)
	}
	// 带 usdcCheckoutId 的确认轮询不能复用签名重试的 Idempotency-Key。
	// Porkbun 在第一次非 402 应答后缓存那个键，随后的轮询会得到 IDEMPOTENCY_KEY_MISMATCH。
	idem := in.Idempotency
	if strings.TrimSpace(in.CheckoutID) != "" {
		idem = confirmIdempotencyKey(in.Idempotency)
	}
	order, err := call(in.CheckoutID, "", idem)
	if paymentInFlight(order.Code) {
		return order, nil
	}
	if err != nil && !paymentRequired(order) {
		return order, err
	}
	if order.OrderID != "" && order.HTTPStatus < 300 {
		return order, nil
	}
	if order.KeptAsCredit {
		return order, nil
	}
	if !paymentRequired(order) {
		if err != nil {
			return order, err
		}
		return order, nil
	}
	if strings.TrimSpace(in.CheckoutID) != "" {
		if order.CheckoutID == "" {
			order.CheckoutID = in.CheckoutID
		}
		return order, ErrCheckoutNeedsReview
	}
	if in.Sign == nil {
		return order, errString("x402 signer is missing")
	}
	req, err := ParsePaymentRequired(order.Required)
	if err != nil {
		return order, err
	}
	amount := CentsToUSDC(in.CostCents)
	item, err := Select(req, PayPolicy{
		Networks: []string{in.Network},
		Asset:    in.Asset,
		Amount:   amount,
		Schemes:  []string{"exact", "auth-capture"},
	})
	if err != nil {
		return order, err
	}
	if new(big.Int).Set(amount).Cmp(mustInt(item.Amount)) != 0 {
		return order, errString("x402 amount diverged from the bill")
	}
	if in.BeforeSign != nil {
		if hookErr := in.BeforeSign(); hookErr != nil {
			return order, hookErr
		}
	}
	payment, err := in.Sign(item, in.Now)
	if err != nil {
		return order, err
	}
	if in.OnSigned != nil {
		if hookErr := in.OnSigned(SignedCheckout{
			CheckoutID:  order.CheckoutID,
			ValidBefore: payment.ValidBefore,
			Nonce:       payment.Nonce,
			Payer:       payment.Payer,
		}); hookErr != nil {
			return order, hookErr
		}
	}
	// 签名重试必须和触发 402 的请求体完全相同：payWith:usdc，不带 usdcCheckoutId，同一个 Idempotency-Key。
	signed, err := call("", payment.Header, in.Idempotency)
	signed = keepCheckout(signed, order)
	if err != nil && !paymentInFlight(signed.Code) {
		return signed, err
	}
	return signed, nil
}

// confirmIdempotencyKey 是确认轮询的键。空键保持为空，请求不带 Idempotency-Key。
func confirmIdempotencyKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	return key + "-confirm"
}

func keepCheckout(signed, first Order) Order {
	if signed.Required == "" {
		signed.Required = first.Required
	}
	if signed.CheckoutID == "" {
		signed.CheckoutID = first.CheckoutID
	}
	signed.X402URL = firstNonEmpty(signed.X402URL, first.X402URL)
	return signed
}

func paymentInFlight(code string) bool {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "PAYMENT_IN_PROGRESS", "PAYMENT_PENDING", "IDEMPOTENCY_KEY_IN_USE":
		return true
	default:
		return false
	}
}

func paymentRequired(order Order) bool {
	return order.HTTPStatus == 402 || order.Code == "PAYMENT_REQUIRED" || order.Required != ""
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

type strErr string

func (e strErr) Error() string { return string(e) }

func errString(s string) error { return strErr(s) }
