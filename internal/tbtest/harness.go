// Package tbtest 是黑盒测试底座：在临时 data_dir 上启动真实服务，
// 从外部发 HTTP 请求，时钟可注入，并提供造数据的辅助方法。
// 后续所有工单的 API 测试都复用这里的 Start。
package tbtest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/server"
	"github.com/allanpk716/agent-taskboard/internal/store"
)

// Server 是测试用的真实服务实例。
type Server struct {
	TB testing.TB

	url     string
	clk     *clock.Fake
	dataDir string
	webDir  string
	srv     *server.Server
}

// Option 定制 Start 的行为。
type Option func(*options)

type options struct {
	webDir     string
	clockStart time.Time
}

// WithWebDir 指定前端目录（默认用临时目录里的一页 index.html 占位）。
func WithWebDir(dir string) Option { return func(o *options) { o.webDir = dir } }

// WithClockStart 指定假时钟的起点（默认 2026-10-16 09:00 北京时间）。
func WithClockStart(t time.Time) Option { return func(o *options) { o.clockStart = t } }

// defaultClockStart 是假时钟默认起点：一个工作日的上午。
var defaultClockStart = time.Date(2026, 10, 16, 9, 0, 0, 0, clock.Beijing)

// Start 启动一个真实服务：临时 data_dir、临时前端目录、随机回环端口、假时钟。
// 服务随测试结束自动关闭。
func Start(t testing.TB, opts ...Option) *Server {
	t.Helper()
	o := options{clockStart: defaultClockStart}
	for _, opt := range opts {
		opt(&o)
	}

	dataDir := t.TempDir()
	webDir := o.webDir
	if webDir == "" {
		webDir = t.TempDir()
		if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<!doctype html><title>tbtest</title>"), 0o644); err != nil {
			t.Fatalf("写占位 index.html 失败：%v", err)
		}
	}

	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("打开数据库失败：%v", err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := config.Config{DataDir: dataDir, WebDir: webDir}
	clk := clock.NewFake(o.clockStart)
	srv, err := server.New(cfg, st, clk)
	if err != nil {
		t.Fatalf("装配服务失败：%v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败：%v", err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), server.ShutdownTimeout)
		defer cancel()
		_ = srv.Shutdown(ctx)
		<-done
	})

	return &Server{
		TB:      t,
		url:     "http://" + ln.Addr().String(),
		clk:     clk,
		dataDir: dataDir,
		webDir:  webDir,
		srv:     srv,
	}
}

// URL 返回服务根地址，如 http://127.0.0.1:52341。
func (s *Server) URL() string { return s.url }

// Clock 返回注入的假时钟。
func (s *Server) Clock() *clock.Fake { return s.clk }

// DataDir 返回本测试独占的数据目录。
func (s *Server) DataDir() string { return s.dataDir }

// WebDir 返回本测试使用的前端目录。
func (s *Server) WebDir() string { return s.webDir }

// Identity 暴露身份模块（后续工单造数据用，如建成员、发 Agent token）。
func (s *Server) Identity() *identity.Manager { return s.srv.Ident }

// Store 暴露数据库（测试造数据的快路径，如给「第 1000 张」编号测试预置占位工单；
// 断言外部行为仍应走 HTTP 接口）。
func (s *Server) Store() *store.Store { return s.srv.Store }

// CreateAdmin 造一位管理员（等价于 taskboard create-admin 子命令做的事）。
func (s *Server) CreateAdmin(username, password string) {
	s.TB.Helper()
	if _, err := s.srv.Ident.CreateAdmin(context.Background(), username, username, password); err != nil {
		s.TB.Fatalf("创建管理员 %s 失败：%v", username, err)
	}
}

// CreateMember 造一位普通成员（标签治理等需要非管理员账号的测试用；
// 成员账号管理界面在 #42）。
func (s *Server) CreateMember(username, password string) {
	s.TB.Helper()
	if _, err := s.srv.Ident.CreateMember(context.Background(), username, username, password); err != nil {
		s.TB.Fatalf("创建成员 %s 失败：%v", username, err)
	}
}

// NewClient 返回不带任何凭证的 HTTP 客户端。
func (s *Server) NewClient() *http.Client { return &http.Client{} }

// Login 用用户名密码登录，返回带会话 cookie 的客户端。
// Go 的 http.Client 只在 HTTPS 上发送 Secure cookie，而测试服务是 HTTP，
// 所以这里手工把 cookie 固定在后续请求上（浏览器对 127.0.0.1 没有这个限制）。
func (s *Server) Login(username, password string) *http.Client {
	s.TB.Helper()
	res := s.Do(s.TB, s.NewClient(), http.MethodPost, "/api/v1/session",
		map[string]string{"username": username, "password": password}, nil)
	if res.StatusCode != http.StatusOK {
		s.TB.Fatalf("登录 %s 失败：状态码 %d", username, res.StatusCode)
	}
	cookie := sessionCookieFrom(res)
	if cookie == "" {
		s.TB.Fatalf("登录 %s 成功但没有会话 cookie", username)
	}
	return s.cookieClient(cookie)
}

