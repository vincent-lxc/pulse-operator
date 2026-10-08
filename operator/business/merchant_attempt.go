package business

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/procurement"
)

// merchantAttempt 是一次 Porkbun create/renew。同一个 N 的网络重试复用 Key。
// Signed 为真表示授权已经签好，并且在带签名的请求发出前落了库。
// ValidBefore 和 Nonce 是 authorization 里的字段，不是签名，也不是密钥。
type merchantAttempt struct {
	N           int    `json:"n"`
	Key         string `json:"key"`
	At          string `json:"at"`
	Outcome     string `json:"outcome"`
	Reason      string `json:"reason,omitempty"`
	Checkout    string `json:"checkout,omitempty"`
	Signed      bool   `json:"signed,omitempty"`
	ValidBefore int64  `json:"valid_before,omitempty"`
	Nonce       string `json:"nonce,omitempty"`
}

func legacyMerchantKey(code string) string {
	return "bill-" + strings.TrimSpace(code)
}

func merchantAttemptKey(code string, n int) string {
	return fmt.Sprintf("bill-%s-a%d", strings.TrimSpace(code), n)
}

func parseMerchantAttempts(raw string) ([]merchantAttempt, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []merchantAttempt
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("merchant attempts: %w", err)
	}
	return out, nil
}

func writeMerchantAttempts(bill *models.Bill, attempts []merchantAttempt) error {
	raw, err := json.Marshal(attempts)
	if err != nil {
		return err
	}
	bill.MerchantAttempts = string(raw)
	return nil
}

// prepareMerchantKey 在调用 Porkbun 前定下本次尝试的 Idempotency-Key，并先落库。
// 已经签过的尝试不换键。未签名的上一试已经确定没有可用 checkout 时才递增。
func prepareMerchantKey(bill *models.Bill) (string, error) {
	if bill == nil || strings.TrimSpace(bill.Code) == "" {
		return "", fmt.Errorf("bill id is missing")
	}
	attempts, err := parseMerchantAttempts(bill.MerchantAttempts)
	if err != nil {
		return "", err
	}
	if checkoutBlocksNewKey(bill, attempts) {
		if len(attempts) == 0 {
			return legacyMerchantKey(bill.Code), nil
		}
		return attempts[len(attempts)-1].Key, nil
	}
	if len(attempts) == 0 && legacyAttemptUsed(bill) {
		attempts = append(attempts, merchantAttempt{
			N: 1, Key: legacyMerchantKey(bill.Code), At: time.Now().UTC().Format(time.RFC3339),
			Outcome: "legacy", Reason: "earlier run used the legacy idempotency key",
		})
	}
	if len(attempts) == 0 || merchantAttemptUnusable(attempts[len(attempts)-1]) {
		n := 1
		if len(attempts) > 0 {
			n = attempts[len(attempts)-1].N + 1
		}
		bill.PorkbunCheckoutID = ""
		attempts = append(attempts, merchantAttempt{
			N: n, Key: merchantAttemptKey(bill.Code, n), At: time.Now().UTC().Format(time.RFC3339), Outcome: "started",
		})
		if err := writeMerchantAttempts(bill, attempts); err != nil {
			return "", err
		}
		if err := models.SaveBill(bill); err != nil {
			return "", err
		}
		return attempts[len(attempts)-1].Key, nil
	}
	return attempts[len(attempts)-1].Key, nil
}

func finishMerchantAttempt(bill *models.Bill, key string, order procurement.Order, callErr error) error {
	if bill == nil {
		return nil
	}
	attempts, err := parseMerchantAttempts(bill.MerchantAttempts)
	if err != nil {
		return err
	}
	if len(attempts) == 0 || attempts[len(attempts)-1].Key != key {
		n := 1
		if len(attempts) > 0 {
			n = attempts[len(attempts)-1].N + 1
		}
		attempts = append(attempts, merchantAttempt{
			N: n, Key: key, At: time.Now().UTC().Format(time.RFC3339), Outcome: "started",
		})
	}
	last := &attempts[len(attempts)-1]
	outcome, reason := classifyMerchantAttempt(order, callErr)
	reason = publicText(reason)
	switch {
	case last.Signed && paymentExpired(order) && authorizationLapsed(last.ValidBefore, time.Now()):
		last.Outcome = "expired"
		last.Reason = reason
	case last.Signed && (outcome == "accepted" || outcome == "pending"):
		last.Outcome = outcome
		last.Reason = reason
	case last.Signed:
		if reason != "" {
			last.Reason = reason
		}
		if last.Outcome == "" || last.Outcome == "started" {
			last.Outcome = "signed"
		}
	case outcome == "started" && last.Outcome != "" && last.Outcome != "started":
		if reason != "" {
			last.Reason = reason
		}
	default:
		last.Outcome = outcome
		last.Reason = reason
	}
	if strings.TrimSpace(order.CheckoutID) != "" {
		last.Checkout = order.CheckoutID
	}
	if err := writeMerchantAttempts(bill, attempts); err != nil {
		return err
	}
	return models.SaveBill(bill)
}

