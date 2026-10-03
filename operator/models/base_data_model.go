// 本文件是品类和收款方共用的基础资料支路。
package models

import (
	"strings"

	"github.com/digitalwayhk/core/pkg/utils"
)

// BaseDataModel 保存稳定编码、名称和启停状态。
type BaseDataModel struct {
	*ServiceModel
	Code    string `gorm:"not null;uniqueIndex" json:"code" desc:"编码"`
	Name    string `json:"name" desc:"名称"`
	Enabled bool   `json:"enabled" desc:"是否启用"`
}

// NewBaseDataModel 创建基础资料。
func NewBaseDataModel() *BaseDataModel {
	return &BaseDataModel{ServiceModel: NewServiceModel()}
}

// GetHash 用规范化编码作为唯一哈希。
func (own *BaseDataModel) GetHash() string {
	code := strings.ToLower(strings.TrimSpace(own.Code))
	if code == "" {
		return ""
	}
	return utils.HashCodes(code)
}
