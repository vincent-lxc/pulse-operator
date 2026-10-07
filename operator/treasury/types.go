// 本文件定义金库快照、应付项和决策记录，供规则引擎与链客户端共用。
package treasury

import (
	"math/big"
	"time"
)

const (
	// ActionPay 表示在硬限制内自主支付。
	ActionPay = "pay"
	// ActionDefer 表示本轮不支付，留到之后的循环。
	ActionDefer = "defer"
	// ActionSweep 表示把超出储备目标的余额划到冷钱包。
	ActionSweep = "sweep_to_reserve"
	// ActionEscalate 表示交给人审批，超限时仍会提交 PolicyVault.pay。
	ActionEscalate = "escalate_to_human"
	// ActionReject 表示明确不付，并把应付标成 closed。
	ActionReject = "reject"

	// ReasonWithinPolicy 表示金额、预算、名单和储备都允许支付。
	ReasonWithinPolicy = "within_policy"
	// ReasonCooldown 表示同一收款方仍在冷却期内。
	ReasonCooldown = "cooldown"
	// ReasonNotDue 表示到期日还没到。
	ReasonNotDue = "not_due"
	// ReasonReserveFloor 表示支付后会跌破储备下限。
	ReasonReserveFloor = "reserve_floor"
	// ReasonOverTxCap 表示超过品类单笔上限。
	ReasonOverTxCap = "over_tx_cap"
	// ReasonOverBudget 表示超过本期剩余预算。
	ReasonOverBudget = "over_budget"
	// ReasonPayeeNotAllowed 表示收款方不在品类白名单。
	ReasonPayeeNotAllowed = "payee_not_allowlisted"
	// ReasonUnknownCategory 表示品类未启用。
	ReasonUnknownCategory = "unknown_category"
	// ReasonPaused 表示金库已暂停。
	ReasonPaused = "vault_paused"
	// ReasonSurplus 表示余额高于储备目标和未付义务。
	ReasonSurplus = "surplus_above_target"
	// ReasonNoSurplus 表示没有可划转的超额。
	ReasonNoSurplus = "no_surplus"
	// ReasonMaxSpend 表示本轮自主支付已经达到 maxSpendPerRun。
	ReasonMaxSpend = "max_spend_per_run"
)

// Category 是从 PolicyVault 读到的品类预算快照。
type Category struct {
	Name          string
	Enabled       bool
	Budget        *big.Int
	PerTxCap      *big.Int
	Spent         *big.Int
	Remaining     *big.Int
	PeriodSeconds uint64
	Payees        map[string]bool
}

// Inflow 是一笔观察到的 USDC 流入。
type Inflow struct {
	TxHash  string
	From    string
	Amount  *big.Int
	Block   uint64
	Source  string
	Product string
}

// Approval 是链上或本地记录的待人工审批。
type Approval struct {
	RequestID    string
	Category     string
	Payee        string
	Amount       *big.Int
	DecisionHash string
	Reason       string
	Status       string
}

// Snapshot 是一轮循环开始时的金库只读视图。
type Snapshot struct {
	Balance    *big.Int
	Paused     bool
	Reserve    string
	Categories map[string]Category
	Pending    []Approval
	Inflows    []Inflow
	Block      uint64
}

// Payable 是账本中的一笔待付。
type Payable struct {
	ID       string
	Category string
	Payee    string
	Amount   *big.Int
	Due      time.Time
	Memo     string
	Status   string
}

// Policy 是代码里的硬规则参数，LLM 不能放宽它们。
type Policy struct {
	AgentID        string
	ChainID        string
	Vault          string
	ReserveFloor   *big.Int
	ReserveTarget  *big.Int
	Horizon        time.Duration
	Cooldown       time.Duration
	Now            time.Time
	MaxSpendPerRun *big.Int
}

// Decision 是一条机器可读的处置结果。
type Decision struct {
	PayableID     string
	Action        string
	ReasonCode    string
	Reason        string
	Category      string
	Payee         string
	Amount        *big.Int
	DecisionHash  string
	Submit        bool
	Outcome       string
	TxHash        string
	RequestID     string
	SoftNote      string
	Calldata      string
	Product       string
	CircleTxID    string `json:"circle_tx_id,omitempty"`
	CircleState   string `json:"circle_state,omitempty"`
	Planner       string `json:"planner,omitempty"`
	ModelID       string `json:"model_id,omitempty"`
	PlannerAction string `json:"planner_action,omitempty"`
	Rationale     string `json:"rationale,omitempty"`
	PromptHash    string `json:"prompt_hash,omitempty"`
	RiskNotes     string `json:"risk_notes,omitempty"`
	Confidence    string `json:"confidence,omitempty"`
	Disagree      bool   `json:"disagree,omitempty"`
	LatencyMS     int64  `json:"latency_ms,omitempty"`
	PlannerRaw    string `json:"planner_raw,omitempty"`
}

// Liquidity 是余额、即将到期义务和储备目标的对照。
type Liquidity struct {
	Balance       *big.Int
	Obligations   *big.Int
	ReserveFloor  *big.Int
	ReserveTarget *big.Int
	Surplus       *big.Int
	Headroom      *big.Int
}

// Notice 是发给通知钩子的一条消息。
type Notice struct {
	Kind string
	Text string
}

// Report 是一轮循环的完整结果。
type Report struct {
	RunID            string
	ObservedAt       time.Time
	Balance          *big.Int
	OpeningBalance   *big.Int
	Block            uint64
	Inflows          []Inflow
	Liquidity        Liquidity
	OpeningLiquidity Liquidity
	Decisions        []Decision
	Pending          []Approval
	Notices          []Notice
	Categories       map[string]Category
	PayeePaidAt      map[string]time.Time
}
