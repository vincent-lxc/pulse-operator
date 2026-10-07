// 本文件只读展示域名账单，包括模型给出的理由。
package manage

import (
	servertypes "github.com/digitalwayhk/core/pkg/server/types"
	managepkg "github.com/digitalwayhk/core/service/manage"
	"github.com/digitalwayhk/core/service/manage/view"
	"github.com/vincent-lxc/pulse-operator/operator/models"
)

// BillManage 列出账单。
type BillManage struct {
	*managepkg.ManageService[models.Bill]
}

// NewBillManage 创建账单管理。
func NewBillManage() *BillManage {
	own := &BillManage{}
	own.ManageService = managepkg.NewManageService[models.Bill](own)
	return own
}

// GetList 使用 models 层的统一列表。
func (*BillManage) GetList() interface{} {
	return models.NewManageModelList[models.Bill]()
}

// Routers 只暴露查看和查询。
func (own *BillManage) Routers() []servertypes.IRouter {
	return []servertypes.IRouter{own.View, own.Search}
}

// ViewModel 设置菜单标题。
func (own *BillManage) ViewModel(model *view.ViewModel) {
	model.Title = "Bills"
	model.AutoLoad = true
}

// GetTitle 返回中文菜单名。
func (*BillManage) GetTitle() string { return "账单" }

// GetLocaleTitle 按语言返回菜单名。
func (own *BillManage) GetLocaleTitle(locale string) string {
	return localeTitle(locale, own.GetTitle(), "Bills")
}
