// 本文件保存每一轮的流动性快照，供只读看板使用。
package models

import (
	"strings"

	"github.com/digitalwayhk/core/pkg/utils"
)

// CycleSnapshot 是一轮循环的余额和储备对照。
type CycleSnapshot struct {
	*BusinessModel
	Code               string `gorm:"uniqueIndex" json:"code" desc:"运行编号"`
	BalanceUnits       string `json:"balanceUnits" desc:"余额（USDC 最小单位）"`
	ObligationUnits    string `json:"obligationUnits" desc:"窗口内未付（USDC 最小单位）"`
	ReserveFloorUnits  string `json:"reserveFloorUnits" desc:"储备下限"`
	ReserveTargetUnits string `json:"reserveTargetUnits" desc:"储备目标"`
	SurplusUnits       string `json:"surplusUnits" desc:"可划转超额"`
	ObservedAt         string `json:"observedAt" desc:"观察时间"`
	CircleProduct      string `json:"circle" desc:"Circle 产品"`
}

// NewCycleSnapshot 创建完整初始化的快照。
func NewCycleSnapshot() *CycleSnapshot {
	return &CycleSnapshot{BusinessModel: NewBusinessModel()}
}

// NewModel 供 ModelList 反射初始化继承链。
func (own *CycleSnapshot) NewModel() {
	if own.BusinessModel == nil || own.ServiceModel == nil || own.Model == nil {
		own.BusinessModel = NewBusinessModel()
	}
}

// GetHash 以运行编号作为唯一哈希。
func (own *CycleSnapshot) GetHash() string {
	if strings.TrimSpace(own.Code) == "" {
		return ""
	}
	return utils.HashCodes("cycle", own.Code)
}

// InsertCycle 写入一轮快照。
func InsertCycle(row *CycleSnapshot) error {
	touchNew(row.SetID, row.SetCreatedAt, row.SetUpdatedAt, row.SetHashcode, row.GetHash(), 0)
	return getDataAction().Insert(row)
}

// ListCycles 返回全部快照。
func ListCycles() ([]*CycleSnapshot, error) {
	if err := ensureModel(NewCycleSnapshot()); err != nil {
		return nil, err
	}
	var rows []*CycleSnapshot
	err := getDataAction().Load(newSearch(NewCycleSnapshot(), 500), &rows)
	return rows, err
}
