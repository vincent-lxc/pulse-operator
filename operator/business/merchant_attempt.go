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
type merchantAttempt struct {
	N        int    `json:"n"`
	Key      string `json:"key"`
	At       string `json:"at"`
	Outcome  string `json:"outcome"`
	Reason   string `json:"reason,omitempty"`
	Checkout string `json:"checkout,omitempty"`
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
// 已有未过期的 checkout 时不换键。上一试已经确定没有可用 checkout 时才递增。
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
	if outcome == "started" && last.Outcome != "" && last.Outcome != "started" {
		if reason != "" {
			last.Reason = publicText(reason)
		}
	} else {
		last.Outcome = outcome
		last.Reason = publicText(reason)
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
	switch item.Outcome {
	case "legacy", "no_402", "idempotency_mismatch", "insufficient_funds", "expired":
		return true
	default:
		return false
	}
}

func classifyMerchantAttempt(order procurement.Order, callErr error) (string, string) {
	code := strings.ToUpper(strings.TrimSpace(order.Code))
	reason := strings.TrimSpace(strings.TrimSpace(order.Code + " " + order.Message))
	switch {
	case code == "IDEMPOTENCY_KEY_MISMATCH":
		return "idempotency_mismatch", reason
	case code == "INSUFFICIENT_FUNDS":
		return "insufficient_funds", reason
	case checkoutExpired(order):
		return "expired", reason
	case order.HTTPStatus == http.StatusPaymentRequired || code == "PAYMENT_REQUIRED" || strings.TrimSpace(order.Required) != "":
		return "payment_required", reason
	case code == "PAYMENT_PENDING" || code == "PAYMENT_IN_PROGRESS":
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

func checkoutExpired(order procurement.Order) bool {
	blob := strings.ToUpper(order.Code + " " + order.Message + " " + order.Status)
	return strings.Contains(blob, "EXPIR")
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
	}
	if strings.Contains(strings.ToUpper(paid.Code+" "+paid.Message), "EXPIR") {
		return "merchant_checkout_expired"
	}
	if paid.Required != "" || code == "PAYMENT_REQUIRED" {
		return "x402_rejected"
	}
	if code != "" {
		return "merchant_" + strings.ToLower(code)
	}
	return "x402_rejected"
}
