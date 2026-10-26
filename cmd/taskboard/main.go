// taskboard 是部门工单平台的单二进制入口。
//
// 用法：
//
//	taskboard serve        启动服务（默认子命令）
//	taskboard create-admin 创建首位管理员账号
//	taskboard backup       手动备份（定时备份在 serve 内置）
//	taskboard restore <备份文件> 恢复（需先停止服务）
//	taskboard update       检查并安装更新（前后端；-check 只检查）
//	taskboard rollback     换回上一版 exe 和更新前的备份
//	taskboard version      显示当前前后端版本
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/server"
	"github.com/allanpk716/agent-taskboard/internal/store"
	"github.com/allanpk716/agent-taskboard/internal/updater"
	"github.com/allanpk716/agent-taskboard/internal/version"
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
	case "backup":
		return cmdBackup(args)
	case "restore":
		return cmdRestore(args)
	case "update":
		return cmdUpdate(args)
	case "rollback":
		return cmdRollback(args)
	case "version":
		return cmdVersion(args)
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
  taskboard serve [flags]              启动服务（默认，内置每日备份调度）
  taskboard create-admin [flags]       创建管理员账号
  taskboard backup [flags]             手动备份一次（与定时备份同一段代码）
  taskboard restore <备份文件> [flags] 恢复（先停止服务，见 docs/deploy-and-release.md）
  taskboard update [flags]             检查并安装更新（-check 只检查不安装）
  taskboard rollback [flags]           换回上一版 exe 和更新前的备份（先停止服务）
  taskboard version [flags]            显示当前前后端版本
  taskboard help                       显示本帮助

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
	if identity.HasReplacementChar(*name) {
		return fmt.Errorf("名字含乱码字符（�），请检查终端编码；请用 UTF-8 编码提交")
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

// cmdBackup 手动做一次备份（每天 03:00 的定时备份、更新前备份和它共用
// internal/ops 的同一段代码）。服务运行中也可以执行：VACUUM INTO 是在线备份。
func cmdBackup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
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
	b, err := ops.NewBackups(st.DB, cfg.DataDir, cfg.Backup.BackupOptions(), clock.Real{})
	if err != nil {
		return err
	}
	// 手动备份按当天的日期定时备份算（每月 1 日是 monthly），参与同类轮换。
	now := clock.Real{}.Now()
	rec, err := b.BackupNow(context.Background(), ops.KindForDate(now))
	if err != nil {
		return fmt.Errorf("备份失败：%w", err)
	}
	fmt.Printf("备份完成：%s（%d 字节，完整性校验通过，附件镜像新增 %d 个文件）\n",
		rec.Filename, rec.Size, rec.FilesAdded)
	return nil
}

// cmdRestore 把库换成指定的备份并从镜像补齐附件（五步见 docs/deploy-and-release.md
// 「备份与恢复」）。要求服务已停止；完成后由 NSSM 或操作员重新启动。
func cmdRestore(args []string) error {
	// restore 的自然写法是「taskboard restore <文件> -config …」（文件在前），
	// 标准库 flag 不支持位置参数后的 flag，这里自己拆。
	configPath, backupFile := "config.toml", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-config", "--config":
			if i+1 >= len(args) {
				return fmt.Errorf("-config 需要一个配置文件路径")
			}
			i++
			configPath = args[i]
		default:
			if backupFile != "" {
				return fmt.Errorf("restore 只接受一个备份文件参数：%q 多余", args[i])
			}
			backupFile = args[i]
		}
	}
	if backupFile == "" {
		return fmt.Errorf("restore 需要一个备份文件参数：taskboard restore <文件> -config <配置>")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	res, err := ops.Restore(cfg.DataDir, cfg.Port, backupFile, clock.Real{}.Now())
	if err != nil {
		return err
	}
	if res.SavedCurrentAs != "" {
		fmt.Printf("已另存当前库：%s\n", filepath.Base(res.SavedCurrentAs))
	}
	if res.SavedGuestLinkKeyAs != "" {
		fmt.Printf("已另存当前访客链接加密密钥：%s\n", filepath.Base(res.SavedGuestLinkKeyAs))
	}
	fmt.Printf("已换上备份：%s，从镜像补齐附件 %d 个", filepath.Base(res.BackupFile), res.AttachmentsRestored)
	if len(res.MissingAttachments) > 0 {
		fmt.Printf("（镜像里也缺 %d 个：%s）", len(res.MissingAttachments), strings.Join(res.MissingAttachments, "、"))
	}
	fmt.Println()
	if res.GuestLinkKeyRestored {
		fmt.Println("已还原访客链接加密密钥：恢复后的访客链接可继续直接复制转发文案")
	} else {
		fmt.Println("备份里没有访客链接加密密钥：恢复后的访客链接按老链接处理，照常可用，需换新地址后才能再次复制")
	}
	fmt.Println("恢复完成：请启动服务（NSSM 下执行 nssm start <服务名>，或直接 taskboard serve）")
	return nil
}

