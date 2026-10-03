// 本文件保存从金库读到的品类预算快照，供管理界面列出。
package models

import "strings"

// SpendCategory 是支出品类基础资料。
type SpendCategory struct {
	*BaseDataModel
	BudgetUnits    string `json:"budgetUnits" desc:"本期预算（USDC 最小单位）"`
	PerTxCapUnits  string `json:"perTxCapUnits" desc:"单笔上限（USDC 最小单位）"`
	SpentUnits     string `json:"spentUnits" desc:"本期已花（USDC 最小单位）"`
	RemainingUnits string `json:"remainingUnits" desc:"本期剩余（USDC 最小单位）"`
	PeriodSeconds  int64  `json:"periodSeconds" desc:"账期秒数"`
}

// NewSpendCategory 创建完整初始化的品类。
func NewSpendCategory() *SpendCategory {
	return &SpendCategory{BaseDataModel: NewBaseDataModel()}
}

// NewModel 供 ModelList 反射初始化继承链。
func (own *SpendCategory) NewModel() {
	if own.BaseDataModel == nil || own.ServiceModel == nil || own.Model == nil {
		own.BaseDataModel = NewBaseDataModel()
	}
}

// SaveSpendCategory 按编码插入或更新品类快照。
func SaveSpendCategory(code, name, budget, cap, spent, remaining string, period int64, enabled bool) error {
	existing, err := findSpendCategory(code)
	if err != nil {
		return err
	}
	if existing == nil {
		row := NewSpendCategory()
		row.Code = strings.ToLower(strings.TrimSpace(code))
		row.Name = name
		row.Enabled = enabled
		row.BudgetUnits = budget
		row.PerTxCapUnits = cap
		row.SpentUnits = spent
		row.RemainingUnits = remaining
		row.PeriodSeconds = period
		touchNew(row.SetID, row.SetCreatedAt, row.SetUpdatedAt, row.SetHashcode, row.GetHash(), 0)
		return getDataAction().Insert(row)
	}
	existing.Name = name
	existing.Enabled = enabled
	existing.BudgetUnits = budget
	existing.PerTxCapUnits = cap
	existing.SpentUnits = spent
	existing.RemainingUnits = remaining
	existing.PeriodSeconds = period
	return getDataAction().Update(existing)
}

// ListSpendCategories 返回全部品类快照。
func ListSpendCategories() ([]*SpendCategory, error) {
	if err := ensureModel(NewSpendCategory()); err != nil {
		return nil, err
	}
	var rows []*SpendCategory
	err := getDataAction().Load(newSearch(NewSpendCategory(), 500), &rows)
	return rows, err
}

func findSpendCategory(code string) (*SpendCategory, error) {
	if err := ensureModel(NewSpendCategory()); err != nil {
		return nil, err
	}
	search := newSearch(NewSpendCategory(), 5)
	search.AddWhereN("Code", strings.ToLower(strings.TrimSpace(code)))
	var rows []*SpendCategory
	if err := getDataAction().Load(search, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}
