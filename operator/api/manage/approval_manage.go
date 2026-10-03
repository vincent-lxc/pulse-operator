// 本文件只读展示待人工审批。
package manage

import (
	servertypes "github.com/digitalwayhk/core/pkg/server/types"
	managepkg "github.com/digitalwayhk/core/service/manage"
	"github.com/digitalwayhk/core/service/manage/view"
	"github.com/vincent-lxc/pulse-operator/operator/models"
)

// ApprovalManage 列出审批。
type ApprovalManage struct {
	*managepkg.ManageService[models.Approval]
}

// NewApprovalManage 创建审批管理并传入最终 owner。
func NewApprovalManage() *ApprovalManage {
	own := &ApprovalManage{}
	own.ManageService = managepkg.NewManageService[models.Approval](own)
	return own
}

// GetList 使用 models 层的统一列表。
func (*ApprovalManage) GetList() interface{} { return models.NewManageModelList[models.Approval]() }

// Routers 只暴露查看和查询。
func (own *ApprovalManage) Routers() []servertypes.IRouter {
	return []servertypes.IRouter{own.View, own.Search}
}

// ViewModel 设置菜单标题。
func (own *ApprovalManage) ViewModel(model *view.ViewModel) {
	model.Title = "Approvals"
	model.AutoLoad = true
}

// GetTitle 返回中文菜单名。
func (*ApprovalManage) GetTitle() string { return "待审批" }

// GetLocaleTitle 按语言返回菜单名。
func (own *ApprovalManage) GetLocaleTitle(locale string) string {
	return localeTitle(locale, own.GetTitle(), "Approvals")
}
