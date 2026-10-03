// 本文件提供只读看板，不写链。
package public

import (
	"net/http"

	"github.com/digitalwayhk/core/pkg/server/router"
	servertypes "github.com/digitalwayhk/core/pkg/server/types"
	"github.com/vincent-lxc/pulse-operator/operator/api/dto"
	"github.com/vincent-lxc/pulse-operator/operator/business"
)

// Dashboard 返回余额、预算、最近决策和待审批。
type Dashboard struct{}

// Parse 看板没有参数。
func (own *Dashboard) Parse(servertypes.IRequest) error { return nil }

// Validation 接受空请求。
func (own *Dashboard) Validation(servertypes.IRequest) error { return nil }

// Do 读取已落库的快照。
func (own *Dashboard) Do(servertypes.IRequest) (interface{}, error) {
	view, err := business.LoadDashboard()
	if err != nil {
		return nil, err
	}
	return dto.NewDashboardResponse(view), nil
}

// GetResponse 声明 OpenAPI 成功体。
func (own *Dashboard) GetResponse() interface{} { return &dto.DashboardResponse{} }

// RouterInfo 注册为公开 GET。
func (own *Dashboard) RouterInfo() *servertypes.RouterInfo {
	return router.DefaultRouterInfoWithOptions(own, router.WithMethod(http.MethodGet))
}
