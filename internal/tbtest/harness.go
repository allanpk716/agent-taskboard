// Package tbtest 是黑盒测试底座：在临时 data_dir 上启动真实服务，
// 从外部发 HTTP 请求，时钟可注入，并提供造数据的辅助方法。
// 后续所有工单的 API 测试都复用这里的 Start。
package tbtest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/idempotency"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/notifications"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/server"
	"github.com/allanpk716/agent-taskboard/internal/store"
)

// Server 是测试用的真实服务实例。
type Server struct {
	TB testing.TB

	url               string
	clk               *clock.Fake
	dataDir           string
	webDir            string
	srv               *server.Server
	guestDeviceTokens map[string]string
}

// Option 定制 Start 的行为。
type Option func(*options)

type options struct {
	webDir      string
	clockStart  time.Time
	attachments config.Attachments
	backup      config.Backup
	update      config.Update
	intake      config.Intake
	publicURL   string
	minClient   string
}

// WithWebDir 指定前端目录（默认用临时目录里的一页 index.html 占位）。
func WithWebDir(dir string) Option { return func(o *options) { o.webDir = dir } }

// WithClockStart 指定假时钟的起点（默认 2026-10-16 09:00 北京时间）。
func WithClockStart(t time.Time) Option { return func(o *options) { o.clockStart = t } }

// WithAttachments 指定附件上限（默认 5MB 单文件、每帖 5 个附件 10 张插图、
// 整单 50MB）。测试里用小上限触发 413，不必打真实大小的载荷（#33）。
func WithAttachments(maxFileMB, maxFilesPerPost, maxImagesPerPost, maxTicketMB int) Option {
	return func(o *options) {
		o.attachments = config.Attachments{
			MaxFileMB: maxFileMB, MaxFilesPerPost: maxFilesPerPost,
			MaxImagesPerPost: maxImagesPerPost, MaxTicketMB: maxTicketMB,
		}
	}
}

// WithBackup 指定备份配置（#43；默认无目标，即 data_dir\backups\ + local_only）。
// 测试里用临时目录充当备份目标，或用“文件下的子路径”模拟连不上的目标。
func WithBackup(b config.Backup) Option { return func(o *options) { o.backup = b } }

// WithPublicURL 指定 [server] public_url（评审 P2-7：访客链接与 share_text
// 回绝对地址）；默认留空（相对路径）。
func WithPublicURL(url string) Option { return func(o *options) { o.publicURL = url } }

// WithMinClientVersion 指定平台要求的最低桌面客户端版本；默认空字符串。
func WithMinClientVersion(version string) Option {
	return func(o *options) {
		o.minClient = version
	}
}

// WithUpdate 指定更新源配置（#45；默认不配置任何源：管理端点会明确报
// “没有配置更新源”，测试里指向本地假源，不碰真实 GitHub/UpdateHub）。
func WithUpdate(u config.Update) Option { return func(o *options) { o.update = u } }

// WithIntake 指定入口匿名上传的限流与临时配额。
func WithIntake(ratePerMin, pendingQuotaMB int) Option {
	return func(o *options) {
		o.intake = config.Intake{UploadRatePerMin: ratePerMin, PendingQuotaMB: pendingQuotaMB}
	}
}

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
	// 先叠加测试指定的覆盖项，再补默认值（部分覆盖也能拿到默认上限）。
	if o.attachments != (config.Attachments{}) {
		cfg.Attachments = o.attachments
	}
	if o.backup != (config.Backup{}) {
		cfg.Backup = o.backup
	}
	if len(o.update.Sources) > 0 {
		cfg.Update = o.update
	}
	if o.intake != (config.Intake{}) {
		cfg.Intake = o.intake
	}
	if o.publicURL != "" {
		cfg.Server.PublicURL = o.publicURL
	}
	cfg.Server.MinClientVersion = o.minClient
	config.ApplyDefaults(&cfg) // 与 config.Load 一致：端口、附件上限等补默认值
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
		TB:                t,
		url:               "http://" + ln.Addr().String(),
		clk:               clk,
		dataDir:           dataDir,
		webDir:            webDir,
		srv:               srv,
		guestDeviceTokens: map[string]string{},
	}
}

