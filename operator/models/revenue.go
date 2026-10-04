// 本文件保存观察到的 USDC 流入，来源是链上日志或 HTTP 补录。
package models

import (
	"strings"

	"github.com/digitalwayhk/core/pkg/utils"
)

// Revenue 是一笔收入观察。
type Revenue struct {
	*BusinessModel
	Code          string `gorm:"uniqueIndex" json:"code" desc:"来源键"`
	Source        string `json:"source" desc:"chain 或 http"`
	Ref           string `json:"ref" desc:"交易哈希或手工参考号"`
	FromAddress   string `json:"fromAddress" desc:"付款方"`
	AmountUnits   string `json:"amountUnits" desc:"金额（USDC 最小单位）"`
	ObservedAt    string `json:"observedAt" desc:"观察时间"`
	Memo          string `json:"memo" desc:"备注"`
	Reconciled    bool   `json:"reconciled" desc:"是否已计入某轮余额"`
	CircleProduct string `json:"circle" desc:"Circle 产品"`
}

// NewRevenue 创建完整初始化的收入记录。
func NewRevenue() *Revenue {
	return &Revenue{BusinessModel: NewBusinessModel()}
}

// NewModel 供 ModelList 反射初始化继承链。
func (own *Revenue) NewModel() {
	if own.BusinessModel == nil || own.ServiceModel == nil || own.Model == nil {
		own.BusinessModel = NewBusinessModel()
	}
}

// GetHash 以来源键作为唯一哈希。
func (own *Revenue) GetHash() string {
	if strings.TrimSpace(own.Code) == "" {
		return ""
	}
	return utils.HashCodes("revenue", own.Code)
}

// InsertRevenue 写入一笔收入。相同编码已存在时忽略。
func InsertRevenue(source, ref, from, amount, observedAt, memo, circle string) (*Revenue, error) {
	code := source + ":" + strings.ToLower(strings.TrimSpace(ref))
	existing, err := findRevenue(code)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}
	row := NewRevenue()
	row.Code = code
	row.Source = source
	row.Ref = ref
	row.FromAddress = from
	row.AmountUnits = amount
	row.ObservedAt = observedAt
	row.Memo = memo
	row.CircleProduct = circle
	touchNew(row.SetID, row.SetCreatedAt, row.SetUpdatedAt, row.SetHashcode, row.GetHash(), 0)
	if err := getDataAction().Insert(row); err != nil {
		return nil, err
	}
	return row, nil
}

// ListRevenues 返回全部收入观察。
func ListRevenues() ([]*Revenue, error) {
	if err := ensureModel(NewRevenue()); err != nil {
		return nil, err
	}
	var rows []*Revenue
	err := getDataAction().Load(newSearch(NewRevenue(), 500), &rows)
	return rows, err
}

func findRevenue(code string) (*Revenue, error) {
	if err := ensureModel(NewRevenue()); err != nil {
		return nil, err
	}
	search := newSearch(NewRevenue(), 5)
	search.AddWhereN("Code", code)
	var rows []*Revenue
	if err := getDataAction().Load(search, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// MarkRevenueReconciled 标记这笔手工收入已经计入一轮评估。
func MarkRevenueReconciled(code string) error {
	row, err := findRevenue(code)
	if err != nil || row == nil {
		return err
	}
	row.Reconciled = true
	return getDataAction().Update(row)
}
