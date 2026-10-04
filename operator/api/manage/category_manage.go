// 本文件只读展示品类预算快照。写入由循环完成。
package manage

import (
	servertypes "github.com/digitalwayhk/core/pkg/server/types"
	managepkg "github.com/digitalwayhk/core/service/manage"
	"github.com/digitalwayhk/core/service/manage/view"
	"github.com/vincent-lxc/pulse-operator/operator/models"
)

// CategoryManage 列出支出品类。
type CategoryManage struct {
	*managepkg.ManageService[models.SpendCategory]
}

// NewCategoryManage 创建品类管理并传入最终 owner。
func NewCategoryManage() *CategoryManage {
	own := &CategoryManage{}
	own.ManageService = managepkg.NewManageService[models.SpendCategory](own)
	return own
}

// GetList 使用 models 层的统一列表。
func (*CategoryManage) GetList() interface{} {
	return models.NewManageModelList[models.SpendCategory]()
}

// Routers 只暴露查看和查询。
func (own *CategoryManage) Routers() []servertypes.IRouter {
	return []servertypes.IRouter{own.View, own.Search}
}

// ViewModel 设置菜单标题。
func (own *CategoryManage) ViewModel(model *view.ViewModel) {
	model.Title = "Categories"
	model.AutoLoad = true
}

// GetTitle 返回中文菜单名。
func (*CategoryManage) GetTitle() string { return "品类" }

// GetLocaleTitle 按语言返回菜单名。
func (own *CategoryManage) GetLocaleTitle(locale string) string {
	return localeTitle(locale, own.GetTitle(), "Categories")
}
