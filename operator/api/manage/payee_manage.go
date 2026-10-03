// 本文件只读展示品类白名单。
package manage

import (
	servertypes "github.com/digitalwayhk/core/pkg/server/types"
	managepkg "github.com/digitalwayhk/core/service/manage"
	"github.com/digitalwayhk/core/service/manage/view"
	"github.com/vincent-lxc/pulse-operator/operator/models"
)

// PayeeManage 列出收款方。
type PayeeManage struct {
	*managepkg.ManageService[models.Payee]
}

// NewPayeeManage 创建收款方管理并传入最终 owner。
func NewPayeeManage() *PayeeManage {
	own := &PayeeManage{}
	own.ManageService = managepkg.NewManageService[models.Payee](own)
	return own
}

// GetList 使用 models 层的统一列表。
func (*PayeeManage) GetList() interface{} { return models.NewManageModelList[models.Payee]() }

// Routers 只暴露查看和查询。
func (own *PayeeManage) Routers() []servertypes.IRouter {
	return []servertypes.IRouter{own.View, own.Search}
}

// ViewModel 设置菜单标题。
func (own *PayeeManage) ViewModel(model *view.ViewModel) {
	model.Title = "Payees"
	model.AutoLoad = true
}

// GetTitle 返回中文菜单名。
func (*PayeeManage) GetTitle() string { return "收款方" }

// GetLocaleTitle 按语言返回菜单名。
func (own *PayeeManage) GetLocaleTitle(locale string) string {
	return localeTitle(locale, own.GetTitle(), "Payees")
}
