package main

import (
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/digitalwayhk/core/pkg/server/config"
	"github.com/digitalwayhk/core/pkg/server/run"
)

func TestAdminViewBindsLoopback(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	ws := &run.WebServer{}
	htmls := run.NewHTMLServer(port)
	setField(htmls, "handler", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	setField(ws, "htmls", htmls)
	viewBindWait = time.Second
	installViewBind(ws, "127.0.0.1")
	if err := callSavedConfig(ws, &config.ServerConfig{}); err != nil {
		t.Fatal(err)
	}
	if htmls.Port != 0 {
		t.Fatalf("framework port %d", htmls.Port)
	}
	deadline := time.Now().Add(2 * time.Second)
	var body string
	for time.Now().Before(deadline) {
		res, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/")
		if err != nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		body = string(raw)
		break
	}
	if viewServer != nil {
		_ = viewServer.Close()
	}
	if body != "ok" {
		t.Fatalf("body %q", body)
	}
}
