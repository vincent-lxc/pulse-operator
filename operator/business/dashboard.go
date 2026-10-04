// 本文件汇总只读看板需要的余额、预算、决策和待审批。
package business

import (
	"context"
	"os"
	"time"

	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

// Dashboard 是管理界面之外的只读 JSON 视图。
type Dashboard struct {
	Balance       string         `json:"balance"`
	Obligations   string         `json:"obligations"`
	Surplus       string         `json:"surplus"`
	CircleProduct string         `json:"circle"`
	Categories    []CategoryView `json:"categories"`
	Decisions     []DecisionView `json:"decisions"`
	Approvals     []ApprovalView `json:"approvals"`
	Revenues      []RevenueView  `json:"revenues"`
}

// CategoryView 是品类预算的公开字段。
type CategoryView struct {
	Code      string `json:"code"`
	Remaining string `json:"remaining"`
	Budget    string `json:"budget"`
	PerTxCap  string `json:"perTxCap"`
	Enabled   bool   `json:"enabled"`
}

// DecisionView 是最近决策的公开字段。
type DecisionView struct {
	Action        string `json:"action"`
	Payable       string `json:"payable"`
	Amount        string `json:"amount"`
	ReasonCode    string `json:"reasonCode"`
	Outcome       string `json:"outcome"`
	Hash          string `json:"hash"`
	TxHash        string `json:"txHash"`
	CircleProduct string `json:"circle"`
	CircleTxID    string `json:"circle_tx_id"`
	CircleState   string `json:"circle_state"`
}

// ApprovalView 是待人工处理的公开字段。
type ApprovalView struct {
	RequestID     string `json:"requestID"`
	Category      string `json:"category"`
	Payee         string `json:"payee"`
	Amount        string `json:"amount"`
	State         string `json:"state"`
	Reason        string `json:"reason"`
	CircleProduct string `json:"circle"`
	CircleTxID    string `json:"circle_tx_id"`
	CircleState   string `json:"circle_state"`
}

// RevenueView 是收入观察的公开字段。
type RevenueView struct {
	Source        string `json:"source"`
	Ref           string `json:"ref"`
	Amount        string `json:"amount"`
	From          string `json:"from"`
	CircleProduct string `json:"circle"`
}

// LoadDashboard 从已落库的模型组装看板。
func LoadDashboard() (Dashboard, error) {
	var view Dashboard
	cycles, err := models.ListCycles()
	if err != nil {
		return view, err
	}
	if len(cycles) > 0 {
		last := cycles[len(cycles)-1]
		view.Balance = last.BalanceUnits
		view.Obligations = last.ObligationUnits
		view.Surplus = last.SurplusUnits
		view.CircleProduct = last.CircleProduct
	}
	refreshDashboard(&view)
	cats, err := models.ListSpendCategories()
	if err != nil {
		return view, err
	}
	for _, c := range cats {
		view.Categories = append(view.Categories, CategoryView{
			Code: c.Code, Remaining: c.RemainingUnits, Budget: c.BudgetUnits, PerTxCap: c.PerTxCapUnits, Enabled: c.Enabled,
		})
	}
	decisions, err := models.ListDecisions()
	if err != nil {
		return view, err
	}
	for _, d := range decisions {
		view.Decisions = append(view.Decisions, DecisionView{
			Action: d.Action, Payable: d.PayableCode, Amount: d.AmountUnits, ReasonCode: d.ReasonCode, Outcome: d.Outcome, Hash: d.Code, TxHash: d.TxHash, CircleProduct: d.CircleProduct, CircleTxID: d.CircleTxID, CircleState: d.CircleState,
		})
	}
	approvals, err := models.ListApprovals()
	if err != nil {
		return view, err
	}
	for _, a := range approvals {
		if a.State != "pending" {
			continue
		}
		view.Approvals = append(view.Approvals, ApprovalView{
			RequestID: a.RequestID, Category: a.CategoryCode, Payee: a.Payee, Amount: a.AmountUnits, State: a.State, Reason: a.ReasonCode, CircleProduct: a.CircleProduct, CircleTxID: a.CircleTxID, CircleState: a.CircleState,
		})
	}
	revenues, err := models.ListRevenues()
	if err != nil {
		return view, err
	}
	for _, r := range revenues {
		view.Revenues = append(view.Revenues, RevenueView{Source: r.Source, Ref: r.Ref, Amount: r.AmountUnits, From: r.FromAddress, CircleProduct: r.CircleProduct})
	}
	return view, nil
}

// refreshDashboard 用当前配置覆盖上一轮留下的 circle，并尽力同步链上 pending。
// 拨号失败不影响看板。这里不加循环锁，避免和正在进行的 approve 互相等待。
func refreshDashboard(view *Dashboard) {
	cfg, err := loadRuntimeConfig()
	if err != nil {
		return
	}
	if product := cfg.ExecutorProduct(); product != "" {
		view.CircleProduct = product
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	chain, closeChain, err := openChain(ctx, cfg, nil)
	if err != nil {
		return
	}
	defer closeChain()
	src, ok := chain.(treasury.PendingSource)
	if !ok {
		return
	}
	items, err := src.ListPending(ctx)
	if err != nil {
		return
	}
	_ = SyncChainApprovals(items)
}

func loadRuntimeConfig() (treasury.Config, error) {
	path := os.Getenv("OPERATOR_CONFIG")
	if path == "" {
		path = "config/dry-run.yaml"
	}
	return treasury.LoadConfig(path)
}

// RecordRevenue 写入一笔手工收入，下一轮循环会计入未对账余额。
func RecordRevenue(ref, from, amountUSDC, memo string) error {
	amount, err := parsePositive(amountUSDC)
	if err != nil {
		return err
	}
	_, err = models.InsertRevenue("http", ref, from, amount, "", memo, "local:http")
	return err
}
