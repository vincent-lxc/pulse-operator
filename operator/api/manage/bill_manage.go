// 本文件展示域名账单，并允许所有者处理还没进金库的升级账单。
package manage

import (
	"context"
	"fmt"
	"strings"

	servertypes "github.com/digitalwayhk/core/pkg/server/types"
	managepkg "github.com/digitalwayhk/core/service/manage"
	"github.com/digitalwayhk/core/service/manage/view"
	"github.com/vincent-lxc/pulse-operator/operator/access"
	"github.com/vincent-lxc/pulse-operator/operator/api/dto"
	"github.com/vincent-lxc/pulse-operator/operator/business"
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

// Routers 暴露查看、查询、重开和关闭。账单批准按钮只在 dry-run 出现。
func (own *BillManage) Routers() []servertypes.IRouter {
	routes := []servertypes.IRouter{own.View, own.Search}
	if billApproveButton() {
		routes = append(routes, &BillApprove{manage: own})
	}
	return append(routes, &BillReopen{manage: own}, &BillClose{manage: own})
}

func billApproveButton() bool {
	cfg, err := loadManageConfig()
	if err != nil {
		return false
	}
	return business.AdminBillApproveAllowed(cfg.Mode)
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

// BillApprove 把一张离链升级的账单送进金库。
type BillApprove struct {
	manage *BillManage
	Code   string `json:"code"`
	ID     string `json:"id"`
}

// BillReopen 把账单放回待执行。
type BillReopen struct {
	manage *BillManage
	Code   string `json:"code"`
	ID     string `json:"id"`
}

// BillClose 关闭一张还没进金库的账单。
type BillClose struct {
	manage *BillManage
	Code   string `json:"code"`
	ID     string `json:"id"`
}

func (own *BillApprove) GetInstance() interface{} { return billCommandInstance(own.manage, own) }
func (own *BillReopen) GetInstance() interface{}  { return billCommandInstance(own.manage, own) }
func (own *BillClose) GetInstance() interface{}   { return billCommandInstance(own.manage, own) }

func (own *BillApprove) Parse(req servertypes.IRequest) error { return bindBillCommand(req, own) }
func (own *BillReopen) Parse(req servertypes.IRequest) error  { return bindBillCommand(req, own) }
func (own *BillClose) Parse(req servertypes.IRequest) error   { return bindBillCommand(req, own) }

func (own *BillApprove) Validation(servertypes.IRequest) error { return requireBillCode(own.Code) }
func (own *BillReopen) Validation(servertypes.IRequest) error  { return requireBillCode(own.Code) }
func (own *BillClose) Validation(servertypes.IRequest) error   { return requireBillCode(own.Code) }

func (own *BillApprove) Do(req servertypes.IRequest) (interface{}, error) {
	cfg, err := loadManageConfig()
	if err != nil {
		return nil, err
	}
	if !business.AdminBillApproveAllowed(cfg.Mode) {
		return nil, fmt.Errorf("bill approval is CLI-only in %s mode; run pulse bill approve --id with --yes", cfg.Mode)
	}
	if err := access.RequireLoopback(req); err != nil {
		return nil, err
	}
	row, err := business.ApproveBill(context.Background(), cfg, own.Code, business.BillFlags{})
	return billActionResponse(row), err
}

func (own *BillReopen) Do(servertypes.IRequest) (interface{}, error) {
	row, err := business.ReopenBill(own.Code, business.DefaultBillsFile)
	return billActionResponse(row), err
}

func (own *BillClose) Do(servertypes.IRequest) (interface{}, error) {
	row, err := business.CloseBill(own.Code, business.DefaultBillsFile)
	return billActionResponse(row), err
}

func (own *BillApprove) GetResponse() interface{} { return &dto.BillActionResponse{} }
func (own *BillReopen) GetResponse() interface{}  { return &dto.BillActionResponse{} }
func (own *BillClose) GetResponse() interface{}   { return &dto.BillActionResponse{} }

func (own *BillApprove) RouterInfo() *servertypes.RouterInfo { return managepkg.RouterInfo(own) }
func (own *BillReopen) RouterInfo() *servertypes.RouterInfo  { return managepkg.RouterInfo(own) }
func (own *BillClose) RouterInfo() *servertypes.RouterInfo   { return managepkg.RouterInfo(own) }

func billCommandInstance(manage *BillManage, fallback interface{}) interface{} {
	if manage != nil {
		return manage
	}
	return fallback
}

func bindBillCommand(req servertypes.IRequest, dest interface{}) error {
	var body struct {
		Code string `json:"code"`
		ID   string `json:"id"`
	}
	if err := req.Bind(&body); err != nil {
		return err
	}
	id := strings.TrimSpace(body.Code)
	if id == "" {
		id = strings.TrimSpace(body.ID)
	}
	switch row := dest.(type) {
	case *BillApprove:
		row.Code = id
	case *BillReopen:
		row.Code = id
	case *BillClose:
		row.Code = id
	}
	return nil
}

func requireBillCode(id string) error {
	if strings.TrimSpace(id) == "" {
		return models.NewValidationError("code is required")
	}
	return nil
}

func billActionResponse(row *models.Bill) *dto.BillActionResponse {
	if row == nil {
		return &dto.BillActionResponse{}
	}
	return &dto.BillActionResponse{
		Code: row.Code, State: row.State, Action: row.Action, ReasonCode: row.ReasonCode,
		DecisionHash: row.DecisionHash, Planner: row.Planner, VaultTx: row.VaultTx,
	}
}
