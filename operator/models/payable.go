// 本文件保存可导入的应付账本。状态只能由循环推进。
package models

import (
	"strings"

	"github.com/digitalwayhk/core/pkg/utils"
)

// Payable 是一笔待付业务事实。
type Payable struct {
	*BusinessModel
	Code         string `gorm:"uniqueIndex" json:"code" desc:"应付编号"`
	CategoryCode string `json:"categoryCode" desc:"品类"`
	Payee        string `json:"payee" desc:"收款地址"`
	AmountUnits  string `json:"amountUnits" desc:"金额（USDC 最小单位）"`
	DueAt        string `json:"dueAt" desc:"到期时间"`
	Memo         string `json:"memo" desc:"备注"`
	State        string `json:"state" desc:"状态"`
}

// NewPayable 创建完整初始化的应付。
func NewPayable() *Payable {
	return &Payable{BusinessModel: NewBusinessModel(), State: "pending"}
}

// NewModel 供 ModelList 反射初始化继承链。
func (own *Payable) NewModel() {
	if own.BusinessModel == nil || own.ServiceModel == nil || own.Model == nil {
		own.BusinessModel = NewBusinessModel()
	}
}

// GetHash 以应付编号作为唯一哈希。
func (own *Payable) GetHash() string {
	code := strings.TrimSpace(own.Code)
	if code == "" {
		return ""
	}
	return utils.HashCodes("payable", code)
}

// UpsertPayable 按编号导入应付。已存在时不覆盖终端状态。
func UpsertPayable(code, category, payee, amount, due, memo string) error {
	existing, err := FindPayable(code)
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}
	row := NewPayable()
	row.Code = strings.TrimSpace(code)
	row.CategoryCode = category
	row.Payee = payee
	row.AmountUnits = amount
	row.DueAt = due
	row.Memo = memo
	row.State = "pending"
	touchNew(row.SetID, row.SetCreatedAt, row.SetUpdatedAt, row.SetHashcode, row.GetHash(), 0)
	return getDataAction().Insert(row)
}

// UpdatePayableState 更新应付状态。
func UpdatePayableState(code, state string) error {
	row, err := FindPayable(code)
	if err != nil || row == nil {
		return err
	}
	row.State = state
	return getDataAction().Update(row)
}

// FindPayable 按编号查找应付。
func FindPayable(code string) (*Payable, error) {
	if err := ensureModel(NewPayable()); err != nil {
		return nil, err
	}
	search := newSearch(NewPayable(), 5)
	search.AddWhereN("Code", strings.TrimSpace(code))
	var rows []*Payable
	if err := getDataAction().Load(search, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// ListPayables 返回全部应付。
func ListPayables() ([]*Payable, error) {
	if err := ensureModel(NewPayable()); err != nil {
		return nil, err
	}
	var rows []*Payable
	err := getDataAction().Load(newSearch(NewPayable(), 500), &rows)
	return rows, err
}
