// 本文件让 owner 批准或拒绝 PolicyVault 上的待审批。
package public

import (
	"context"
	"strings"

	"github.com/digitalwayhk/core/pkg/server/router"
	servertypes "github.com/digitalwayhk/core/pkg/server/types"
	"github.com/vincent-lxc/pulse-operator/operator/api/dto"
	"github.com/vincent-lxc/pulse-operator/operator/business"
	"github.com/vincent-lxc/pulse-operator/operator/models"
)

// Approve 批准一笔待审批。
type Approve struct {
	RequestID string `json:"request_id"`
}

// Reject 拒绝一笔待审批。
type Reject struct {
	RequestID string `json:"request_id"`
}

func (own *Approve) Parse(req servertypes.IRequest) error { return bindRequestID(req, &own.RequestID) }
func (own *Reject) Parse(req servertypes.IRequest) error  { return bindRequestID(req, &own.RequestID) }

func (own *Approve) Validation(servertypes.IRequest) error { return requireRequestID(own.RequestID) }
func (own *Reject) Validation(servertypes.IRequest) error  { return requireRequestID(own.RequestID) }

func (own *Approve) Do(servertypes.IRequest) (interface{}, error) {
	return settle(own.RequestID, "approve")
}
func (own *Reject) Do(servertypes.IRequest) (interface{}, error) {
	return settle(own.RequestID, "reject")
}

func (own *Approve) GetResponse() interface{} { return &dto.ApprovalActionResponse{} }
func (own *Reject) GetResponse() interface{}  { return &dto.ApprovalActionResponse{} }

func (own *Approve) RouterInfo() *servertypes.RouterInfo { return router.DefaultRouterInfo(own) }
func (own *Reject) RouterInfo() *servertypes.RouterInfo  { return router.DefaultRouterInfo(own) }

func settle(requestID, action string) (*dto.ApprovalActionResponse, error) {
	cfg, err := loadOperatorConfig()
	if err != nil {
		return nil, err
	}
	res, err := business.SettleApproval(context.Background(), cfg, requestID, action)
	if err != nil {
		return nil, err
	}
	return &dto.ApprovalActionResponse{RequestID: res.RequestID, Status: res.Status, TxHash: res.TxHash, Circle: res.Circle}, nil
}

func bindRequestID(req servertypes.IRequest, dest *string) error {
	var body struct {
		RequestID      string `json:"request_id"`
		RequestIDCamel string `json:"requestID"`
	}
	if err := req.Bind(&body); err != nil {
		return err
	}
	*dest = body.RequestID
	if *dest == "" {
		*dest = body.RequestIDCamel
	}
	return nil
}

func requireRequestID(id string) error {
	if strings.TrimSpace(id) == "" {
		return models.NewValidationError("request_id is required")
	}
	return nil
}
