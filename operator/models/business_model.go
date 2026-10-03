// 本文件是应付、收入、决策和审批共用的业务事实支路。
package models

// BusinessModel 是持续产生的业务记录基座，不继承基础资料。
type BusinessModel struct {
	*ServiceModel
}

// NewBusinessModel 创建业务事实基座。
func NewBusinessModel() *BusinessModel {
	return &BusinessModel{ServiceModel: NewServiceModel()}
}
