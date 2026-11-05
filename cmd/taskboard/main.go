// taskboard 是部门工单平台的单二进制入口。
//
// 用法：
//
//	taskboard serve        启动服务（默认子命令）
//	taskboard create-admin 创建首位管理员账号
//	taskboard backup       手动备份（定时备份在 serve 内置）
//	taskboard restore <备份文件> 恢复（需先停止服务）
//	taskboard backup-url <备份名> 打印 OSS 备份的一小时签名地址
//	taskboard migrate-attachments 附件在本地与 OSS 间离线迁移
//	taskboard healthcheck  容器健康检查（按配置端口请求 health）
//	taskboard update       检查并安装更新（前后端；-check 只检查）
//	taskboard rollback     换回上一版 exe 和更新前的备份
//	taskboard version      显示当前前后端版本
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
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
	case "backup-url":
		return cmdBackupURL(args)
	case "healthcheck":
		return cmdHealthcheck(args)
	case "restore":
		return cmdRestore(args)
	case "migrate-attachments":
		return cmdMigrateAttachments(args)
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

func usage(w io.Writer) {
	fmt.Fprintln(w, `用法：
  taskboard serve [flags]              启动服务（默认，内置每日备份调度）
  taskboard create-admin [flags]       创建管理员账号
  taskboard backup [flags]             手动备份一次（与定时备份同一段代码）
  taskboard backup-url <备份名> [flags] 打印 OSS 库备份的 1 小时签名地址
  taskboard healthcheck [flags]        按配置端口请求 health（镜像 HEALTHCHECK 用）
  taskboard restore <备份文件> [flags] 恢复（先停止服务；也可 -from-oss <备份名|latest>）
  taskboard migrate-attachments -to oss|local [flags] 附件离线迁移（-dry-run 只列计划）
    -accept-missing                   源端缺失时按清单切换后端
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
	preupdate := fs.Bool("preupdate", false, "做更新前备份，成功后最后一行只输出备份文件名")
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
	storage, objects, _, err := configuredAttachmentBackends(cfg)
	if err != nil {
		return err
	}
	b.SetAttachmentStorage(storage)
	b.SetObjectStorage(objects)
	var rec ops.Record
	if *preupdate {
		rec, err = b.Preupdate(context.Background())
	} else {
		// 手动备份按当天的日期定时备份算（每月 1 日是 monthly），参与同类轮换。
		rec, err = b.BackupNow(context.Background(), ops.KindForDate(clock.Real{}.Now()))
	}
	if err != nil {
		return fmt.Errorf("备份失败：%w", err)
	}
	if *preupdate {
		fmt.Println(rec.Filename)
		return nil
	}
	fmt.Printf("备份完成：%s（%d 字节，完整性校验通过，附件镜像新增 %d 个文件）\n",
		rec.Filename, rec.Size, rec.FilesAdded)
	return nil
}

// cmdBackupURL 签出一个 OSS 库备份的一小时下载地址；不在网页上提供入口。
func cmdBackupURL(args []string) error {
	configPath, name := "config.toml", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-config", "--config":
			if i+1 >= len(args) {
				return fmt.Errorf("-config 需要一个配置文件路径")
			}
			i++
			configPath = args[i]
		default:
			if name != "" {
				return fmt.Errorf("backup-url 只接受一个备份名：%q 多余", args[i])
			}
			name = args[i]
		}
	}
	if name == "" {
		return fmt.Errorf("backup-url 需要一个备份名：taskboard backup-url <备份名>")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	_, objects, _, err := configuredAttachmentBackends(cfg)
	if err != nil {
		return err
	}
	if objects == nil {
		return fmt.Errorf("backup-url 只能在 [attachments] backend = \"oss\" 时使用")
	}
	target, err := ops.BackupObjectURL(context.Background(), objects, name)
	if err != nil {
		return err
	}
	fmt.Println(target)
	return nil
}

func cmdHealthcheck(args []string) error {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	configPath := fs.String("config", "config.toml", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/v1/health", cfg.Port), nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("健康检查失败：%w", err)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("健康检查失败：HTTP %d", res.StatusCode)
	}
	return nil
}

// cmdRestore 把库换成指定的备份并从镜像补齐附件（五步见 docs/deploy-and-release.md
// 「备份与恢复」）。要求服务已停止；完成后由 NSSM 或操作员重新启动。
func cmdRestore(args []string) error {
	// restore 的自然写法是「taskboard restore <文件> -config …」（文件在前），
	// 标准库 flag 不支持位置参数后的 flag，这里自己拆。
	configPath, backupFile, fromOSS := "config.toml", "", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-config", "--config":
			if i+1 >= len(args) {
				return fmt.Errorf("-config 需要一个配置文件路径")
			}
			i++
			configPath = args[i]
		case "-from-oss", "--from-oss":
			if i+1 >= len(args) {
				return fmt.Errorf("-from-oss 需要一个备份名或 latest")
			}
			i++
			fromOSS = args[i]
		default:
			if backupFile != "" {
				return fmt.Errorf("restore 只接受一个备份文件参数：%q 多余", args[i])
			}
			backupFile = args[i]
		}
	}
	if backupFile != "" && fromOSS != "" {
		return fmt.Errorf("restore 不能同时使用备份文件和 -from-oss")
	}
	if backupFile == "" && fromOSS == "" {
		return fmt.Errorf("restore 需要一个备份文件参数：taskboard restore <文件> -config <配置>")
	}
	if fromOSS != "" {
		return restoreFromOSS(configPath, fromOSS)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	storage, _, marker, err := configuredAttachmentBackends(cfg)
	if err != nil {
		return err
	}
	res, err := ops.RestoreWithStorageAndMarker(cfg.DataDir, cfg.Port, backupFile,
		clock.Real{}.Now(), storage, marker)
	if err != nil {
		return err
	}
	printRestoreResult(res)
	return nil
}

func restoreFromOSS(configPath, selector string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if err := attachments.EnsureServiceStopped(cfg.Port); err != nil {
		return err
	}
	storage, objects, marker, err := configuredAttachmentBackends(cfg)
	if err != nil {
		return err
	}
	if objects == nil {
		return fmt.Errorf("restore -from-oss 只能在 [attachments] backend = \"oss\" 时使用")
	}
	name := selector
	if name == "latest" {
		// latest 直接从 OSS 解析：本机库可能已经丢失，这里绝不能打开
		// （更不能新建）本地库。
		name, err = ops.LatestBackupFromStorage(context.Background(), objects)
		if err != nil {
			return err
		}
	}
	backupFile, err := ops.DownloadBackupFromOSS(context.Background(), objects,
		filepath.Join(cfg.DataDir, "backups"), name)
	if err != nil {
		return err
	}
	res, err := ops.RestoreWithStorageAndMarker(cfg.DataDir, cfg.Port, backupFile,
		clock.Real{}.Now(), storage, marker)
	if err != nil {
		return err
	}
	fmt.Printf("已从 OSS 下载备份：%s\n", filepath.Base(res.BackupFile))
	printRestoreResult(res)
	return nil
}

func printRestoreResult(res ops.RestoreResult) {
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
}

// cmdMigrateAttachments 在停服状态下把附件在本地上传和 OSS 之间双向迁移。
func cmdMigrateAttachments(args []string) error {
	fs := flag.NewFlagSet("migrate-attachments", flag.ContinueOnError)
	configPath := fs.String("config", "config.toml", "配置文件路径")
	targetBackend := fs.String("to", "", "目标附件后端（oss 或 local）")
	dryRun := fs.Bool("dry-run", false, "只列迁移计划，不写任何对象或标记")
	acceptMissing := fs.Bool("accept-missing", false, "源端缺失时按缺失清单切换后端")
	fs.Usage = func() { usage(os.Stderr) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("migrate-attachments 不接受位置参数：%q", fs.Arg(0))
	}
	target := strings.TrimSpace(*targetBackend)
	if target != config.AttachmentBackendLocal && target != config.AttachmentBackendOSS {
		return fmt.Errorf("migrate-attachments 的 -to 只能是 oss 或 local")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	// 先确认停服，再打开数据库，避免对运行中的库做离线操作。
	if err := attachments.EnsureServiceStopped(cfg.Port); err != nil {
		return err
	}
	var db *sql.DB
	if *dryRun {
		// dry-run 绝不能写任何东西：不跑迁移、不切 WAL、不新建库。
		db, err = store.OpenReadOnly(cfg.DataDir)
		if err == nil {
			defer db.Close()
		}
	} else {
		var st *store.Store
		st, err = store.Open(cfg.DataDir)
		if err == nil {
			defer st.Close()
			db = st.DB
		}
	}
	if err != nil {
		return err
	}

	ctx := context.Background()
	marker, exists, err := attachments.ReadBackendMarker(ctx, db)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("数据库缺少 attachments_backend 标记：先用当前版本完成数据库迁移，或恢复完整备份")
	}
	if marker.Backend != config.AttachmentBackendLocal && marker.Backend != config.AttachmentBackendOSS {
		return fmt.Errorf("数据库记录的附件后端 %q 无效", marker.Backend)
	}
	if marker.Backend == target {
		return fmt.Errorf("附件已在 %s 后端：无需迁移；请检查 [attachments] backend 配置", marker.Backend)
	}

	var source attachments.Storage
	switch marker.Backend {
	case config.AttachmentBackendLocal:
		source, err = attachments.NewLocalStorage(cfg.DataDir)
	case config.AttachmentBackendOSS:
		source, err = attachments.NewOSSStorage(attachments.OSSConfig{
			Region: cfg.Attachments.OSS.Region, Bucket: marker.Bucket, Prefix: marker.Prefix,
			InternalEndpoint: cfg.Attachments.OSS.InternalEndpoint,
			SigningHost:      cfg.Attachments.OSS.SigningHost,
		}, attachments.OSSStorageOptions{
			AccessKeyID:     os.Getenv("OSS_ACCESS_KEY_ID"),
			AccessKeySecret: os.Getenv("OSS_ACCESS_KEY_SECRET"),
		})
	}
	if err != nil {
		return err
	}

	want := attachments.BackendMarker{Backend: target}
	var destination attachments.Storage
	if target == config.AttachmentBackendOSS {
		want.Bucket = strings.TrimSpace(cfg.Attachments.OSS.Bucket)
		want.Prefix, err = attachments.NormalizeOSSPrefix(cfg.Attachments.OSS.Prefix)
		if err != nil {
			return err
		}
		destination, err = attachments.NewOSSStorage(attachments.OSSConfig{
			Region: cfg.Attachments.OSS.Region, Bucket: want.Bucket, Prefix: want.Prefix,
			InternalEndpoint: cfg.Attachments.OSS.InternalEndpoint,
			SigningHost:      cfg.Attachments.OSS.SigningHost,
		}, attachments.OSSStorageOptions{
			AccessKeyID:     os.Getenv("OSS_ACCESS_KEY_ID"),
			AccessKeySecret: os.Getenv("OSS_ACCESS_KEY_SECRET"),
		})
	} else {
		destination, err = attachments.NewLocalStorage(cfg.DataDir)
	}
	if err != nil {
		return err
	}

	res, migrateErr := attachments.MigrateAttachments(ctx, db, source, destination, attachments.MigrationOptions{
		Target: want, DataDir: cfg.DataDir, DryRun: *dryRun,
		AcceptMissing: *acceptMissing,
		Progress: func(done, total int) {
			fmt.Printf("附件迁移进度：%d/%d\n", done, total)
		},
	})
	printMigrationResult(res)
	if migrateErr != nil {
		return migrateErr
	}
	if !*dryRun {
		fmt.Printf("迁移完成：请把 [attachments] backend 改成 %s 再启动\n", target)
	}
	return nil
}

func printMigrationResult(res attachments.MigrationResult) {
	if res.DryRun {
		fmt.Printf("附件迁移计划：附件 %d 个、共 %d 字节；需复制 %d 个、已一致 %d 个、源端缺失 %d 个\n",
			res.Planned+res.Skipped+res.Missing+res.Failed, res.TotalSize,
			res.Planned, res.Skipped, res.Missing)
	} else {
		fmt.Printf("附件迁移汇总：复制 %d 个、跳过 %d 个、失败 %d 个、源端缺失 %d 个\n",
			res.Copied, res.Skipped, res.Failed, res.Missing)
	}
	printIDList("源端缺失附件", res.MissingIDs)
	printIDList("复制失败附件", res.FailedIDs)
	printIDList("源端有对象但数据库无记录", res.SourceOnlyIDs)
}

func printIDList(title string, ids []int64) {
	if len(ids) == 0 {
		return
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("%d", id)
	}
	fmt.Printf("%s：%s\n", title, strings.Join(parts, "、"))
}

// configuredAttachmentBackends 按配置构造离线命令要用的附件后端。这里不探测
// 桶：第一条备份/恢复请求会暴露连接问题，也让 backup-url 可以只做本地签名。
func configuredAttachmentBackends(cfg config.Config) (attachments.Storage,
	attachments.ObjectStorage, attachments.BackendMarker, error) {
	none := attachments.BackendMarker{}
	switch cfg.Attachments.Backend {
	case "", config.AttachmentBackendLocal:
		storage, err := attachments.NewLocalStorage(cfg.DataDir)
		if err != nil {
			return nil, nil, none, err
		}
		return storage, nil, attachments.BackendMarker{
			Backend: attachments.AttachmentBackendLocal,
		}, nil
	case config.AttachmentBackendOSS:
		marker := attachments.BackendMarker{
			Backend: attachments.AttachmentBackendOSS,
			Bucket:  strings.TrimSpace(cfg.Attachments.OSS.Bucket),
		}
		var err error
		marker.Prefix, err = attachments.NormalizeOSSPrefix(cfg.Attachments.OSS.Prefix)
		if err != nil {
			return nil, nil, none, err
		}
		if marker.Bucket == "" {
			return nil, nil, none, fmt.Errorf("backend = \"oss\" 时必须配置 bucket")
		}
		storage, err := attachments.NewOSSStorage(attachments.OSSConfig{
			Region: cfg.Attachments.OSS.Region, Bucket: marker.Bucket, Prefix: marker.Prefix,
			InternalEndpoint: cfg.Attachments.OSS.InternalEndpoint,
			SigningHost:      cfg.Attachments.OSS.SigningHost,
		}, attachments.OSSStorageOptions{
			AccessKeyID:     os.Getenv("OSS_ACCESS_KEY_ID"),
			AccessKeySecret: os.Getenv("OSS_ACCESS_KEY_SECRET"),
		})
		if err != nil {
			return nil, nil, none, err
		}
		objects := attachments.ObjectStorageOf(storage)
		if objects == nil {
			return nil, nil, none, fmt.Errorf("OSS 附件后端不支持备份对象接口")
		}
		return storage, objects, marker, nil
	default:
		return nil, nil, none, fmt.Errorf("[attachments] backend = %q 无效（只能是 local 或 oss）",
			cfg.Attachments.Backend)
	}
}

// updateOptions 从配置构造更新选项（三个更新入口共用 internal/updater）。
func updateOptions(cfg config.Config, exePath string, logf func(string, ...any)) updater.Options {
	return updater.Options{
		Update: cfg.Update, DataDir: cfg.DataDir, Backup: cfg.Backup, WebDir: cfg.WebDir,
		ExePath: exePath, Port: cfg.Port, Log: logf,
		Preupdate: func(ctx context.Context) (updater.PreupdateBackup, error) {
			st, err := store.Open(cfg.DataDir)
			if err != nil {
				return updater.PreupdateBackup{}, err
			}
			defer st.Close()
			b, err := ops.NewBackups(st.DB, cfg.DataDir, cfg.Backup.BackupOptions(), clock.Real{})
			if err != nil {
				return updater.PreupdateBackup{}, err
			}
			storage, objects, _, err := configuredAttachmentBackends(cfg)
			if err != nil {
				return updater.PreupdateBackup{}, err
			}
			b.SetAttachmentStorage(storage)
			b.SetObjectStorage(objects)
			rec, err := b.Preupdate(ctx)
			if err != nil {
				return updater.PreupdateBackup{}, err
			}
			return updater.PreupdateBackup{
				Filename: rec.Filename, Local: rec.Location == ops.LocationLocal,
			}, nil
		},
		RestoreBackup: func(dataDir string, port int, backupFile string, now time.Time) (ops.RestoreResult, error) {
			storage, _, marker, err := configuredAttachmentBackends(cfg)
			if err != nil {
				return ops.RestoreResult{}, err
			}
			return ops.RestoreWithStorageAndMarker(dataDir, port, backupFile, now, storage, marker)
		},
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
	features := fs.Bool("features", false, "每行输出一个机器可读的能力名")
	fs.Usage = func() { usage(os.Stderr) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *features {
		// 不加载配置、不碰网络：部署脚本在无配置的容器里探测镜像能力。
		for _, feature := range version.Features {
			fmt.Println(feature)
		}
		return nil
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
