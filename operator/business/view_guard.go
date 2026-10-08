// 本文件在主网启动时拒绝暴露管理界面。框架把视图端口绑在所有网卡上，而且不能关掉 testtoken。
package business

import (
	"fmt"
	"strconv"
	"strings"
)

// AdminBillApproveAllowed 只在 dry-run 显示账单批准按钮。测试网、live 和主网必须走 CLI。
func AdminBillApproveAllowed(mode string) bool {
	return mode == "" || mode == "dry-run"
}

// ViewPortFromArgs 读取 -view，不调用 flag.Parse。框架会自己解析命令行，这里再注册同名 flag 会 panic。
// 没写 -view 时与 digitalwayhk/core 一样默认 80。-view 0 关闭视图。
func ViewPortFromArgs(args []string) int {
	port := 80
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-view" || arg == "--view":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					port = n
				}
			}
		case strings.HasPrefix(arg, "-view="), strings.HasPrefix(arg, "--view="):
			raw := arg[strings.IndexByte(arg, '=')+1:]
			if n, err := strconv.Atoi(raw); err == nil {
				port = n
			}
		}
	}
	return port
}

// GuardExposedView 在主网拒绝把管理界面暴露出去，除非操作者显式确认。
// digitalwayhk/core 没有配置或中间件可以关掉 /api/servermanage/testtoken。
// 该接口在 IsLoaclVisit 下把 RFC1918（含 172.30.0.0/16）当成本地，并向其签发管理员 token。
func GuardExposedView(mode string, viewPort int, ack bool) error {
	if mode != "mainnet" || viewPort == 0 || ack {
		return nil
	}
	return fmt.Errorf("mainnet refuses an admin view on port %d: digitalwayhk/core listens on all interfaces and /api/servermanage/testtoken issues an admin token to RFC1918 callers; this repo cannot disable that route. Pass -view 0, or set acknowledgeExposedAdminView: true or OPERATOR_ACK_EXPOSED_VIEW=1 after the port is firewalled to localhost", viewPort)
}
