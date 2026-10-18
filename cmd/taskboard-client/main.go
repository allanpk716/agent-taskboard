// taskboard-client 是 Windows 桌面客户端的装配入口；行为在 internal/client 核心，
// 这里只读配置、选择平台适配层并启动核心。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/client"
	"github.com/allanpk716/agent-taskboard/internal/client/desktop"
	"github.com/allanpk716/agent-taskboard/internal/version"
)

// DefaultServerURL 由发布构建用 -ldflags -X 注入；命令行参数和环境变量可覆盖。
var DefaultServerURL = "http://127.0.0.1:8080"

func main() {
	server := flag.String("server", DefaultServerURL, "Taskboard 平台地址")
	showVersion := flag.Bool("version", false, "显示客户端版本后退出")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.Client)
		return
	}
	envServer := strings.TrimSpace(os.Getenv("TASKBOARD_SERVER_URL"))
	explicitServer := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "server" {
			explicitServer = true
		}
	})
	if envServer != "" && !explicitServer {
		*server = envServer
	}
	if strings.TrimSpace(*server) == "" {
		fmt.Fprintln(os.Stderr, "缺少 Taskboard 平台地址")
		os.Exit(2)
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
		ServerURL: strings.TrimRight(*server, "/"), Version: version.Client, Hostname: hostname,
	}, system.Dependencies())
	if err := app.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "桌面客户端退出：%v\n", err)
		os.Exit(1)
	}
}