func (s *Server) cookieClient(cookie string) *http.Client {
	return &http.Client{Transport: cookieTransport{cookie: cookie}}
}

// AgentClient 返回带 Bearer Agent token 的 HTTP 客户端（模拟成员 Agent 调 API）。
func (s *Server) AgentClient(token string) *http.Client {
	return &http.Client{Transport: bearerTransport{token: token}}
}

// GuestClient 返回带 Bearer 访客链接密钥的 HTTP 客户端（模拟访客及其 Agent；
// 访客链接 /g/<密钥> 的密钥直接作 Bearer token，#32）。
func (s *Server) GuestClient(key string) *http.Client {
	return &http.Client{Transport: bearerTransport{token: key}}
}

// CreateGuestLink 用成员凭证为 ref 指向的工单创建一条访客链接，返回响应结构
// （含只出现一次的完整地址 /g/<密钥>）。造数据辅助，等价于走 HTTP 接口。
func (s *Server) CreateGuestLink(t testing.TB, c *http.Client, ref string, body map[string]any) GuestLink {
	s.TB.Helper()
	res := s.Do(t, c, http.MethodPost, "/api/v1/tickets/"+ref+"/guest-links", body, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("创建访客链接失败：状态码 %d", res.StatusCode)
	}
	var link GuestLink
	DecodeJSON(t, res, &link)
	return link
}

// GuestLink 是访客链接的对外结构（测试侧）。
type GuestLink struct {
	ID           int64  `json:"id"`
	URL          string `json:"url"`
	Note         string `json:"note"`
	ExpiresAt    string `json:"expires_at"`
	CanEditBrief bool   `json:"can_edit_brief"`
}

// Key 返回链接密钥（从 /g/<密钥> 地址里取出，直接作 Bearer token）。
func (l GuestLink) Key(t testing.TB) string {
	t.Helper()
	if !strings.HasPrefix(l.URL, "/g/") {
		t.Fatalf("访客链接地址 = %q, 想以 /g/ 开头", l.URL)
	}
	return strings.TrimPrefix(l.URL, "/g/")
}

// IssueAgentToken 用网页会话为当前成员签发一个具名 Agent token，返回 (id, 明文 token)。
// 明文只在签发响应里出现一次。
func (s *Server) IssueAgentToken(t testing.TB, c *http.Client, name string) (int64, string) {
	s.TB.Helper()
	var issued struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	res := s.Do(t, c, http.MethodPost, "/api/v1/me/agent-tokens", map[string]string{"name": name}, &issued)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("签发 Agent token %q 失败：状态码 %d", name, res.StatusCode)
	}
	if issued.Token == "" {
		t.Fatalf("签发 Agent token %q 成功但没有返回 token", name)
	}
	return issued.ID, issued.Token
}

type cookieTransport struct {
	cookie string
}

func (t cookieTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Cookie", httpapiSessionCookie+"="+t.cookie)
	return http.DefaultTransport.RoundTrip(clone)
}

const httpapiSessionCookie = "tb_session"

type bearerTransport struct {
	token string
}

func (t bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(clone)
}

func sessionCookieFrom(res *http.Response) string {
	for _, c := range res.Cookies() {
		if c.Name == httpapiSessionCookie && c.Value != "" {
			return c.Value
		}
	}
	return ""
}

// Do 发一个 JSON 请求并把响应体解码进 out（为 nil 时跳过）。返回原始响应。
func (s *Server) Do(t testing.TB, c *http.Client, method, path string, in, out any) *http.Response {
	t.Helper()
	return s.DoWithHeaders(t, c, method, path, nil, in, out)
}

// DoWithHeaders 同 Do，但可附加请求头（如 Idempotency-Key）；
// out 为 nil 时不读响应体，调用方可以自己 io.ReadAll 后比较字节。
func (s *Server) DoWithHeaders(t testing.TB, c *http.Client, method, path string, headers map[string]string, in, out any) *http.Response {
	t.Helper()
	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("构造请求体失败：%v", err)
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, s.url+path, body)
	if err != nil {
		t.Fatalf("构造请求失败：%v", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s 请求失败：%v", method, path, err)
	}
	t.Cleanup(func() { res.Body.Close() })
	if out != nil {
		DecodeJSON(t, res, out)
	}
	return res
}

// DecodeJSON 把响应体解码进 v。
func DecodeJSON(t testing.TB, res *http.Response, v any) {
	t.Helper()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("读响应体失败：%v", err)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		t.Fatalf("%s %s 响应体为空，无法按 JSON 解码", res.Request.Method, res.Request.URL.Path)
	}
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("响应体不是合法 JSON：%s\n%v", truncate(body), err)
	}
}

func truncate(b []byte) string {
	if len(b) > 500 {
		return string(b[:500]) + "…"
	}
	return strings.TrimSpace(string(b))
}
