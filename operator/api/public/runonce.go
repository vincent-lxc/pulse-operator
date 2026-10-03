// 本文件手动触发一轮 operator 循环。模式由配置决定，dry-run 不写链。
package public

import (
	"context"
	"os"

	"github.com/digitalwayhk/core/pkg/server/router"
	servertypes "github.com/digitalwayhk/core/pkg/server/types"
	"github.com/vincent-lxc/pulse-operator/operator/api/dto"
	"github.com/vincent-lxc/pulse-operator/operator/business"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

// RunOnce 按 OPERATOR_CONFIG 执行一轮循环。
type RunOnce struct{}

// Parse 没有请求体。
func (own *RunOnce) Parse(servertypes.IRequest) error { return nil }

// Validation 要求配置文件存在。
func (own *RunOnce) Validation(servertypes.IRequest) error { return nil }

// Do 执行一轮并返回决策条数。
func (own *RunOnce) Do(servertypes.IRequest) (interface{}, error) {
	path := os.Getenv("OPERATOR_CONFIG")
	if path == "" {
		path = "config/dry-run.yaml"
	}
	cfg, err := treasury.LoadConfig(path)
	if err != nil {
		return nil, err
	}
	report, err := business.Run(context.Background(), cfg)
	if err != nil {
		return nil, err
	}
	return &dto.RunOnceResponse{RunID: report.RunID, Decisions: len(report.Decisions)}, nil
}

// GetResponse 声明 OpenAPI 成功体。
func (own *RunOnce) GetResponse() interface{} { return &dto.RunOnceResponse{} }

// RouterInfo 注册为公开 POST。
func (own *RunOnce) RouterInfo() *servertypes.RouterInfo {
	return router.DefaultRouterInfo(own)
}
