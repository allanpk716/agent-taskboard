// taskboard-client 是 Windows 桌面客户端的装配入口；行为在 internal/client 核心，
// 这里只读配置、选择平台适配层并启动核心。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/allanpk716/agent-taskboard/internal/client"
	"github.com/allanpk716/agent-taskboard/internal/client/desktop"
	"github.com/allanpk716/agent-taskboard/internal/version"
)

func main() {
	reporter := desktop.NewFatalReporter()
	defer func() {
		if err := recover(); err != nil {
			reporter.Fatalf(1, "桌面客户端发生内部错误：%v", err)
		}
	}()
	server := flag.String("server", "", "Taskboard 平台地址")
	configKey := flag.String("config-key", "", "平台地址的 HKCU 配置键（测试隔离用）")
	autostart := flag.Bool("autostart", false, "开机自启启动参数")
	afterUpdate := flag.Bool("after-update", false, "自动更新后的新进程启动参数")
	afterInstall := flag.Bool("after-install", false, "覆盖安装后的新进程启动参数")
	showVersion := flag.Bool("version", false, "显示客户端版本后退出")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.Client)
		return
	}
	reporter.Infof("启动 Taskboard 桌面客户端 version=%s args=%q", version.Client, os.Args[1:])
	lifecycle := desktop.NewLifecycleWithConfigKey(*configKey)
	promptCtx, stopPrompt := signal.NotifyContext(context.Background(), os.Interrupt)
	serverURL, ok, err := client.ResolveInteractiveServerURL(promptCtx, *server, os.Getenv("TASKBOARD_SERVER_URL"), lifecycle.Config, desktop.NewServerURLPrompter(), nil)
	stopPrompt()
	if err != nil {
		reporter.Fatalf(2, "%v", err)
	}
	if !ok {
		return
	}
	reporter.Infof("解析后的 Taskboard 平台地址：%s", serverURL)
	installed, err := client.InstallIfNeeded(*lifecycle, serverURL)
	if err != nil {
		reporter.Fatalf(1, "安装 Taskboard 桌面客户端失败：%v", err)
	}
	if installed {
		return
	}
	system, err := desktop.NewSystem()
	if err != nil {
		reporter.Fatalf(1, "%v", err)
	}
	hostname, err := os.Hostname()
	if err != nil {
		reporter.Fatalf(1, "读取主机名失败：%v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	app := client.New(client.Config{
		ServerURL: serverURL, Version: version.Client, Hostname: hostname, AfterUpdate: *afterUpdate,
		AfterInstall: *afterInstall,
	}, system.Dependencies())
	if err := app.Run(ctx); err != nil {
		if errors.Is(err, client.ErrAlreadyRunning) {
			reporter.Infof("%v，请在右下角托盘查看。", err)
			if !*autostart && !*afterUpdate && !*afterInstall {
				desktop.ShowAlreadyRunning()
			}
			return
		}
		reporter.Fatalf(1, "桌面客户端退出：%v", err)
	}
}
