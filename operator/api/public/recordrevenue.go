// 本文件接收手工收入。它只落库，不广播交易。
package public

import (
	"strings"

	"github.com/digitalwayhk/core/pkg/server/router"
	servertypes "github.com/digitalwayhk/core/pkg/server/types"
	"github.com/vincent-lxc/pulse-operator/operator/api/dto"
	"github.com/vincent-lxc/pulse-operator/operator/business"
	"github.com/vincent-lxc/pulse-operator/operator/models"
)

// RecordRevenue 记录一笔尚未出现在链上日志里的 USDC 收入。
type RecordRevenue struct {
	Ref        string `json:"ref"`
	From       string `json:"from"`
	AmountUSDC string `json:"amount_usdc"`
	Memo       string `json:"memo"`
}

// Parse 绑定 JSON 字段。
func (own *RecordRevenue) Parse(req servertypes.IRequest) error { return req.Bind(own) }

// Validation 要求参考号、地址和正数金额。
func (own *RecordRevenue) Validation(servertypes.IRequest) error {
	if strings.TrimSpace(own.Ref) == "" {
		return models.NewValidationError("ref is required")
	}
	if !strings.HasPrefix(strings.TrimSpace(own.From), "0x") {
		return models.NewValidationError("from must be an address")
	}
	if strings.TrimSpace(own.AmountUSDC) == "" {
		return models.NewValidationError("amount_usdc is required")
	}
	return nil
}

// Do 写入收入模型，下一轮循环会计入未对账金额。
func (own *RecordRevenue) Do(servertypes.IRequest) (interface{}, error) {
	if err := business.RecordRevenue(own.Ref, own.From, own.AmountUSDC, own.Memo); err != nil {
		return nil, err
	}
	return &dto.RevenueResponse{Status: "accepted", Ref: own.Ref}, nil
}

// GetResponse 声明 OpenAPI 成功体。
func (own *RecordRevenue) GetResponse() interface{} { return &dto.RevenueResponse{} }

// RouterInfo 注册为公开 POST。
func (own *RecordRevenue) RouterInfo() *servertypes.RouterInfo {
	return router.DefaultRouterInfo(own)
}
