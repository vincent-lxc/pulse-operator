// 本文件用 Circle CCTP v2 Iris 确认一笔跨链 USDC，并记成未对账收入。
package public

import (
	"context"
	"os"
	"strconv"

	"github.com/digitalwayhk/core/pkg/server/router"
	servertypes "github.com/digitalwayhk/core/pkg/server/types"
	"github.com/vincent-lxc/pulse-operator/operator/access"
	"github.com/vincent-lxc/pulse-operator/operator/api/dto"
	"github.com/vincent-lxc/pulse-operator/operator/business"
	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

// CCTPIn 确认一笔源链 burn 已经可以在 Arc 上铸出。
type CCTPIn struct {
	SourceDomain string `json:"source_domain"`
	TxHash       string `json:"tx_hash"`
}

// Parse 绑定 JSON。
func (own *CCTPIn) Parse(req servertypes.IRequest) error { return req.Bind(own) }

// Validation 要求源域和 burn 交易哈希。
func (own *CCTPIn) Validation(servertypes.IRequest) error {
	if own.TxHash == "" {
		return models.NewValidationError("tx_hash is required")
	}
	if _, err := strconv.ParseUint(own.SourceDomain, 10, 32); err != nil {
		return models.NewValidationError("source_domain must be a CCTP domain id")
	}
	return nil
}

// Do 查询 Iris 并落库。dry-run 也会查询，因为这是显式的确认调用；没有网络时返回 Iris 的错误。
func (own *CCTPIn) Do(req servertypes.IRequest) (interface{}, error) {
	if err := access.RequireLoopback(req); err != nil {
		return nil, err
	}
	cfg, err := loadOperatorConfig()
	if err != nil {
		return nil, err
	}
	domain, _ := strconv.ParseUint(own.SourceDomain, 10, 32)
	in, err := business.IngestCCTP(context.Background(), cfg, uint32(domain), own.TxHash)
	if err != nil {
		return nil, err
	}
	return &dto.RevenueResponse{Status: "accepted", Ref: in.TxHash, Circle: treasury.ProductCCTP}, nil
}

// GetResponse 声明 OpenAPI 成功体。
func (own *CCTPIn) GetResponse() interface{} { return &dto.RevenueResponse{} }

// RouterInfo 注册为公开 POST。
func (own *CCTPIn) RouterInfo() *servertypes.RouterInfo {
	return router.DefaultRouterInfo(own)
}

func loadOperatorConfig() (treasury.Config, error) {
	path := os.Getenv("OPERATOR_CONFIG")
	if path == "" {
		path = "config/dry-run.yaml"
	}
	return treasury.LoadConfig(path)
}
