// taskboard 是部门工单平台的单二进制入口。
//
// 用法：
//
//	taskboard serve       启动服务（默认子命令）
//	taskboard create-admin 创建首位管理员账号
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/server"
	"github.com/allanpk716/agent-taskboard/internal/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "taskboard: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}
	switch cmd {
	case "serve":
		return cmdServe(args)
	case "create-admin":
		return cmdCreateAdmin(args)
	case "help":
		usage(os.Stdout)
		return nil
	default:
		usage(os.Stderr)
		return fmt.Errorf("未知子命令 %q", cmd)
	}
}

func usage(w *os.File) {
	fmt.Fprintln(w, `用法：
  taskboard serve [flags]        启动服务（默认）
  taskboard create-admin [flags] 创建管理员账号
  taskboard help                 显示本帮助

通用 flags：
  -config string   配置文件路径（默认 config.toml）`)
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := fs.String("config", "config.toml", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	srv, err := server.New(cfg, st, clock.Real{})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), server.ShutdownTimeout)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func cmdCreateAdmin(args []string) error {
	fs := flag.NewFlagSet("create-admin", flag.ContinueOnError)
	configPath := fs.String("config", "config.toml", "配置文件路径")
	username := fs.String("username", "", "登录名")
	password := fs.String("password", "", "密码")
	name := fs.String("name", "", "显示名（默认同登录名）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *username == "" || *password == "" {
		return fmt.Errorf("create-admin 需要 -username 和 -password")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	member, err := identity.NewManager(st.DB, clock.Real{}).CreateAdmin(context.Background(), *username, *name, *password)
	if err != nil {
		return err
	}
	fmt.Printf("已创建管理员 %s（%s）\n", member.Username, member.Name)
	return nil
}
