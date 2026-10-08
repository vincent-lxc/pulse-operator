// 本文件组装 operator 的 Manage 与公开路由。
package operatorsvc

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/digitalwayhk/core/pkg/server/types"
	"github.com/vincent-lxc/pulse-operator/operator/api/manage"
	publicapi "github.com/vincent-lxc/pulse-operator/operator/api/public"
	"github.com/vincent-lxc/pulse-operator/operator/business"
	"github.com/vincent-lxc/pulse-operator/operator/contract"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

// Service 是金库 operator 的服务组合根。
type Service struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

// ServiceName 返回稳定服务名。
func (own *Service) ServiceName() string { return contract.ServiceName }

// GetTitle 是目录的中文名。
func (own *Service) GetTitle() string { return "金库" }

// GetLocaleTitle 按语言返回目录名。
func (own *Service) GetLocaleTitle(locale string) string {
	if locale == "en-US" {
		return "Treasury"
	}
	return own.GetTitle()
}

// Routers 返回管理列表和公开的看板、收入、单轮执行接口。
func (own *Service) Routers() []types.IRouter {
	routers := make([]types.IRouter, 0, 16)
	routers = append(routers, manage.NewCategoryManage().Routers()...)
	routers = append(routers, manage.NewPayeeManage().Routers()...)
	routers = append(routers, manage.NewPayableManage().Routers()...)
	routers = append(routers, manage.NewRevenueManage().Routers()...)
	routers = append(routers, manage.NewDecisionManage().Routers()...)
	routers = append(routers, manage.NewBillManage().Routers()...)
	routers = append(routers, manage.NewApprovalManage().Routers()...)
	routers = append(routers, manage.NewCycleManage().Routers()...)
	routers = append(routers,
		&publicapi.Dashboard{},
		&publicapi.RecordRevenue{},
		&publicapi.RunOnce{},
		&publicapi.CCTPIn{},
		&publicapi.GatewayHook{},
		&publicapi.Approve{},
		&publicapi.Reject{},
	)
	return routers
}

// Start 在 loopEnabled 时按 loopInterval 跑循环。默认关闭。
func (own *Service) Start() {
	path := os.Getenv("OPERATOR_CONFIG")
	if path == "" {
		path = "config/dry-run.yaml"
	}
	cfg, err := treasury.LoadConfig(path)
	if err != nil || !cfg.LoopEnabled || cfg.LoopInterval <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	own.mu.Lock()
	own.cancel = cancel
	own.mu.Unlock()
	go func() {
		ticker := time.NewTicker(cfg.LoopInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = business.Run(context.Background(), cfg)
			}
		}
	}()
}

// Stop 停掉定时循环。
func (own *Service) Stop() {
	own.mu.Lock()
	cancel := own.cancel
	own.cancel = nil
	own.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
