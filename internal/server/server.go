// Package server 把各模块装配成可监听的 HTTP 服务。
package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/agentdocs"
	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/httpapi"
	"github.com/allanpk716/agent-taskboard/internal/idempotency"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/labels"
	"github.com/allanpk716/agent-taskboard/internal/notifications"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/store"
	"github.com/allanpk716/agent-taskboard/internal/templates"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
	"github.com/allanpk716/agent-taskboard/internal/updater"
	"github.com/allanpk716/agent-taskboard/internal/version"
	"github.com/allanpk716/agent-taskboard/internal/webapp"
)

// Server 是一个运行中的 taskboard 服务实例。
type Server struct {
	cfg           config.Config
	Ident         *identity.Manager
	Store         *store.Store
	Atts          *attachments.Manager
	Backups       *ops.Backups
	Idem          *idempotency.Manager
	Notifications *notifications.Manager

	handler  http.Handler
	web      *webapp.Handler
	httpSrv  *http.Server
	listener net.Listener
}

// New 打开数据库、装配 API 与静态站点。webDir 不存在会报错。
// 首次启动时导入标签字典与预置模板的一次性种子（spec「初始数据」）。
func New(cfg config.Config, st *store.Store, c clock.Clock) (*Server, error) {
	ident := identity.NewManager(st.DB, c)
	labelDict := labels.NewManager(st.DB)
	if err := labelDict.SeedIfEmpty(context.Background()); err != nil {
		return nil, fmt.Errorf("导入标签字典种子失败：%w", err)
	}
	atts, err := attachments.NewManager(st.DB, cfg.DataDir, c, attachments.LimitsOf(
		cfg.Attachments.MaxFileMB, cfg.Attachments.MaxFilesPerPost,
		cfg.Attachments.MaxImagesPerPost, cfg.Attachments.MaxTicketMB))
	if err != nil {
		return nil, err
	}
	templateDict := templates.NewManager(st.DB, c)
	if err := templateDict.SeedIfEmpty(context.Background()); err != nil {
		return nil, fmt.Errorf("导入预置模板失败：%w", err)
	}
	// #63 一次性迁移：没改过的预置 Bug 反馈模板升级成带分组的新版本。
	if err := templateDict.UpgradePresetBugReport(context.Background()); err != nil {
		return nil, fmt.Errorf("升级预置 Bug 反馈模板失败：%w", err)
	}
	backups, err := ops.NewBackups(st.DB, cfg.DataDir, cfg.Backup.BackupOptions(), c)
	if err != nil {
		return nil, err
	}
	idem := idempotency.NewManager(st.DB, c)
	notificationStore := notifications.NewManager(st.DB, c)
	ticketStore := tickets.NewManager(st.DB, c, labelDict, templateDict, atts)
	ticketStore.SetNotifier(notificationStore)
	api := httpapi.NewAPI(ident, ticketStore,
		templateDict, idem, atts, backups, cfg)
	api.SetNotifications(notificationStore)
	web, err := webapp.New(cfg.WebDir)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle("/api/", api.Handler())
	docs := agentdocs.Handler(cfg.Server.PublicURL)
	mux.Handle("/agent.md", docs)
	mux.Handle("/llms.txt", docs)
	mux.Handle("/api.md", docs)
	mux.Handle("/", web)

	return &Server{
		cfg:           cfg,
		Ident:         ident,
		Store:         st,
		Atts:          atts,
		Backups:       backups,
		Idem:          idem,
		Notifications: notificationStore,
		handler:       mux,
		web:           web,
	}, nil
}

// Handler 返回完整路由（测试和反向代理调试用）。
func (s *Server) Handler() http.Handler { return s.handler }

// Serve 在 ln 上对外服务，直到 Shutdown 或关闭连接。服务期间周期运行附件
// 清除任务（未引用满 24 小时的待用附件；时钟可注入，见 internal/attachments）
// 和备份调度（每天 daily_at；备份时刻同样由注入时钟决定，见 internal/ops）。
func (s *Server) Serve(ln net.Listener) error {
	s.listener = ln
	s.httpSrv = &http.Server{Handler: s.handler}
	janitorDone := make(chan struct{})
	backupDone := make(chan struct{})
	idemDone := make(chan struct{})
	go s.runAttachmentJanitor(janitorDone)
	go s.Backups.RunSchedule(backupDone, ops.DefaultScheduleTick)
	go s.runIdempotencyJanitor(idemDone)
	defer func() { close(idemDone) }()
	defer func() { close(backupDone) }()
	defer func() { close(janitorDone) }()
	defer s.web.Close()
	err := s.httpSrv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// attachmentJanitorInterval 是附件清除任务的运行间隔（真实时钟；24 小时的
// 判定用注入时钟，测试里直接调 CleanupUnreferenced，不睡真实时间）。
const attachmentJanitorInterval = 10 * time.Minute

// runAttachmentJanitor 周期清除未引用的待用附件。清除失败只影响本轮
// （下一轮再试），不影响服务。
func (s *Server) runAttachmentJanitor(done <-chan struct{}) {
	ticker := time.NewTicker(attachmentJanitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			_, _ = s.Atts.CleanupUnreferenced(context.Background())
		}
	}
}

// idempotencyJanitorInterval 是幂等记录清除任务的运行间隔（真实时钟；24 小时
// 的判定用注入时钟，测试里直接调 PurgeExpired，不睡真实时间）。
const idempotencyJanitorInterval = 10 * time.Minute

// runIdempotencyJanitor 周期清除超过保留时长（24 小时）的幂等记录：快照里
// 可能带着访客链接密钥，不能无限期留在库里（评审修正）。
func (s *Server) runIdempotencyJanitor(done <-chan struct{}) {
	ticker := time.NewTicker(idempotencyJanitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			_, _ = s.Idem.PurgeExpired(context.Background())
		}
	}
}

// ListenAndServe 监听 127.0.0.1:<port> 并服务。平台只监听回环地址，HTTPS 由 Caddy 反代提供。
// 启动后做一次健康自检（更新流程的最后一步：新版本起进程时先迁移——
// store.Open 已做——再做健康检查，见 docs/deploy-and-release.md「更新」）。
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(s.cfg.Port)))
	if err != nil {
		return fmt.Errorf("监听 127.0.0.1:%d 失败：%w", s.cfg.Port, err)
	}
	fmt.Printf("taskboard 已启动：http://127.0.0.1:%d（数据目录 %s）\n", s.cfg.Port, s.cfg.DataDir)
	go s.selfHealthCheck()
	return s.Serve(ln)
}

// selfHealthCheck 启动后对自身做一次健康检查：通过则记录版本（更新成功的
// 标志），不通过只警告不退出——是否重启交给 NSSM；新版本起不来时用
// taskboard rollback 回滚。
func (s *Server) selfHealthCheck() {
	url := fmt.Sprintf("http://127.0.0.1:%d/api/v1/health", s.cfg.Port)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		res, err := http.Get(url)
		if err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				fmt.Printf("健康检查通过（后端 %s，前端 %s）\n", version.Server, updater.WebVersion(s.cfg.WebDir))
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Printf("警告：健康检查未通过（%s）：请查看日志；新版本有问题时执行 taskboard rollback 回滚\n", url)
}

// Shutdown 优雅停机。
func (s *Server) Shutdown(ctx context.Context) error {
	s.Notifications.Close()
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Shutdown(ctx)
}

// ShutdownTimeout 是优雅停机的等待上限。
const ShutdownTimeout = 5 * time.Second
