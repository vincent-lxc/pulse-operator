// 本文件接收 Circle Gateway 的 v2 webhook，并记成未对账收入。
package public

import (
	"context"
	"encoding/json"
	"io"

	"github.com/digitalwayhk/core/pkg/server/router"
	servertypes "github.com/digitalwayhk/core/pkg/server/types"
	"github.com/vincent-lxc/pulse-operator/operator/access"
	"github.com/vincent-lxc/pulse-operator/operator/api/dto"
	"github.com/vincent-lxc/pulse-operator/operator/business"
	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

// GatewayHook 接收 gateway.mint.finalized / gateway.deposit.finalized。
type GatewayHook struct {
	raw       []byte
	signature string
	keyID     string
}

// Parse 优先读取原始 HTTP 正文，签名校验必须对着原文。
func (own *GatewayHook) Parse(req servertypes.IRequest) error {
	if httpReq, ok := req.(servertypes.IRequestHttp); ok && httpReq.GetHttpRequest() != nil {
		request := httpReq.GetHttpRequest()
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return err
		}
		own.raw = body
		own.signature = request.Header.Get("X-Circle-Signature")
		own.keyID = request.Header.Get("X-Circle-Key-Id")
		return nil
	}
	var payload json.RawMessage
	if err := req.Bind(&payload); err != nil {
		return err
	}
	own.raw = payload
	return nil
}

// Validation 要求正文是 JSON。
func (own *GatewayHook) Validation(servertypes.IRequest) error {
	if len(own.raw) == 0 {
		return models.NewValidationError("gateway body is required")
	}
	return nil
}

// Do 解析通知。live 模式校验 Circle 签名；dry-run 接受未签名的本地样例。
func (own *GatewayHook) Do(req servertypes.IRequest) (interface{}, error) {
	if err := access.RequireLoopback(req); err != nil {
		return nil, err
	}
	cfg, err := loadOperatorConfig()
	if err != nil {
		return nil, err
	}
	in, err := business.IngestGateway(context.Background(), cfg, own.raw, own.signature, own.keyID)
	if err != nil {
		return nil, err
	}
	return &dto.RevenueResponse{Status: "accepted", Ref: in.TxHash, Circle: treasury.ProductGateway}, nil
}

// GetResponse 声明 OpenAPI 成功体。
func (own *GatewayHook) GetResponse() interface{} { return &dto.RevenueResponse{} }

// RouterInfo 注册为公开 POST。
func (own *GatewayHook) RouterInfo() *servertypes.RouterInfo {
	return router.DefaultRouterInfo(own)
}
