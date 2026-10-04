// 本文件由 owner 签名批准或拒绝 PolicyVault 上的待审批，并写入审计。
package business

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

// ApprovalResult 是一次 approve 或 reject 的结果。
type ApprovalResult struct {
	RequestID   string
	Status      string
	TxHash      string
	Circle      string
	CircleTxID  string
	CircleState string
}

// SettleApproval 对一笔链上请求执行 approve 或 reject。action 只能是 approve 或 reject。
func SettleApproval(ctx context.Context, cfg treasury.Config, requestID, action string) (ApprovalResult, error) {
	if !cycleMu.TryLock() {
		return ApprovalResult{}, models.NewBusinessError("operator cycle already running")
	}
	defer cycleMu.Unlock()
	requestID = strings.TrimSpace(requestID)
	if _, err := treasury.ParseRequestID(requestID); err != nil {
		return ApprovalResult{}, models.NewValidationError(err.Error())
	}
	action = strings.ToLower(strings.TrimSpace(action))
	if action != "approve" && action != "reject" {
		return ApprovalResult{}, models.NewValidationError("action must be approve or reject")
	}
	if err := models.EnsureStorage(); err != nil {
		return ApprovalResult{}, err
	}
	chain, closeChain, err := openChain(ctx, cfg, nil)
	if err != nil {
		return ApprovalResult{}, err
	}
	defer closeChain()
	sender, ok := chain.(treasury.ApprovalSender)
	if !ok {
		return ApprovalResult{}, models.NewBusinessError("this executor cannot settle approvals")
	}
	var res treasury.ExecResult
	if action == "approve" {
		res, err = sender.Approve(ctx, requestID)
	} else {
		res, err = sender.Reject(ctx, requestID)
	}
	if err != nil {
		_ = auditApproval(cfg, requestID, action, res, err)
		return ApprovalResult{}, classifyChainError(err)
	}
	if res.Product == "" {
		res.Product = cfg.ExecutorProduct()
	}
	if err := auditApproval(cfg, requestID, action, res, nil); err != nil {
		return ApprovalResult{}, err
	}
	if err := rememberApproval(requestID, action, res); err != nil {
		return ApprovalResult{}, err
	}
	if err := settlePayable(requestID, action, res); err != nil {
		return ApprovalResult{}, err
	}
	if mock, ok := chain.(*treasury.MockChain); ok && cfg.ChainDriver == "mock" {
		if err := saveMockSnapshot(mockStatePath(cfg), mock.Snapshot()); err != nil {
			return ApprovalResult{}, err
		}
	}
	return ApprovalResult{
		RequestID: requestID, Status: res.Status, TxHash: res.TxHash, Circle: res.Product,
		CircleTxID: res.CircleTxID, CircleState: res.CircleState,
	}, nil
}

// classifyChainError 把已解开的合约回退变成 422 业务错误。其他错误保持原样。
func classifyChainError(err error) error {
	if reason := treasury.RevertReason(err); reason != "" {
		return models.NewBusinessError(reason)
	}
	return err
}

func settlePayable(requestID, action string, res treasury.ExecResult) error {
	state, ok := payableAfterSettlement(action, res.Status)
	if !ok {
		return nil
	}
	code, err := payableCodeForRequest(requestID)
	if err != nil || code == "" {
		return err
	}
	return models.UpdatePayableSettlement(code, state, res.TxHash)
}

func payableAfterSettlement(action, status string) (string, bool) {
	if strings.HasPrefix(status, "dry_run") {
		return "", false
	}
	switch action {
	case "approve":
		return "paid", true
	case "reject":
		return "closed", true
	default:
		return "", false
	}
}

func payableCodeForRequest(requestID string) (string, error) {
	row, err := models.FindApprovalByRequest(requestID)
	if err != nil || row == nil {
		return "", err
	}
	if row.DecisionHash != "" {
		dec, err := models.FindDecision(row.DecisionHash)
		if err != nil {
			return "", err
		}
		if dec != nil && dec.PayableCode != "" && dec.PayableCode != "cycle" {
			return dec.PayableCode, nil
		}
	}
	rows, err := models.ListPayables()
	if err != nil {
		return "", err
	}
	match := ""
	for _, p := range rows {
		if p.State != "escalated" || p.CategoryCode != row.CategoryCode || p.AmountUnits != row.AmountUnits {
			continue
		}
		if treasury.NormalizeAddress(p.Payee) != treasury.NormalizeAddress(row.Payee) {
			continue
		}
		if match != "" {
			return "", nil
		}
		match = p.Code
	}
	return match, nil
}

func rememberApproval(requestID, action string, res treasury.ExecResult) error {
	row, err := models.FindApprovalByRequest(requestID)
	if err != nil {
		return err
	}
	code := "request:" + requestID
	category, payee, amount, decision, reason := "", "", "0", "", action
	if row != nil {
		code = row.Code
		category = row.CategoryCode
		payee = row.Payee
		amount = row.AmountUnits
		decision = row.DecisionHash
		reason = row.ReasonCode
	}
	state := res.Status
	if state == "" {
		state = action + "d"
	}
	return models.SaveApproval(code, requestID, category, payee, amount, decision, state, reason, res.Product, res.CircleTxID, res.CircleState)
}

func auditApproval(cfg treasury.Config, requestID, action string, res treasury.ExecResult, callErr error) error {
	if cfg.AuditLog == "" {
		return nil
	}
	log, err := treasury.OpenAudit(cfg.AuditLog)
	if err != nil {
		return err
	}
	outcome := res.Status
	if callErr != nil {
		outcome = "error"
	}
	payload, err := json.Marshal(map[string]string{
		"action":       action,
		"request_id":   requestID,
		"tx_hash":      res.TxHash,
		"status":       res.Status,
		"circle":       res.Product,
		"circle_tx_id": res.CircleTxID,
		"circle_state": res.CircleState,
		"error":        auditErrorText(callErr),
	})
	if err != nil {
		return err
	}
	return log.Append(treasury.AuditEvent{
		Kind:    "approval",
		TxHash:  res.TxHash,
		Outcome: outcome,
		Circle:  firstCircle(res.Product, cfg.ExecutorProduct()),
		Payload: payload,
	})
}

func auditErrorText(err error) string {
	if err == nil {
		return ""
	}
	if reason := treasury.RevertReason(err); reason != "" {
		return reason
	}
	return err.Error()
}

func firstCircle(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
