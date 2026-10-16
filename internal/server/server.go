// Package server 把各模块装配成可监听的 HTTP 服务。
package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/httpapi"
	"github.com/allanpk716/agent-taskboard/internal/idempotency"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/labels"
	"github.com/allanpk716/agent-taskboard/internal/store"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
	"github.com/allanpk716/agent-taskboard/internal/webapp"
)

// Server 是一个运行中的 taskboard 服务实例。
type Server struct {
	cfg   config.Config
	Ident *identity.Manager
	Store *store.Store
	Atts  *attachments.Manager

	handler  http.Handler
	web      *webapp.Handler
	httpSrv  *http.Server
	listener net.Listener
}

// New 打开数据库、装配 API 与静态站点。webDir 不存在会报错。
// 首次启动时导入标签字典的一次性种子（spec「初始数据」）。
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
	api := httpapi.NewAPI(ident, tickets.NewManager(st.DB, c, labelDict, atts), idempotency.NewManager(st.DB), atts)
	web, err := webapp.New(cfg.WebDir)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle("/api/", api.Handler())
	mux.Handle("/", web)

	return &Server{
		cfg:     cfg,
		Ident:   ident,
		Store:   st,
		Atts:    atts,
		handler: mux,
		web:     web,
	}, nil
}

// Handler 返回完整路由（测试和反向代理调试用）。
func (s *Server) Handler() http.Handler { return s.handler }

// Serve 在 ln 上对外服务，直到 Shutdown 或关闭连接。服务期间周期运行附件
// 清除任务（未引用满 24 小时的待用附件；时钟可注入，见 internal/attachments）。
func (s *Server) Serve(ln net.Listener) error {
	s.listener = ln
	s.httpSrv = &http.Server{Handler: s.handler}
	janitorDone := make(chan struct{})
	go s.runAttachmentJanitor(janitorDone)
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

// ListenAndServe 监听 127.0.0.1:<port> 并服务。平台只监听回环地址，HTTPS 由 Caddy 反代提供。
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(s.cfg.Port)))
	if err != nil {
		return fmt.Errorf("监听 127.0.0.1:%d 失败：%w", s.cfg.Port, err)
	}
	fmt.Printf("taskboard 已启动：http://127.0.0.1:%d（数据目录 %s）\n", s.cfg.Port, s.cfg.DataDir)
	return s.Serve(ln)
}

// Shutdown 优雅停机。
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Shutdown(ctx)
}

// ShutdownTimeout 是优雅停机的等待上限。
const ShutdownTimeout = 5 * time.Second