// URL 返回服务根地址，如 http://127.0.0.1:52341。
func (s *Server) URL() string { return s.url }

// Clock 返回注入的假时钟。
func (s *Server) Clock() *clock.Fake { return s.clk }

// Shutdown 显式优雅停机；测试结束时的自动关闭会再执行一次，幂等。
func (s *Server) Shutdown(t testing.TB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), server.ShutdownTimeout)
	defer cancel()
	if err := s.srv.Shutdown(ctx); err != nil {
		t.Fatalf("停机失败：%v", err)
	}
}

// DataDir 返回本测试独占的数据目录。
func (s *Server) DataDir() string { return s.dataDir }

// WebDir 返回本测试使用的前端目录。
func (s *Server) WebDir() string { return s.webDir }

// Identity 暴露身份模块（后续工单造数据用，如建成员、发 Agent token）。
func (s *Server) Identity() *identity.Manager { return s.srv.Ident }

// Store 暴露数据库（测试造数据的快路径，如给「第 1000 张」编号测试预置占位工单；
// 断言外部行为仍应走 HTTP 接口）。
func (s *Server) Store() *store.Store { return s.srv.Store }

// Attachments 暴露附件模块（#33）：测试里直接触发 24 小时清除任务
// （服务里由定时器周期调用；测试不睡真实时间，用注入时钟推进后手动跑）。
func (s *Server) Attachments() *attachments.Manager { return s.srv.Atts }

// Backups 暴露备份模块（#43）：测试里直接做一次备份或触发调度检查
// （服务里由定时器周期调用；备份时刻由注入时钟决定）。
func (s *Server) Backups() *ops.Backups { return s.srv.Backups }

// Notifications 暴露通知模块（#177 系统通知测试用）。
func (s *Server) Notifications() *notifications.Manager { return s.srv.Notifications }

// Idempotency 暴露幂等模块（评审修正）：测试里直接触发 24 小时清除任务
// （服务里由定时器周期调用；不睡真实时间，用注入时钟推进后手动跑）。
func (s *Server) Idempotency() *idempotency.Manager { return s.srv.Idem }

// CreateAdmin 造一位管理员（等价于 taskboard create-admin 子命令做的事）。
func (s *Server) CreateAdmin(username, password string) {
	s.TB.Helper()
	if _, err := s.srv.Ident.CreateAdmin(context.Background(), username, username, password); err != nil {
		s.TB.Fatalf("创建管理员 %s 失败：%v", username, err)
	}
}

// CreateMember 造一位普通成员（标签治理等需要非管理员账号的测试用；
// 网页上的账号管理走 POST /api/v1/members（#42），测试里可直接用这个快路径）。
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

// GuestClient 返回已认领设备的 Bearer 客户端（模拟访客浏览器）。首次拿到
// 链接密钥时先走认领接口，之后复用返回的 tbgd_ 设备凭证。
func (s *Server) GuestClient(key string) *http.Client {
	if token, ok := s.guestDeviceTokens[key]; ok {
		return &http.Client{Transport: bearerTransport{token}}
	}
	if strings.HasPrefix(key, "tbgd_") {
		return &http.Client{Transport: bearerTransport{token: key}}
	}
	raw := &http.Client{Transport: bearerTransport{token: key}}
	var claimed struct {
		DeviceToken string `json:"device_token"`
	}
	res := s.Do(s.TB, raw, http.MethodPost, "/api/v1/guest-links/claim",
		map[string]string{"author_name": "检验科 张三"}, &claimed)
	if res.StatusCode != http.StatusCreated {
		// 撤销、过期等链接本来就不能认领；保留原始密钥，让后续请求验证其状态。
		return raw
	}
	s.guestDeviceTokens[key] = claimed.DeviceToken
	return &http.Client{Transport: bearerTransport{token: claimed.DeviceToken}}
}

