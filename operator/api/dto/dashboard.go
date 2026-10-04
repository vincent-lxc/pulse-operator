// 本文件是看板和收入接口的对外 DTO，不暴露持久化模型。
package dto

import "github.com/vincent-lxc/pulse-operator/operator/business"

// DashboardResponse 是只读看板。
type DashboardResponse struct {
	Balance     string                  `json:"balance" desc:"余额"`
	Obligations string                  `json:"obligations" desc:"窗口内未付"`
	Surplus     string                  `json:"surplus" desc:"可划转超额"`
	Circle      string                  `json:"circle" desc:"Circle 产品"`
	Categories  []business.CategoryView `json:"categories" desc:"品类预算"`
	Decisions   []business.DecisionView `json:"decisions" desc:"决策"`
	Approvals   []business.ApprovalView `json:"approvals" desc:"待审批"`
	Revenues    []business.RevenueView  `json:"revenues" desc:"收入"`
}

// NewDashboardResponse 从业务快照复制公开字段。
func NewDashboardResponse(view business.Dashboard) *DashboardResponse {
	return &DashboardResponse{
		Balance:     view.Balance,
		Obligations: view.Obligations,
		Surplus:     view.Surplus,
		Circle:      view.CircleProduct,
		Categories:  view.Categories,
		Decisions:   view.Decisions,
		Approvals:   view.Approvals,
		Revenues:    view.Revenues,
	}
}

// RevenueResponse 是手工收入的回执。
type RevenueResponse struct {
	Status string `json:"status" desc:"accepted"`
	Ref    string `json:"ref" desc:"参考号"`
	Circle string `json:"circle,omitempty" desc:"Circle 产品"`
}

// RunOnceResponse 是手动触发一轮后的摘要。
type RunOnceResponse struct {
	RunID     string `json:"runID" desc:"运行编号"`
	Decisions int    `json:"decisions" desc:"决策条数"`
}

// ApprovalActionResponse 是 owner approve 或 reject 的回执。
type ApprovalActionResponse struct {
	RequestID   string `json:"requestID" desc:"链上请求号"`
	Status      string `json:"status" desc:"结果"`
	TxHash      string `json:"txHash" desc:"交易哈希"`
	Circle      string `json:"circle" desc:"Circle 产品"`
	CircleTxID  string `json:"circle_tx_id,omitempty" desc:"Circle 交易号"`
	CircleState string `json:"circle_state,omitempty" desc:"Circle 状态"`
}
