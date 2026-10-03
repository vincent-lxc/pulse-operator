// pulse 启动 operator 服务，或用 demo 子命令离线跑完一轮 dry-run。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/digitalwayhk/core/pkg/server/run"
	operatorsvc "github.com/vincent-lxc/pulse-operator/operator"
	"github.com/vincent-lxc/pulse-operator/operator/business"
	"github.com/vincent-lxc/pulse-operator/operator/models"
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
	if err := models.EnsureStorage(); err != nil {
		fmt.Fprintln(os.Stderr, "operator:", err)
		os.Exit(1)
	}
	server := run.NewWebServer()
	server.AddIService(&operatorsvc.Service{})
	server.Start()
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
