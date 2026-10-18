package client_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/client"
	"github.com/allanpk716/agent-taskboard/internal/clientstamp"
	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
	"github.com/allanpk716/agent-taskboard/internal/updater"
)

func Test自动更新就绪后替换exe并退出旧进程(t *testing.T) {
	fake := newFakeDesktop(t)
	s := fake.startService(t)
	webLi := s.Login("李工", "secret-pass")
	fake.browser.web = webLi
	fake.browser.serverURL = s.URL()
	newExe := testClientBinary(t, "1.2.0 new")
	installClientPackage(t, s, "1.2.0", newExe)
	fx := newUpdateFixture(t, s, testClientBinary(t, "0.2.0 old"))
	fake.lifecycle = fx.lifecycle
	fake.updater = fx.updater
	fake.clock = clock.NewFake(time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fake.startClient(t, ctx, s.URL())
	fake.waitState(t, client.TrayNormal)

	waitClient(t, done)
	fx.updater.waitStarted(t)
	stampedNew := string(newExe) + stampSuffix(t, s.URL())
	if got := readFile(t, fx.exePath); string(got) != stampedNew {
		t.Fatalf("更新后的 exe 内容 = %q, 想新版本", got)
	}
	if got := readFile(t, fx.exePath+".prev"); !strings.HasSuffix(string(got), "0.2.0 old") {
		t.Fatalf("上一版 exe 内容 = %q", got)
	}
	starts := fx.updater.startsList()
	if len(starts) != 1 || starts[0].path != fx.exePath ||
		strings.Join(starts[0].args, ",") != "--after-update" {
		t.Fatalf("新进程启动参数 = %+v", starts)
	}
	if len(fx.updater.stoppedList()) != 0 {
		t.Fatalf("就绪后不应结束新进程：%v", fx.updater.stoppedList())
	}
}

func Test自动更新三十秒无就绪时结束新进程并换回旧版(t *testing.T) {
	fake := newFakeDesktop(t)
	s := fake.startService(t)
	webLi := s.Login("李工", "secret-pass")
	fake.browser.web = webLi
	fake.browser.serverURL = s.URL()
	oldExe := testClientBinary(t, "0.2.0 old")
	newExe := testClientBinary(t, "1.2.0 new")
	installClientPackage(t, s, "1.2.0", newExe)
	fx := newUpdateFixture(t, s, oldExe)
	fx.updater.autoReady = false
	fake.lifecycle = fx.lifecycle
	fake.updater = fx.updater
	fake.clock = clock.NewFake(time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fake.startClient(t, ctx, s.URL())
	fake.waitState(t, client.TrayNormal)
	fake.clock.Advance(31 * time.Second)

	waitCondition(t, func() bool { return len(fx.updater.stoppedList()) == 1 })
	if got := readFile(t, fx.exePath); string(got) != string(oldExe) {
		t.Fatalf("回滚后的 exe 内容不符：前缀 %q", string(got[:len(oldExe)]))
	}
	if _, err := os.Stat(fx.exePath + ".prev"); !os.IsNotExist(err) {
		t.Fatalf(".prev 应随回滚换回：%v", err)
	}
	select {
	case err := <-done:
		t.Fatalf("旧客户端不应因更新失败退出：%v", err)
	default:
	}
	cancel()
	waitClient(t, done)
}

func Test自动更新SHA256不符时不替换并继续运行(t *testing.T) {
	fake := newFakeDesktop(t)
	s := fake.startService(t)
	webLi := s.Login("李工", "secret-pass")
	fake.browser.web = webLi
	fake.browser.serverURL = s.URL()
	oldExe := testClientBinary(t, "0.2.0 old")
	newExe := testClientBinary(t, "1.2.0 new")
	installClientPackage(t, s, "1.2.0", newExe)
	proxy := newBadLatestProxy(t, s.URL())
	t.Cleanup(proxy.Close)
	fake.browser.serverURL = proxy.URL
	fx := newUpdateFixture(t, s, oldExe)
	fake.lifecycle = fx.lifecycle
	fake.updater = fx.updater
	fake.clock = clock.NewFake(time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fake.startClient(t, ctx, proxy.URL)
	fake.waitState(t, client.TrayNormal)

	if got := readFile(t, fx.exePath); string(got) != string(oldExe) {
		t.Fatal("SHA256 不符时不应替换 exe")
	}
	if _, err := os.Stat(fx.exePath + ".download"); !os.IsNotExist(err) {
		t.Fatalf("临时文件应删除：%v", err)
	}
	if starts := fx.updater.startsList(); len(starts) != 0 {
		t.Fatalf("SHA256 不符时不应启动新进程：%+v", starts)
	}
	select {
	case err := <-done:
		t.Fatalf("客户端不应因校验失败退出：%v", err)
	default:
	}
	cancel()
	waitClient(t, done)
}

func Test自动更新每六小时检查一次新版本(t *testing.T) {
	fake := newFakeDesktop(t)
	s := fake.startService(t)
	webLi := s.Login("李工", "secret-pass")
	fake.browser.web = webLi
	fake.browser.serverURL = s.URL()
	oldExe := testClientBinary(t, "0.2.0 old")
	installClientPackage(t, s, "0.2.0", testClientBinary(t, "0.2.0 same"))
	fx := newUpdateFixture(t, s, oldExe)
	fake.lifecycle = fx.lifecycle
	fake.updater = fx.updater
	fake.clock = clock.NewFake(time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fake.startClient(t, ctx, s.URL())
	fake.waitState(t, client.TrayNormal)
	if starts := fx.updater.startsList(); len(starts) != 0 {
		t.Fatalf("版本不比自己新时不应启动新进程：%+v", starts)
	}

	installClientPackage(t, s, "1.2.0", testClientBinary(t, "1.2.0 new"))
	fake.clock.Advance(6 * time.Hour)
	fx.updater.waitStarted(t)
	waitClient(t, done)
	if got := readFile(t, fx.exePath); !strings.HasSuffix(string(got), stampSuffix(t, s.URL())) {
		t.Fatalf("定时检查后 exe = %q, 想新版本", got)
	}
}

func TestSSE重连收到hello后检查更新(t *testing.T) {
	fake := newFakeDesktop(t)
	s := fake.startService(t)
	webLi := s.Login("李工", "secret-pass")
	fake.browser.web = webLi
	fake.browser.serverURL = s.URL()
	oldExe := testClientBinary(t, "0.2.0 old")
	installClientPackage(t, s, "0.2.0", testClientBinary(t, "0.2.0 same"))
	proxy := newLatestCountProxy(t, s.URL())
	t.Cleanup(proxy.Close)
	fx := newUpdateFixture(t, s, oldExe)
	fake.lifecycle = fx.lifecycle
	fake.updater = fx.updater
	fake.clock = clock.NewFake(time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fake.startClient(t, ctx, proxy.URL)
	fake.waitState(t, client.TrayNormal)
	waitCondition(t, func() bool { return proxy.count() >= 1 })
	time.Sleep(100 * time.Millisecond) // 等待 streamCancel 挂上，托盘的重新登录/重连命令才会断开 SSE。
	installClientPackage(t, s, "1.2.0", testClientBinary(t, "1.2.0 new"))
	fake.tray.click(t, "reconnect")
	waitCondition(t, func() bool { return proxy.count() >= 2 })

	fx.updater.waitStarted(t)
	waitClient(t, done)
	if got := readFile(t, fx.exePath); !strings.HasSuffix(string(got), stampSuffix(t, proxy.URL)) {
		t.Fatalf("hello 后的 exe = %q, 想新版本", got)
	}
}

func TestAfterUpdate进程发就绪信号并弹更新Toast(t *testing.T) {
	fake := newFakeDesktop(t)
	s := fake.startService(t)
	webLi := s.Login("李工", "secret-pass")
	fake.browser.web = webLi
	fake.browser.serverURL = s.URL()
	fx := newUpdateFixture(t, s, testClientBinary(t, "0.2.0 old"))
	fake.lifecycle = fx.lifecycle
	fake.updater = fx.updater
	fake.clock = clock.NewFake(time.Now())
	oldRelease := fake.lock.AcquireMust(t)
	fake.updater.quitOld = oldRelease

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := client.New(client.Config{
		ServerURL: s.URL(), Version: "1.2.0", Hostname: "TEST-PC", Nonces: fixedNonces,
		RetryWait: func(context.Context, time.Duration) error { return nil }, AfterUpdate: true,
	}, client.Dependencies{
		Tray: fake.tray, Toaster: fake.toaster, Store: fake.store, Browser: fake.browser,
		Lock: fake.lock, Lifecycle: &fake.lifecycle, Update: fake.updater, Clock: fake.clock,
	})
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	go func() {
		for i := 0; i < 100 && fake.lock.acquiredCount() < 2; i++ {
			time.Sleep(time.Millisecond)
			fake.clock.Advance(100 * time.Millisecond)
		}
	}()

	fx.updater.waitStarted(t)
	if got := fake.lock.acquiredCount(); got != 2 {
		t.Fatalf("更新进程获取单实例锁次数 = %d, 想 2", got)
	}
	if fake.lock.heldByUpdate() != true {
		t.Fatal("更新完成后单实例锁应由新进程持有")
	}
	toasts := fake.toaster.shown(t)
	if len(toasts) == 0 || toasts[0].Text != "客户端已更新到 1.2.0" {
		t.Fatalf("更新 Toast = %+v", toasts)
	}
	cancel()
	oldRelease()
	waitClient(t, done)
}

func Test版本过旧时停止拉取通知并提示下载(t *testing.T) {
	fake := newFakeDesktop(t)
	s := tbtest.Start(t, tbtest.WithMinClientVersion("1.0.0"))
	s.CreateAdmin("王工", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	webLi := s.Login("李工", "secret-pass")
	fake.browser.web = webLi
	fake.browser.serverURL = s.URL()
	fake.clock = clock.NewFake(time.Now())
	proxy := newLatestCountProxy(t, s.URL())
	t.Cleanup(proxy.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fake.startClient(t, ctx, proxy.URL)
	fake.waitState(t, client.TrayOutdated)
	toasts := fake.toaster.shown(t)
	if len(toasts) != 1 || toasts[0].Reason != "outdated" || toasts[0].Ticket.URL != "/me#client" {
		t.Fatalf("版本过旧 Toast = %+v", toasts)
	}
	fake.toaster.click(t, toasts[0])
	if got := fake.browser.opened(t); got != proxy.URL+"/me#client" {
		t.Fatalf("点击打开 = %q, 想 %q", got, proxy.URL+"/me#client")
	}
	if streams := proxy.streams(); streams != 0 {
		t.Fatalf("版本过旧时不应建立 SSE，连接数 = %d", streams)
	}
	cancel()
	waitClient(t, done)
}

type latestCountProxy struct {
	*httptest.Server
	mu     sync.Mutex
	checks int
	conns  int
}

func newLatestCountProxy(t testing.TB, backend string) *latestCountProxy {
	t.Helper()
	p := &latestCountProxy{}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/client/latest" {
			p.mu.Lock()
			p.checks++
			p.mu.Unlock()
		}
		if r.URL.Path == "/api/v1/notifications/stream" {
			p.mu.Lock()
			p.conns++
			p.mu.Unlock()
		}
		proxy := httputil.NewSingleHostReverseProxy(backendURL(t, backend))
		proxy.FlushInterval = -1
		proxy.ServeHTTP(w, r)
	}))
	return p
}

func backendURL(t testing.TB, backend string) *url.URL {
	t.Helper()
	u, err := url.Parse(backend)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (p *latestCountProxy) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.checks
}

func (p *latestCountProxy) streams() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.conns
}

func stampSuffix(t testing.TB, serverURL string) string {
	t.Helper()
	stamped, err := clientstamp.Append([]byte{}, serverURL)
	if err != nil {
		t.Fatal(err)
	}
	return string(stamped)
}

type updateFixture struct {
	dir       string
	exePath   string
	lifecycle client.LifecycleDependencies
	updater   *fakeUpdateAdapter
}

func newUpdateFixture(t testing.TB, s *tbtest.Server, oldExe []byte) updateFixture {
	t.Helper()
	dir := t.TempDir()
	exePath := filepath.Join(dir, "taskboard-client.exe")
	if err := os.WriteFile(exePath, oldExe, 0o755); err != nil {
		t.Fatal(err)
	}
	files := &fakeFileSystem{
		download: exePath, installDir: dir, installPath: exePath, executable: exePath,
	}
	lifecycle := client.LifecycleDependencies{
		Config: &fakeServerConfig{}, Autostart: &fakeAutostart{}, Toast: &fakeToastRegistration{},
		Files: files, Process: &fakeProcessManager{},
	}
	return updateFixture{dir: dir, exePath: exePath, lifecycle: lifecycle, updater: newFakeUpdateAdapter()}
}

func testClientBinary(t testing.TB, suffix string) []byte {
	t.Helper()
	b := make([]byte, 0x80)
	copy(b, "MZ")
	b[0x3c], b[0x3d], b[0x3e], b[0x3f] = 0x40, 0, 0, 0
	copy(b[0x40:], "PE\x00\x00")
	b[0x44], b[0x45] = 0x64, 0x86
	return append(b, suffix...)
}

func installClientPackage(t testing.TB, s *tbtest.Server, version string, data []byte) {
	t.Helper()
	sum := sha256.Sum256(data)
	pkg := updater.ClientPackage{
		Version: version, Sha256: hex.EncodeToString(sum[:]), Size: int64(len(data)),
	}
	for attempt := 0; ; attempt++ {
		err := updater.InstallClient(s.DataDir(), data, pkg)
		if err == nil {
			return
		}
		if attempt == 20 {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type fakeUpdateAdapter struct {
	mu        sync.Mutex
	starts    []fakeStart
	stopped   []string
	signaled  chan struct{}
	once      sync.Once
	autoReady bool
	quitOld   func()
	quit      chan struct{}
}

func newBadLatestProxy(t testing.TB, backend string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/client/latest" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"1.2.0","sha256":"bad","size":1,"download_url":"/api/v1/client/download"}`))
			return
		}
		req, err := http.NewRequestWithContext(r.Context(), r.Method, backend+r.URL.RequestURI(), r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		req.Header = r.Header.Clone()
		res, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer res.Body.Close()
		for k, vs := range res.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(res.StatusCode)
		_, _ = io.Copy(w, res.Body)
	}))
}

func newFakeUpdateAdapter() *fakeUpdateAdapter {
	return &fakeUpdateAdapter{signaled: make(chan struct{}), quit: make(chan struct{}), autoReady: true}
}

func (u *fakeUpdateAdapter) Start(path string, args []string) (client.UpdateProcess, error) {
	u.mu.Lock()
	u.starts = append(u.starts, fakeStart{path: path, args: args})
	u.mu.Unlock()
	proc := fakeUpdateProcess{owner: u, path: path}
	if u.autoReady {
		go func() {
			time.Sleep(20 * time.Millisecond)
			_ = u.SendReady()
		}()
	}
	return proc, nil
}

func (u *fakeUpdateAdapter) Ready() <-chan struct{} { return u.signaled }

func (u *fakeUpdateAdapter) Quit() <-chan struct{} { return u.quit }

func (u *fakeUpdateAdapter) SendReady() error {
	u.once.Do(func() { close(u.signaled) })
	return nil
}

func (u *fakeUpdateAdapter) SendQuit() error {
	u.mu.Lock()
	quit := u.quitOld
	quitCh := u.quit
	u.mu.Unlock()
	if quit != nil {
		quit()
	}
	close(quitCh)
	return nil
}

func (u *fakeUpdateAdapter) waitStarted(t testing.TB) {
	t.Helper()
	select {
	case <-u.signaled:
	case <-time.After(2 * time.Second):
		t.Fatal("等待新进程就绪超时")
	}
}

func (u *fakeUpdateAdapter) startsList() []fakeStart {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]fakeStart(nil), u.starts...)
}

func (u *fakeUpdateAdapter) stoppedList() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.stopped...)
}

type fakeUpdateProcess struct {
	owner *fakeUpdateAdapter
	path  string
}

func (p fakeUpdateProcess) Stop() error {
	p.owner.mu.Lock()
	p.owner.stopped = append(p.owner.stopped, p.path)
	p.owner.mu.Unlock()
	return nil
}
