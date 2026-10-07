// 本文件保存一张域名账单从报价到付款的全过程。
package models

import (
	"strings"

	"github.com/digitalwayhk/core/pkg/utils"
)

// Bill 是一笔 Porkbun 域名采购。决策哈希在付款前写入，重试沿用同一哈希。
type Bill struct {
	*BusinessModel
	Code                string `gorm:"uniqueIndex" json:"code" desc:"账单编号"`
	Vendor              string `json:"vendor" desc:"供应商"`
	Kind                string `json:"kind" desc:"类型"`
	Domain              string `json:"domain" desc:"域名"`
	Years               int    `json:"years" desc:"年数"`
	QuoteCents          int64  `json:"quote_cents" desc:"报价（分）"`
	Currency            string `json:"currency" desc:"币种"`
	CategoryCode        string `json:"categoryCode" desc:"品类"`
	State               string `json:"state" desc:"状态"`
	Mode                string `json:"mode" desc:"模式"`
	DecisionHash        string `json:"decision_hash" desc:"决策哈希"`
	Action              string `json:"action" desc:"动作"`
	ReasonCode          string `json:"reasonCode" desc:"原因码"`
	Reason              string `json:"reason" desc:"原因"`
	Rationale           string `json:"rationale" desc:"模型理由"`
	ModelID             string `json:"model_id" desc:"模型"`
	PlannerAction       string `json:"planner_action" desc:"模型动作"`
	PromptHash          string `json:"prompt_hash" desc:"提示哈希"`
	RiskNotes           string `json:"risk_notes" desc:"风险备注"`
	Confidence          string `json:"confidence" desc:"置信度"`
	PlannerRaw          string `json:"planner_raw" desc:"模型原文"`
	LatencyMS           int64  `json:"latency_ms" desc:"模型耗时毫秒"`
	AmountUnits         string `json:"amountUnits" desc:"金库金额"`
	FeeAllowanceUnits   string `json:"feeAllowanceUnits" desc:"手续费余量"`
	VaultTx             string `json:"vault_tx" desc:"Arc 金库交易"`
	CCTPBurnTx          string `json:"cctp_burn_tx" desc:"CCTP burn"`
	CCTPMessageHash     string `json:"cctp_message" desc:"CCTP 消息"`
	CCTPNonce           string `json:"cctp_nonce" desc:"CCTP nonce"`
	BaseMintTx          string `json:"base_mint_tx" desc:"Base 铸出交易"`
	ForwardFeeUnits     string `json:"forward_fee" desc:"转发费"`
	X402Scheme          string `json:"x402_scheme" desc:"x402 方案"`
	X402Payer           string `json:"x402_payer" desc:"x402 付款人"`
	X402Receipt         string `json:"x402_receipt" desc:"x402 回执"`
	PorkbunCheckoutID   string `json:"porkbun_checkout_id" desc:"Porkbun checkout"`
	PorkbunOrderID      string `json:"porkbun_order_id" desc:"Porkbun 订单"`
	PorkbunBalanceCents int64  `json:"porkbun_balance_cents" desc:"Porkbun 余额（分）"`
	CircleTxIDs         string `json:"circle_tx_ids" desc:"Circle 交易号"`
	ArcURL              string `json:"arc_url" desc:"Arc 浏览器"`
	BaseURL             string `json:"base_url" desc:"Base 浏览器"`
	PorkbunURL          string `json:"porkbun_url" desc:"Porkbun"`
	PaidAt              string `json:"paidAt" desc:"付款时间"`
	Evidence            string `json:"evidence" desc:"证据"`
}

// NewBill 创建一张空账单。
func NewBill() *Bill {
	return &Bill{BusinessModel: NewBusinessModel(), State: "quoted", Currency: "USDC", Vendor: "porkbun", Years: 1}
}

// NewModel 供 ModelList 反射初始化继承链。
func (own *Bill) NewModel() {
	if own.BusinessModel == nil || own.ServiceModel == nil || own.Model == nil {
		own.BusinessModel = NewBusinessModel()
	}
}

// GetHash 以账单编号作为唯一哈希。
func (own *Bill) GetHash() string {
	code := strings.TrimSpace(own.Code)
	if code == "" {
		return ""
	}
	return utils.HashCodes("bill", code)
}

// InsertBill 追加账单。编号已存在时返回已有行，不覆盖状态。
func InsertBill(row *Bill) (*Bill, error) {
	existing, err := FindBill(row.Code)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}
	if row.BusinessModel == nil {
		row.BusinessModel = NewBusinessModel()
	}
	if row.State == "" {
		row.State = "quoted"
	}
	touchNew(row.SetID, row.SetCreatedAt, row.SetUpdatedAt, row.SetHashcode, row.GetHash(), row.GetID())
	if err := getDataAction().Insert(row); err != nil {
		return nil, err
	}
	return row, nil
}

// SaveBill 写回一张已经存在的账单。
func SaveBill(row *Bill) error {
	if row == nil {
		return NewBusinessError("账单为空")
	}
	return getDataAction().Update(row)
}

// FindBill 按编号查找账单。
func FindBill(code string) (*Bill, error) {
	if err := ensureModel(NewBill()); err != nil {
		return nil, err
	}
	search := newSearch(NewBill(), 5)
	search.AddWhereN("Code", strings.TrimSpace(code))
	var rows []*Bill
	if err := getDataAction().Load(search, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// ListBills 返回全部账单。
func ListBills() ([]*Bill, error) {
	if err := ensureModel(NewBill()); err != nil {
		return nil, err
	}
	var rows []*Bill
	err := getDataAction().Load(newSearch(NewBill(), 500), &rows)
	return rows, err
}
