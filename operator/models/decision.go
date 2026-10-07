// 本文件保存每一条处置。decisionHash 相同的记录只保留第一笔。
package models

import (
	"strings"

	"github.com/digitalwayhk/core/pkg/utils"
)

// DecisionRecord 是一轮循环里的一条决策。
type DecisionRecord struct {
	*BusinessModel
	Code          string `gorm:"uniqueIndex" json:"code" desc:"decisionHash"`
	RunID         string `json:"runID" desc:"运行编号"`
	PayableCode   string `json:"payableCode" desc:"应付编号"`
	Action        string `json:"action" desc:"动作"`
	ReasonCode    string `json:"reasonCode" desc:"原因码"`
	Reason        string `json:"reason" desc:"原因"`
	CategoryCode  string `json:"categoryCode" desc:"品类"`
	Payee         string `json:"payee" desc:"收款地址"`
	AmountUnits   string `json:"amountUnits" desc:"金额（USDC 最小单位）"`
	TxHash        string `json:"txHash" desc:"交易哈希"`
	Outcome       string `json:"outcome" desc:"结果"`
	RequestID     string `json:"requestID" desc:"链上审批号"`
	SoftNote      string `json:"softNote" desc:"软判断备注"`
	CircleProduct string `json:"circle" desc:"Circle 产品"`
	CircleTxID    string `json:"circle_tx_id" desc:"Circle 交易号"`
	CircleState   string `json:"circle_state" desc:"Circle 状态"`
	Rationale     string `json:"rationale" desc:"模型理由"`
	ModelID       string `json:"model_id" desc:"模型"`
	PlannerAction string `json:"planner_action" desc:"模型动作"`
	PromptHash    string `json:"prompt_hash" desc:"提示哈希"`
	RiskNotes     string `json:"risk_notes" desc:"风险备注"`
	Confidence    string `json:"confidence" desc:"置信度"`
	PlannerRaw    string `json:"planner_raw" desc:"模型原文"`
	LatencyMS     int64  `json:"latency_ms" desc:"模型耗时毫秒"`
}

// NewDecisionRecord 创建完整初始化的决策。
func NewDecisionRecord() *DecisionRecord {
	return &DecisionRecord{BusinessModel: NewBusinessModel()}
}

// NewModel 供 ModelList 反射初始化继承链。
func (own *DecisionRecord) NewModel() {
	if own.BusinessModel == nil || own.ServiceModel == nil || own.Model == nil {
		own.BusinessModel = NewBusinessModel()
	}
}

// GetHash 以 decisionHash 作为唯一哈希。
func (own *DecisionRecord) GetHash() string {
	code := strings.TrimSpace(own.Code)
	if code == "" {
		return ""
	}
	return utils.HashCodes("decision", strings.ToLower(code))
}

// InsertDecision 追加决策。相同哈希已存在时返回已有行。
func InsertDecision(row *DecisionRecord) error {
	existing, err := findDecision(row.Code)
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}
	touchNew(row.SetID, row.SetCreatedAt, row.SetUpdatedAt, row.SetHashcode, row.GetHash(), row.GetID())
	return getDataAction().Insert(row)
}

// ListDecisions 返回全部决策。
func ListDecisions() ([]*DecisionRecord, error) {
	if err := ensureModel(NewDecisionRecord()); err != nil {
		return nil, err
	}
	var rows []*DecisionRecord
	err := getDataAction().Load(newSearch(NewDecisionRecord(), 500), &rows)
	return rows, err
}

// FindDecision 按 decisionHash 查找决策。
func FindDecision(code string) (*DecisionRecord, error) {
	return findDecision(code)
}

func findDecision(code string) (*DecisionRecord, error) {
	if err := ensureModel(NewDecisionRecord()); err != nil {
		return nil, err
	}
	search := newSearch(NewDecisionRecord(), 5)
	search.AddWhereN("Code", code)
	var rows []*DecisionRecord
	if err := getDataAction().Load(search, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}