func checkoutBlocksNewKey(bill *models.Bill, attempts []merchantAttempt) bool {
	if len(attempts) > 0 && attempts[len(attempts)-1].Signed {
		last := attempts[len(attempts)-1]
		return !(last.Outcome == "expired" && authorizationLapsed(last.ValidBefore, time.Now()))
	}
	if bill == nil || strings.TrimSpace(bill.PorkbunCheckoutID) == "" {
		return false
	}
	if len(attempts) == 0 {
		return true
	}
	switch attempts[len(attempts)-1].Outcome {
	case "expired", "idempotency_mismatch", "insufficient_funds":
		return false
	default:
		return true
	}
}

func legacyAttemptUsed(bill *models.Bill) bool {
	if bill == nil || strings.TrimSpace(bill.PorkbunCheckoutID) != "" {
		return false
	}
	return bill.State == "failed_merchant"
}

func merchantAttemptUnusable(item merchantAttempt) bool {
	if item.Signed {
		return item.Outcome == "expired" && authorizationLapsed(item.ValidBefore, time.Now())
	}
	switch item.Outcome {
	case "legacy", "no_402", "idempotency_mismatch", "insufficient_funds", "expired":
		return true
	default:
		return false
	}
}

// markAttemptSigned 在带签名的请求发出前把 checkout 和签名标记写入账单。
// 保存失败时恢复内存，避免下一轮把没发出去的授权当成已经签过。
func markAttemptSigned(bill *models.Bill, signed procurement.SignedCheckout) error {
	if bill == nil {
		return fmt.Errorf("bill is missing")
	}
	previousAttempts := bill.MerchantAttempts
	previousCheckout := bill.PorkbunCheckoutID
	attempts, err := parseMerchantAttempts(bill.MerchantAttempts)
	if err != nil {
		return err
	}
	if len(attempts) == 0 {
		return fmt.Errorf("merchant attempt is missing")
	}
	last := &attempts[len(attempts)-1]
	last.Signed = true
	if last.Outcome == "" || last.Outcome == "started" {
		last.Outcome = "signed"
	}
	if signed.ValidBefore > 0 {
		last.ValidBefore = signed.ValidBefore
	}
	nonce := strings.TrimSpace(signed.Nonce)
	if nonce != "" && nonce != "<nil>" {
		last.Nonce = nonce
	}
	if id := strings.TrimSpace(signed.CheckoutID); id != "" {
		last.Checkout = id
		bill.PorkbunCheckoutID = id
	}
	if err := writeMerchantAttempts(bill, attempts); err != nil {
		bill.MerchantAttempts = previousAttempts
		bill.PorkbunCheckoutID = previousCheckout
		return err
	}
	if err := models.SaveBill(bill); err != nil {
		bill.MerchantAttempts = previousAttempts
		bill.PorkbunCheckoutID = previousCheckout
		return err
	}
	return nil
}

func merchantAttemptSigned(bill *models.Bill) bool {
	if bill == nil {
		return false
	}
	attempts, err := parseMerchantAttempts(bill.MerchantAttempts)
	if err != nil || len(attempts) == 0 {
		return false
	}
	return attempts[len(attempts)-1].Signed
}

func classifyMerchantAttempt(order procurement.Order, callErr error) (string, string) {
	code := strings.ToUpper(strings.TrimSpace(order.Code))
	reason := strings.TrimSpace(strings.TrimSpace(order.Code + " " + order.Message))
	switch {
	case code == "IDEMPOTENCY_KEY_MISMATCH":
		return "idempotency_mismatch", reason
	case code == "INSUFFICIENT_FUNDS":
		return "insufficient_funds", reason
	case code == "PAYMENT_EXPIRED":
		return "expired", reason
	case order.HTTPStatus == http.StatusPaymentRequired || code == "PAYMENT_REQUIRED" || strings.TrimSpace(order.Required) != "":
		return "payment_required", reason
	case code == "PAYMENT_PENDING" || code == "PAYMENT_IN_PROGRESS" || code == "IDEMPOTENCY_KEY_IN_USE":
		return "pending", reason
	case callErr == nil && (order.OrderID != "" || (order.HTTPStatus > 0 && order.HTTPStatus < 300)):
		return "accepted", reason
	case order.HTTPStatus == 0 || order.HTTPStatus >= 500:
		return "started", reason
	case callErr != nil:
		return "no_402", reason
	default:
		return "accepted", reason
	}
}

func merchantPending(code string) bool {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "PAYMENT_PENDING", "PAYMENT_IN_PROGRESS", "IDEMPOTENCY_KEY_IN_USE":
		return true
	default:
		return false
	}
}

func paymentExpired(order procurement.Order) bool {
	return strings.EqualFold(strings.TrimSpace(order.Code), "PAYMENT_EXPIRED")
}

func authorizationLapsed(validBefore int64, now time.Time) bool {
	return validBefore > 0 && now.Unix() >= validBefore
}

func merchantReasonCode(err error, paid merchantResult) string {
	if errors.Is(err, procurement.ErrUnsupportedEscrow) {
		return "x402_unsupported_escrow"
	}
	code := strings.ToUpper(strings.TrimSpace(paid.Code))
	switch code {
	case "IDEMPOTENCY_KEY_MISMATCH":
		return "merchant_idempotency_mismatch"
	case "INSUFFICIENT_FUNDS":
		return "merchant_insufficient_funds"
	case "PAYMENT_EXPIRED":
		return "merchant_checkout_expired"
	}
	if code != "" && code != "PAYMENT_REQUIRED" {
		return "merchant_" + strings.ToLower(code)
	}
	return "x402_rejected"
}
