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
	RequestID string
	Status    string
	TxHash    string
	Circle    string
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
		return ApprovalResult{}, err
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
	if mock, ok := chain.(*treasury.MockChain); ok && cfg.ChainDriver == "mock" {
		if err := saveMockSnapshot(mockStatePath(cfg), mock.Snapshot()); err != nil {
			return ApprovalResult{}, err
		}
	}
	return ApprovalResult{RequestID: requestID, Status: res.Status, TxHash: res.TxHash, Circle: res.Product}, nil
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
	return models.SaveApproval(code, requestID, category, payee, amount, decision, state, reason, res.Product)
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
		"action":     action,
		"request_id": requestID,
		"tx_hash":    res.TxHash,
		"status":     res.Status,
		"circle":     res.Product,
		"error":      errorText(callErr),
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

func errorText(err error) string {
	if err == nil {
		return ""
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
