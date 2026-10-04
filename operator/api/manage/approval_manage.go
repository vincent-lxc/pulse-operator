// 本文件只读展示待人工审批。
package manage

import (
	"context"
	"os"
	"strings"

	servertypes "github.com/digitalwayhk/core/pkg/server/types"
	managepkg "github.com/digitalwayhk/core/service/manage"
	"github.com/digitalwayhk/core/service/manage/view"
	"github.com/vincent-lxc/pulse-operator/operator/api/dto"
	"github.com/vincent-lxc/pulse-operator/operator/business"
	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
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

// Routers 暴露查看、查询，以及选中一行后的批准和拒绝。
func (own *ApprovalManage) Routers() []servertypes.IRouter {
	return []servertypes.IRouter{own.View, own.Search, &Approve{manage: own}, &Reject{manage: own}}
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

// Approve 是审批列表上的批准命令。选中行的 requestID 会被提交。
type Approve struct {
	manage    *ApprovalManage
	RequestID string `json:"requestID"`
	AltID     string `json:"request_id"`
}

// Reject 是审批列表上的拒绝命令。
type Reject struct {
	manage    *ApprovalManage
	RequestID string `json:"requestID"`
	AltID     string `json:"request_id"`
}

func (own *Approve) GetInstance() interface{} {
	if own.manage != nil {
		return own.manage
	}
	return own
}

func (own *Reject) GetInstance() interface{} {
	if own.manage != nil {
		return own.manage
	}
	return own
}

func (own *Approve) Parse(req servertypes.IRequest) error { return bindManageRequest(req, own) }
func (own *Reject) Parse(req servertypes.IRequest) error  { return bindManageRequest(req, own) }

func (own *Approve) Validation(servertypes.IRequest) error {
	return requireManageRequest(own.RequestID)
}
func (own *Reject) Validation(servertypes.IRequest) error {
	return requireManageRequest(own.RequestID)
}

func (own *Approve) Do(servertypes.IRequest) (interface{}, error) {
	return settleManage(own.RequestID, "approve")
}
func (own *Reject) Do(servertypes.IRequest) (interface{}, error) {
	return settleManage(own.RequestID, "reject")
}

func (own *Approve) GetResponse() interface{} { return &dto.ApprovalActionResponse{} }
func (own *Reject) GetResponse() interface{}  { return &dto.ApprovalActionResponse{} }

func (own *Approve) RouterInfo() *servertypes.RouterInfo { return managepkg.RouterInfo(own) }
func (own *Reject) RouterInfo() *servertypes.RouterInfo  { return managepkg.RouterInfo(own) }

func bindManageRequest(req servertypes.IRequest, dest interface{}) error {
	var body struct {
		RequestID string `json:"requestID"`
		AltID     string `json:"request_id"`
	}
	if err := req.Bind(&body); err != nil {
		return err
	}
	id := body.RequestID
	if id == "" {
		id = body.AltID
	}
	switch row := dest.(type) {
	case *Approve:
		row.RequestID = id
	case *Reject:
		row.RequestID = id
	}
	return nil
}

func requireManageRequest(id string) error {
	if strings.TrimSpace(id) == "" {
		return models.NewValidationError("requestID is required")
	}
	return nil
}

func settleManage(requestID, action string) (interface{}, error) {
	cfg, err := loadManageConfig()
	if err != nil {
		return nil, err
	}
	res, err := business.SettleApproval(context.Background(), cfg, requestID, action)
	if err != nil {
		return nil, err
	}
	return &dto.ApprovalActionResponse{RequestID: res.RequestID, Status: res.Status, TxHash: res.TxHash, Circle: res.Circle}, nil
}

func loadManageConfig() (treasury.Config, error) {
	path := os.Getenv("OPERATOR_CONFIG")
	if path == "" {
		path = "config/dry-run.yaml"
	}
	return treasury.LoadConfig(path)
}
