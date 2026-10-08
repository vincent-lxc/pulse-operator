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

// CollectInput 是一次域名付款。
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
}

// Collect 先下单，遇到 PAYMENT-REQUIRED 再签名并重试。
func Collect(ctx context.Context, client *Porkbun, in CollectInput) (Order, error) {
	if client == nil {
		return Order{}, errString("porkbun client is missing")
	}
	renew := strings.Contains(in.Kind, "renew")
	call := func(checkout, signature string) (Order, error) {
		if renew {
			return client.Renew(ctx, in.Domain, in.CostCents, in.Years, false, checkout, in.Idempotency, signature)
		}
		return client.Create(ctx, in.Domain, in.CostCents, in.Years, false, checkout, in.Idempotency, signature)
	}
	order, err := call(in.CheckoutID, "")
	if order.Code == "PAYMENT_IN_PROGRESS" || order.Code == "PAYMENT_PENDING" {
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
	payment, err := in.Sign(item, in.Now)
	if err != nil {
		return order, err
	}
	signed, err := call(order.CheckoutID, payment.Header)
	if err != nil && signed.Code != "PAYMENT_IN_PROGRESS" {
		return signed, err
	}
	if signed.Required == "" {
		signed.Required = order.Required
	}
	if signed.CheckoutID == "" {
		signed.CheckoutID = order.CheckoutID
	}
	signed.X402URL = first(signed.X402URL, order.X402URL)
	return signed, nil
}

func paymentRequired(order Order) bool {
	return order.HTTPStatus == 402 || order.Code == "PAYMENT_REQUIRED" || order.Required != ""
}

func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

type strErr string

func (e strErr) Error() string { return string(e) }

func errString(s string) error { return strErr(s) }
