// 本文件保存品类白名单收款方，以及最近一次自主支付时间（冷却用）。
package models

import "strings"

// Payee 是某个品类下的收款地址。
type Payee struct {
	*BaseDataModel
	CategoryCode string `json:"categoryCode" desc:"品类编码"`
	Address      string `json:"address" desc:"收款地址"`
	LastPaidAt   string `json:"lastPaidAt" desc:"最近自主支付时间"`
}

// NewPayee 创建完整初始化的收款方。
func NewPayee() *Payee {
	return &Payee{BaseDataModel: NewBaseDataModel()}
}

// NewModel 供 ModelList 反射初始化继承链。
func (own *Payee) NewModel() {
	if own.BaseDataModel == nil || own.ServiceModel == nil || own.Model == nil {
		own.BaseDataModel = NewBaseDataModel()
	}
}

// PayeeCode 用品类和地址组成稳定编码。
func PayeeCode(category, address string) string {
	return strings.ToLower(strings.TrimSpace(category)) + ":" + strings.ToLower(strings.TrimSpace(address))
}

// SavePayee 插入或更新收款方。
func SavePayee(category, address string, allowed bool, lastPaidAt string) error {
	code := PayeeCode(category, address)
	existing, err := findPayee(code)
	if err != nil {
		return err
	}
	if existing == nil {
		row := NewPayee()
		row.Code = code
		row.Name = address
		row.Enabled = allowed
		row.CategoryCode = category
		row.Address = address
		row.LastPaidAt = lastPaidAt
		touchNew(row.SetID, row.SetCreatedAt, row.SetUpdatedAt, row.SetHashcode, row.GetHash(), 0)
		return getDataAction().Insert(row)
	}
	existing.Enabled = allowed
	existing.Address = address
	if lastPaidAt != "" {
		existing.LastPaidAt = lastPaidAt
	}
	return getDataAction().Update(existing)
}

// ListPayees 返回全部收款方。
func ListPayees() ([]*Payee, error) {
	if err := ensureModel(NewPayee()); err != nil {
		return nil, err
	}
	var rows []*Payee
	err := getDataAction().Load(newSearch(NewPayee(), 500), &rows)
	return rows, err
}

func findPayee(code string) (*Payee, error) {
	if err := ensureModel(NewPayee()); err != nil {
		return nil, err
	}
	search := newSearch(NewPayee(), 5)
	search.AddWhereN("Code", code)
	var rows []*Payee
	if err := getDataAction().Load(search, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}
