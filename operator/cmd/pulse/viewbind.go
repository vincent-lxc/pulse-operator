// 本文件把管理界面绑到本机。框架的 HTML 服务写死监听所有网卡，这里在它启动前把端口改成 0，
// 再用同一套路由在 listen 地址上重新监听。默认是 127.0.0.1。
package main

import (
	"net"
	"net/http"
	"reflect"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"github.com/digitalwayhk/core/pkg/server/config"
	"github.com/digitalwayhk/core/pkg/server/run"
)

var viewBindWait = 30 * time.Second

func installViewBind(ws *run.WebServer, host string) {
	if ws == nil {
		return
	}
	if host == "" {
		host = "127.0.0.1"
	}
	field, ok := reflect.TypeOf(ws).Elem().FieldByName("saveConfig")
	if !ok {
		return
	}
	slot := (*func(*config.ServerConfig) error)(unsafe.Add(unsafe.Pointer(ws), field.Offset))
	prev := *slot
	var once sync.Once
	*slot = func(cfg *config.ServerConfig) error {
		once.Do(func() { redirectView(ws, host) })
		if prev != nil {
			return prev(cfg)
		}
		if cfg == nil {
			return nil
		}
		return cfg.Save()
	}
}

func redirectView(ws *run.WebServer, host string) {
	htmls := htmlServer(ws)
	if htmls == nil || htmls.Port == 0 {
		return
	}
	port := htmls.Port
	htmls.Port = 0
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	go func() {
		deadline := time.Now().Add(viewBindWait)
		var handler http.Handler
		for time.Now().Before(deadline) {
			handler = htmls.Handler()
			if handler != nil {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if handler == nil {
			return
		}
		srv := &http.Server{Addr: addr, Handler: handler}
		viewServer = srv
		_ = srv.ListenAndServe()
	}()
}

var viewServer *http.Server

func htmlServer(ws *run.WebServer) *run.HTMLServer {
	field, ok := reflect.TypeOf(ws).Elem().FieldByName("htmls")
	if !ok {
		return nil
	}
	return *(**run.HTMLServer)(unsafe.Add(unsafe.Pointer(ws), field.Offset))
}

func callSavedConfig(ws *run.WebServer, cfg *config.ServerConfig) error {
	field, ok := reflect.TypeOf(ws).Elem().FieldByName("saveConfig")
	if !ok {
		return nil
	}
	fn := *(*func(*config.ServerConfig) error)(unsafe.Add(unsafe.Pointer(ws), field.Offset))
	if fn == nil {
		return nil
	}
	return fn(cfg)
}

func setField(target any, name string, value any) {
	v := reflect.ValueOf(target)
	f, ok := v.Elem().Type().FieldByName(name)
	if !ok {
		return
	}
	reflect.NewAt(f.Type, unsafe.Add(unsafe.Pointer(v.Pointer()), f.Offset)).Elem().Set(reflect.ValueOf(value))
}
