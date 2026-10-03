// 本文件只读展示决策记录。
package manage

import (
	servertypes "github.com/digitalwayhk/core/pkg/server/types"
	managepkg "github.com/digitalwayhk/core/service/manage"
	"github.com/digitalwayhk/core/service/manage/view"
	"github.com/vincent-lxc/pulse-operator/operator/models"
)

// DecisionManage 列出决策。
type DecisionManage struct {
	*managepkg.ManageService[models.DecisionRecord]
}

// NewDecisionManage 创建决策管理并传入最终 owner。
func NewDecisionManage() *DecisionManage {
	own := &DecisionManage{}
	own.ManageService = managepkg.NewManageService[models.DecisionRecord](own)
	return own
}

// GetList 使用 models 层的统一列表。
func (*DecisionManage) GetList() interface{} {
	return models.NewManageModelList[models.DecisionRecord]()
}

// Routers 只暴露查看和查询。
func (own *DecisionManage) Routers() []servertypes.IRouter {
	return []servertypes.IRouter{own.View, own.Search}
}

// ViewModel 设置菜单标题。
func (own *DecisionManage) ViewModel(model *view.ViewModel) {
	model.Title = "Decisions"
	model.AutoLoad = true
}

// GetTitle 返回中文菜单名。
func (*DecisionManage) GetTitle() string { return "决策" }

// GetLocaleTitle 按语言返回菜单名。
func (own *DecisionManage) GetLocaleTitle(locale string) string {
	return localeTitle(locale, own.GetTitle(), "Decisions")
}
