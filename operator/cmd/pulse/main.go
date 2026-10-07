// pulse 启动 operator 服务，或用 demo 子命令离线跑完一轮 dry-run。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/digitalwayhk/core/pkg/server/router"
	"github.com/digitalwayhk/core/pkg/server/run"
	servertypes "github.com/digitalwayhk/core/pkg/server/types"
	operatorsvc "github.com/vincent-lxc/pulse-operator/operator"
	"github.com/vincent-lxc/pulse-operator/operator/admintitle"
	"github.com/vincent-lxc/pulse-operator/operator/business"
	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
	"github.com/zeromicro/go-zero/core/logx"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "demo" {
		if err := runDemo(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "operator:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "bill" {
		if err := runBill(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "operator:", err)
			os.Exit(1)
		}
		return
	}
	if err := models.EnsureStorage(); err != nil {
		fmt.Fprintln(os.Stderr, "operator:", err)
		os.Exit(1)
	}
	server := run.NewWebServer()
	server.AddIService(&operatorsvc.Service{}, &servertypes.ServerOption{
		Demo: &servertypes.DemoOption{File: admintitle.FS()},
	})
	applyListenHost(listenHost())
	server.Start()
}

func listenHost() string {
	if host := os.Getenv("OPERATOR_BIND"); host != "" {
		return host
	}
	path := os.Getenv("OPERATOR_CONFIG")
	if path != "" {
		if cfg, err := treasury.LoadConfig(path); err == nil && cfg.Listen != "" {
			return cfg.Listen
		}
	}
	return "127.0.0.1"
}

func applyListenHost(host string) {
	local := host == "127.0.0.1" || host == "localhost" || host == "::1"
	for _, sc := range router.GetContexts() {
		if sc == nil || sc.Config == nil {
			continue
		}
		sc.Config.Host = host
		if local {
			sc.Config.IsLoaclVisit = true
		}
	}
}

func runDemo(args []string) error {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	configPath := fs.String("config", "config/dry-run.yaml", "path to operator config")
	if err := fs.Parse(args); err != nil {
		return err
	}
	logx.Disable()
	_, err := business.RunFile(context.Background(), *configPath, os.Stdout)
	return err
}
