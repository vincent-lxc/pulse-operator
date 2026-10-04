// 本文件把链上仍未决的审批补进本地库。别处提交的请求不会出现在本进程的决策里。
package business

import (
	"strings"

	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

// SyncChainApprovals 插入或刷新链上 pending。已经批准或拒绝的本地行保持原状。
func SyncChainApprovals(items []treasury.Approval) error {
	if len(items) == 0 {
		return nil
	}
	if err := models.EnsureStorage(); err != nil {
		return err
	}
	for _, item := range items {
		if strings.TrimSpace(item.RequestID) == "" {
			continue
		}
		if item.Status != "" && item.Status != "pending" {
			continue
		}
		existing, err := models.FindApprovalByRequest(item.RequestID)
		if err != nil {
			return err
		}
		if existing != nil && existing.State != "" && existing.State != "pending" {
			continue
		}
		code := approvalCode(item, existing)
		if existing == nil {
			byCode, err := models.FindApproval(code)
			if err != nil {
				return err
			}
			if byCode != nil && byCode.RequestID != item.RequestID && byCode.State != "" && byCode.State != "pending" {
				code = "chain:" + item.RequestID
			}
		}
		circle := ""
		if existing != nil {
			circle = existing.CircleProduct
		}
		amount := treasury.FormatUSDC(item.Amount)
		if err := models.SaveApproval(code, item.RequestID, item.Category, item.Payee, amount, item.DecisionHash, "pending", item.Reason, circle, "", ""); err != nil {
			return err
		}
	}
	return nil
}

func approvalCode(item treasury.Approval, existing *models.Approval) string {
	if existing != nil && strings.TrimSpace(existing.Code) != "" {
		return existing.Code
	}
	hash := strings.TrimSpace(item.DecisionHash)
	if hash != "" && !zeroHash(hash) {
		return hash
	}
	return "chain:" + item.RequestID
}

func zeroHash(hash string) bool {
	hash = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(hash)), "0x")
	if hash == "" {
		return true
	}
	for _, c := range hash {
		if c != '0' {
			return false
		}
	}
	return true
}
