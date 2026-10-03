// 本文件只读展示应付账本。
package manage

import (
	servertypes "github.com/digitalwayhk/core/pkg/server/types"
	managepkg "github.com/digitalwayhk/core/service/manage"
	"github.com/digitalwayhk/core/service/manage/view"
	"github.com/vincent-lxc/pulse-operator/operator/models"
)

// PayableManage 列出应付。
type PayableManage struct {
	*managepkg.ManageService[models.Payable]
}

// NewPayableManage 创建应付管理并传入最终 owner。
func NewPayableManage() *PayableManage {
	own := &PayableManage{}
	own.ManageService = managepkg.NewManageService[models.Payable](own)
	return own
}

// GetList 使用 models 层的统一列表。
func (*PayableManage) GetList() interface{} { return models.NewManageModelList[models.Payable]() }

// Routers 只暴露查看和查询。
func (own *PayableManage) Routers() []servertypes.IRouter {
	return []servertypes.IRouter{own.View, own.Search}
}

// ViewModel 设置菜单标题。
func (own *PayableManage) ViewModel(model *view.ViewModel) {
	model.Title = "Payables"
	model.AutoLoad = true
}

// GetTitle 返回中文菜单名。
func (*PayableManage) GetTitle() string { return "应付" }

// GetLocaleTitle 按语言返回菜单名。
func (own *PayableManage) GetLocaleTitle(locale string) string {
	return localeTitle(locale, own.GetTitle(), "Payables")
}
