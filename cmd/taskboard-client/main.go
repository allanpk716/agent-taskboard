// taskboard-client 是 Windows 桌面客户端的装配入口；行为在 internal/client 核心，
// 这里只读配置、选择平台适配层并启动核心。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/allanpk716/agent-taskboard/internal/client"
	"github.com/allanpk716/agent-taskboard/internal/client/desktop"
	"github.com/allanpk716/agent-taskboard/internal/version"
)

func main() {
	server := flag.String("server", "", "Taskboard 平台地址")
	afterUpdate := flag.Bool("after-update", false, "自动更新后的新进程启动参数")
	showVersion := flag.Bool("version", false, "显示客户端版本后退出")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.Client)
		return
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取客户端路径失败：%v\n", err)
		os.Exit(2)
	}
	lifecycle := desktop.NewLifecycle()
	serverURL, err := client.ResolveServerURL(*server, os.Getenv("TASKBOARD_SERVER_URL"), lifecycle.Config, exe)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	installed, err := client.InstallIfNeeded(*lifecycle, serverURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "安装 Taskboard 桌面客户端失败：%v\n", err)
		os.Exit(1)
	}
	if installed {
		return
	}
	system, err := desktop.NewSystem()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	hostname, err := os.Hostname()
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取主机名失败：%v\n", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	app := client.New(client.Config{
		ServerURL: serverURL, Version: version.Client, Hostname: hostname, AfterUpdate: *afterUpdate,
	}, system.Dependencies())
	if err := app.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "桌面客户端退出：%v\n", err)
		os.Exit(1)
	}
}
