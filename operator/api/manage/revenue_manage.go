// 本文件只读展示收入观察。
package manage

import (
	servertypes "github.com/digitalwayhk/core/pkg/server/types"
	managepkg "github.com/digitalwayhk/core/service/manage"
	"github.com/digitalwayhk/core/service/manage/view"
	"github.com/vincent-lxc/pulse-operator/operator/models"
)

// RevenueManage 列出收入。
type RevenueManage struct {
	*managepkg.ManageService[models.Revenue]
}

// NewRevenueManage 创建收入管理并传入最终 owner。
func NewRevenueManage() *RevenueManage {
	own := &RevenueManage{}
	own.ManageService = managepkg.NewManageService[models.Revenue](own)
	return own
}

// GetList 使用 models 层的统一列表。
func (*RevenueManage) GetList() interface{} { return models.NewManageModelList[models.Revenue]() }

// Routers 只暴露查看和查询。
func (own *RevenueManage) Routers() []servertypes.IRouter {
	return []servertypes.IRouter{own.View, own.Search}
}

// ViewModel 设置菜单标题。
func (own *RevenueManage) ViewModel(model *view.ViewModel) {
	model.Title = "Revenue"
	model.AutoLoad = true
}

// GetTitle 返回中文菜单名。
func (*RevenueManage) GetTitle() string { return "收入" }

// GetLocaleTitle 按语言返回菜单名。
func (own *RevenueManage) GetLocaleTitle(locale string) string {
	return localeTitle(locale, own.GetTitle(), "Revenue")
}