// updateOptions 从配置构造更新选项（三个更新入口共用 internal/updater）。
func updateOptions(cfg config.Config, exePath string, logf func(string, ...any)) updater.Options {
	return updater.Options{
		Update: cfg.Update, DataDir: cfg.DataDir, Backup: cfg.Backup, WebDir: cfg.WebDir,
		ExePath: exePath, Port: cfg.Port, Log: logf,
	}
}

// cmdUpdate 检查并安装更新（docs/deploy-and-release.md「更新」）：按
// [update].sources 顺序回退，前端 min_server 不满足时先升级后端；后端更新
// 后提示重启（NSSM 拉起新版本，启动时自动迁移并做健康检查）。
func cmdUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	configPath := fs.String("config", "config.toml", "配置文件路径")
	check := fs.Bool("check", false, "只检查更新，不安装")
	exePath := fs.String("exe", "", "要替换的后端 exe 路径（默认当前进程自己的 exe）")
	fs.Usage = func() { usage(os.Stderr) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	opt := updateOptions(cfg, *exePath, func(format string, args ...any) {
		fmt.Printf(format+"\n", args...)
	})
	if *check {
		status, err := updater.Check(context.Background(), opt)
		if err != nil {
			return err
		}
		fmt.Println(status.Describe())
		return nil
	}
	res, err := updater.Apply(context.Background(), opt)
	if err != nil {
		return err
	}
	if len(res.Steps) == 0 {
		fmt.Println("已是最新，无需更新。")
		return nil
	}
	fmt.Printf("更新完成（源 %s）：\n", res.SourceUsed)
	for _, step := range res.Steps {
		fmt.Printf("  %s\n", step)
	}
	if res.RestartNeeded {
		fmt.Println("后端已更新：请重启服务（NSSM 下执行 nssm restart <服务名>），新版本启动时会自动迁移并做健康检查。")
	}
	return nil
}

// cmdRollback 换回上一版 exe 和最近一次更新前的备份（新版本起不来时用；
// 网页上的按钮那时不可用，只能在服务器上执行本命令）。
func cmdRollback(args []string) error {
	fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
	configPath := fs.String("config", "config.toml", "配置文件路径")
	exePath := fs.String("exe", "", "要换回的 exe 路径（默认当前进程自己的 exe）")
	fs.Usage = func() { usage(os.Stderr) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	opt := updateOptions(cfg, *exePath, nil)
	res, err := updater.Rollback(opt)
	if err != nil {
		return err
	}
	fmt.Printf("已换回上一版 exe（%s）\n", filepath.Base(res.RestoredExeFrom))
	fmt.Printf("已恢复更新前备份：%s\n", filepath.Base(res.RestoredBackup))
	if res.Restore.SavedCurrentAs != "" {
		fmt.Printf("更新后的库另存为：%s\n", filepath.Base(res.Restore.SavedCurrentAs))
	}
	if len(res.Restore.MissingAttachments) > 0 {
		fmt.Printf("提醒：镜像里也缺 %d 个附件：%s\n", len(res.Restore.MissingAttachments), strings.Join(res.Restore.MissingAttachments, "、"))
	}
	fmt.Println("回滚完成：请启动服务（NSSM 下执行 nssm start <服务名>，或直接 taskboard serve）")
	return nil
}

// cmdVersion 显示当前前后端版本（后端是编译时注入的，前端读 web 目录的
// version.json）。
func cmdVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	configPath := fs.String("config", "config.toml", "配置文件路径")
	fs.Usage = func() { usage(os.Stderr) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	web := updater.WebVersion(cfg.WebDir)
	if web == "" {
		web = "未知（手动部署，无 version.json）"
	}
	fmt.Printf("后端 %s，前端 %s\n", version.Server, web)
	return nil
}
