// 本文件保存需要人工处理的审批，包括链上 pending 和本地拒绝代付。
package models

import (
	"strings"

	"github.com/digitalwayhk/core/pkg/utils"
)

// Approval 是一条待人工确认的请求。
type Approval struct {
	*BusinessModel
	Code          string `gorm:"uniqueIndex" json:"code" desc:"审批键"`
	RequestID     string `json:"requestID" desc:"链上请求号"`
	CategoryCode  string `json:"categoryCode" desc:"品类"`
	Payee         string `json:"payee" desc:"收款地址"`
	AmountUnits   string `json:"amountUnits" desc:"金额（USDC 最小单位）"`
	DecisionHash  string `json:"decisionHash" desc:"decisionHash"`
	State         string `json:"state" desc:"状态"`
	ReasonCode    string `json:"reasonCode" desc:"原因码"`
	CircleProduct string `json:"circle" desc:"Circle 产品"`
}

// NewApproval 创建完整初始化的审批。
func NewApproval() *Approval {
	return &Approval{BusinessModel: NewBusinessModel()}
}

// NewModel 供 ModelList 反射初始化继承链。
func (own *Approval) NewModel() {
	if own.BusinessModel == nil || own.ServiceModel == nil || own.Model == nil {
		own.BusinessModel = NewBusinessModel()
	}
}

// GetHash 以审批键作为唯一哈希。
func (own *Approval) GetHash() string {
	if strings.TrimSpace(own.Code) == "" {
		return ""
	}
	return utils.HashCodes("approval", strings.ToLower(own.Code))
}

// SaveApproval 插入或更新审批。
func SaveApproval(code, requestID, category, payee, amount, decisionHash, state, reason, circle string) error {
	existing, err := findApproval(code)
	if err != nil {
		return err
	}
	if existing == nil {
		row := NewApproval()
		row.Code = code
		row.RequestID = requestID
		row.CategoryCode = category
		row.Payee = payee
		row.AmountUnits = amount
		row.DecisionHash = decisionHash
		row.State = state
		row.ReasonCode = reason
		row.CircleProduct = circle
		touchNew(row.SetID, row.SetCreatedAt, row.SetUpdatedAt, row.SetHashcode, row.GetHash(), 0)
		return getDataAction().Insert(row)
	}
	existing.State = state
	existing.RequestID = requestID
	existing.ReasonCode = reason
	return getDataAction().Update(existing)
}

// ListApprovals 返回全部审批。
func ListApprovals() ([]*Approval, error) {
	if err := ensureModel(NewApproval()); err != nil {
		return nil, err
	}
	var rows []*Approval
	err := getDataAction().Load(newSearch(NewApproval(), 500), &rows)
	return rows, err
}

// FindApprovalByRequest 按链上请求号查找审批。优先返回仍 pending 的一行。
func FindApprovalByRequest(requestID string) (*Approval, error) {
	rows, err := ListApprovals()
	if err != nil {
		return nil, err
	}
	var fallback *Approval
	for _, row := range rows {
		if row.RequestID != requestID {
			continue
		}
		if row.State == "pending" {
			return row, nil
		}
		fallback = row
	}
	return fallback, nil
}

func findApproval(code string) (*Approval, error) {
	if err := ensureModel(NewApproval()); err != nil {
		return nil, err
	}
	search := newSearch(NewApproval(), 5)
	search.AddWhereN("Code", code)
	var rows []*Approval
	if err := getDataAction().Load(search, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}
