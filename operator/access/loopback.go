// 本文件拒绝非回环地址调用付款和结算。框架把局域网地址也当成本地，这里只认 127.0.0.0/8 和 ::1。
package access

import (
	"fmt"
	"net"
	"strings"

	servertypes "github.com/digitalwayhk/core/pkg/server/types"
)

// RequireLoopback 拒绝拿不到调用方，或调用方不是回环地址的请求。
// 有原始 HTTP 时以 RemoteAddr 为准：ClientIP 或 X-Forwarded-For 写成 127.0.0.1 也不能放行局域网连接。
func RequireLoopback(req servertypes.IRequest) error {
	if req == nil {
		return fmt.Errorf("settlement requires a loopback caller")
	}
	caller := Caller{ClientIP: strings.TrimSpace(req.GetClientIP())}
	if httpReq, ok := req.(servertypes.IRequestHttp); ok && httpReq.GetHttpRequest() != nil {
		r := httpReq.GetHttpRequest()
		caller.HasHTTP = true
		caller.RemoteAddr = r.RemoteAddr
		caller.ForwardedFor = r.Header.Get("X-Forwarded-For")
		caller.RealIP = r.Header.Get("X-Real-Ip")
	}
	return CheckCaller(caller)
}

// Caller 是从请求里取出的地址，测试不必实现完整的 IRequest。
type Caller struct {
	ClientIP     string
	RemoteAddr   string
	ForwardedFor string
	RealIP       string
	HasHTTP      bool
}

// CheckCaller 检查回环。空地址、局域网和伪造的转发头都拒绝。
func CheckCaller(c Caller) error {
	if c.HasHTTP {
		if !loopbackHost(c.RemoteAddr) {
			return fmt.Errorf("settlement is only accepted from a loopback caller")
		}
		if err := forwardedLoopback(c.ForwardedFor); err != nil {
			return err
		}
		if strings.TrimSpace(c.RealIP) != "" && !loopbackHost(c.RealIP) {
			return fmt.Errorf("settlement is only accepted from a loopback caller")
		}
		if strings.TrimSpace(c.ClientIP) != "" && !loopbackHost(c.ClientIP) {
			return fmt.Errorf("settlement is only accepted from a loopback caller")
		}
		return nil
	}
	if !loopbackHost(c.ClientIP) {
		return fmt.Errorf("settlement is only accepted from a loopback caller")
	}
	return nil
}

func forwardedLoopback(header string) error {
	if strings.TrimSpace(header) == "" {
		return nil
	}
	for _, hop := range strings.Split(header, ",") {
		hop = strings.TrimSpace(hop)
		if hop == "" {
			continue
		}
		if !loopbackHost(hop) {
			return fmt.Errorf("settlement is only accepted from a loopback caller")
		}
	}
	return nil
}

func loopbackHost(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	if strings.EqualFold(raw, "localhost") {
		return true
	}
	host := raw
	if h, _, err := net.SplitHostPort(raw); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
