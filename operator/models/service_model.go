// 本文件是 operator 全部模型共享的服务级持久化基座，不是基础资料。
package models

import "github.com/digitalwayhk/core/pkg/persistence/entity"

const databaseName = "operator"

// ServiceModel 承载库名，不保存请求或用户状态。
type ServiceModel struct {
	*entity.Model
}

// NewServiceModel 创建已初始化的服务基座。
func NewServiceModel() *ServiceModel {
	return &ServiceModel{Model: entity.NewModel()}
}

// GetLocalDBName 返回本服务的本地库名。
func (own *ServiceModel) GetLocalDBName() string { return databaseName }

// GetRemoteDBName 与本地库同名，单机演示不拆远程库。
func (own *ServiceModel) GetRemoteDBName() string { return databaseName }