// GuestCookieClient 返回只携带访客设备 cookie 的浏览器客户端；cookie 由认领、
// 访问码进入或入口提交接口下发。
func (s *Server) GuestCookieClient(token string) *http.Client {
	return &http.Client{Transport: guestCookieTransport{token: token}}
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
	res := s.Do(t, c, http.MethodPost, "/api/v1/me/agent-tokens",
		map[string]string{"name": name, "draft_mode": "direct"}, &issued)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("签发 Agent token %q 失败：状态码 %d", name, res.StatusCode)
	}
	if issued.Token == "" {
		t.Fatalf("签发 Agent token %q 成功但没有返回 token", name)
	}
	return issued.ID, issued.Token
}

// AgentTokenRow 是测试侧的 Agent token 列表行。
type AgentTokenRow struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	CreatedAt string  `json:"created_at"`
	RevokedAt *string `json:"revoked_at"`
}

// AgentTokens 读取网页会话当前成员名下的 Agent token 列表（不含明文 token）。
func (s *Server) AgentTokens(t testing.TB, c *http.Client) []AgentTokenRow {
	t.Helper()
	var got struct {
		Tokens []AgentTokenRow `json:"tokens"`
	}
	res := s.Do(t, c, http.MethodGet, "/api/v1/me/agent-tokens", nil, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("读取 Agent token 列表失败：状态码 %d", res.StatusCode)
	}
	return got.Tokens
}

// ClientConnect 是一次「连接这台电脑」请求得到的一次性 code 和回调信息。
type ClientConnect struct {
	Code         string
	State        string
	RedirectURL  string
	CodeVerifier string
}

// IssuedClientCredential 是客户端凭证的签发结果；明文 token 只出现一次。
type IssuedClientCredential struct {
	ID        int64  `json:"id"`
	Token     string `json:"token"`
	Hostname  string `json:"hostname"`
	CreatedAt string `json:"created_at"`
}

// StartClientConnect 模拟桌面客户端打开浏览器并由成员点击「连接」。
func (s *Server) StartClientConnect(t testing.TB, c *http.Client, hostname string) ClientConnect {
	t.Helper()
	var created struct {
		RedirectURL string `json:"redirect_url"`
	}
	res := s.Do(t, c, http.MethodPost, "/api/v1/client/connect", map[string]any{
		"port": 49161, "state": "client-state", "code_challenge": testClientChallenge(), "hostname": hostname,
	}, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("创建客户端连接 code 失败：状态码 %d", res.StatusCode)
	}
	u, err := url.Parse(created.RedirectURL)
	if err != nil {
		t.Fatalf("解析客户端回调失败：%v", err)
	}
	code := u.Query().Get("code")
	state := u.Query().Get("state")
	if code == "" || state == "" {
		t.Fatalf("客户端回调缺 code/state：%q", created.RedirectURL)
	}
	return ClientConnect{Code: code, State: state, RedirectURL: created.RedirectURL, CodeVerifier: testClientVerifier}
}

// ExchangeClientCode 模拟客户端用 code + PKCE verifier 换取凭证。
func (s *Server) ExchangeClientCode(t testing.TB, connect ClientConnect, verifier string) IssuedClientCredential {
	t.Helper()
	var issued IssuedClientCredential
	res := s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/client/token",
		map[string]string{"code": connect.Code, "code_verifier": verifier}, &issued)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("换取客户端凭证失败：状态码 %d", res.StatusCode)
	}
	if issued.Token == "" {
		t.Fatal("换取客户端凭证成功但没有 token")
	}
	return issued
}

// IssueClientCredential 走完整 PKCE 流程，为当前网页成员签发客户端凭证。
func (s *Server) IssueClientCredential(t testing.TB, c *http.Client, hostname string) IssuedClientCredential {
	t.Helper()
	return s.ExchangeClientCode(t, s.StartClientConnect(t, c, hostname), testClientVerifier)
}

// ClientCredentialClient 模拟桌面客户端调用 API。
func (s *Server) ClientCredentialClient(token string) *http.Client {
	return &http.Client{Transport: bearerTransport{token: token}}
}

func testClientChallenge() string {
	sum := sha256.Sum256([]byte(testClientVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

const testClientVerifier = "test-verifier-with-enough-characters-for-pkce-1234567890"

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

type guestCookieTransport struct {
	token string
}

func (t guestCookieTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Cookie", "tb_guest_device="+t.token)
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
