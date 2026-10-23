package client_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/client"
	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/release"
)

func Test检查到客户端新版本只提示确认后更新(t *testing.T) {
	fake := newFakeDesktop(t)
	s := fake.startService(t)
	webLi := s.Login("李工", "secret-pass")
	fake.browser.web = webLi
	fake.browser.serverURL = s.URL()
	oldExe := testClientBinary(t, "0.2.0 old")
	newExe := testClientBinary(t, "1.2.0 new")
	fx := newUpdateFixture(t, oldExe)
	_ = newFakeGitHubRelease(t, "1.2.0", newExe, false)
	fake.lifecycle = fx.lifecycle
	fake.updater = fx.updater
	fake.clock = clock.NewFake(time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fake.startClient(t, ctx, s.URL())
	fake.waitState(t, client.TrayNormal)
	toasts := fake.toaster.waitUpdate(t)
	if len(toasts) != 1 || toasts[0].Text != "Taskboard 客户端有新版本 1.2.0，点击更新" {
		t.Fatalf("更新 Toast = %+v", toasts)
	}
	if got := fake.tray.updateVersion(); got != "1.2.0" {
		t.Fatalf("托盘更新版本 = %q", got)
	}
	fake.toaster.click(t, toasts[0])
	fx.updater.waitStarted(t)
	waitClient(t, done)
	if got := readFile(t, fx.exePath); string(got) != string(newExe) {
		t.Fatalf("更新后的 exe 内容 = %q", got)
	}
	if got := readFile(t, fx.exePath+".prev"); !strings.HasSuffix(string(got), "0.2.0 old") {
		t.Fatalf("上一版 exe 内容 = %q", got)
	}
}

func Test更新下载校验失败时提示并保留重试菜单(t *testing.T) {
	fake := newFakeDesktop(t)
	s := fake.startService(t)
	webLi := s.Login("李工", "secret-pass")
	fake.browser.web = webLi
	fake.browser.serverURL = s.URL()
	oldExe := testClientBinary(t, "0.2.0 old")
	fx := newUpdateFixture(t, oldExe)
	_ = newFakeGitHubRelease(t, "1.2.0", testClientBinary(t, "1.2.0 tampered"), true)
	fake.lifecycle = fx.lifecycle
	fake.updater = fx.updater

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fake.startClient(t, ctx, s.URL())
	fake.waitState(t, client.TrayNormal)
	toasts := fake.toaster.waitUpdate(t)
	fake.toaster.click(t, toasts[0])
	waitCondition(t, func() bool { return len(fake.toaster.shown(t)) >= 2 })
	if got := readFile(t, fx.exePath); string(got) != string(oldExe) {
		t.Fatal("校验失败时不应替换 exe")
	}
	if _, err := os.Stat(fx.exePath + ".download"); !os.IsNotExist(err) {
		t.Fatalf("临时文件应删除：%v", err)
	}
	if got := fake.tray.updateVersion(); got != "1.2.0" {
		t.Fatalf("重试菜单版本 = %q", got)
	}
	cancel()
	waitClient(t, done)
}

func Test更新三十秒无就绪时回滚旧版(t *testing.T) {
	fake := newFakeDesktop(t)
	s := fake.startService(t)
	webLi := s.Login("李工", "secret-pass")
	fake.browser.web = webLi
	fake.browser.serverURL = s.URL()
	oldExe := testClientBinary(t, "0.2.0 old")
	fx := newUpdateFixture(t, oldExe)
	fx.updater.autoReady = false
	_ = newFakeGitHubRelease(t, "1.2.0", testClientBinary(t, "1.2.0 new"), false)
	fake.lifecycle = fx.lifecycle
	fake.updater = fx.updater
	fake.clock = clock.NewFake(time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fake.startClient(t, ctx, s.URL())
	fake.waitState(t, client.TrayNormal)
	updates := fake.toaster.waitUpdate(t)
	go fake.toaster.clickAsync(updates[0])
	waitForFakeTimer(t, fake.clock, 30*time.Second)
	fake.clock.Advance(31 * time.Second)
	waitCondition(t, func() bool { return len(fx.updater.stoppedList()) == 1 })
	waitCondition(t, func() bool {
		data, err := os.ReadFile(fx.exePath)
		return err == nil && string(data) == string(oldExe)
	})
	cancel()
	waitClient(t, done)
}

func Test托盘修改平台地址撤销旧凭证并连接新平台(t *testing.T) {
	fake := newFakeDesktop(t)
	oldServer := fake.startService(t)
	webLi := oldServer.Login("李工", "secret-pass")
	fake.browser.web = webLi
	fake.browser.serverURL = oldServer.URL()

	newServer := newFakeDesktop(t).startService(t)
	newWeb := newServer.Login("王工", "secret-pass")
	prompter := &fakeServerURLPrompter{value: newServer.URL()}
	fake.prompter = prompter

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fake.startClient(t, ctx, oldServer.URL())
	fake.waitState(t, client.TrayNormal)
	if clients := listClients(t, oldServer, webLi); len(clients) != 1 {
		t.Fatalf("切换前旧平台应有 1 个客户端凭证，实际 %d", len(clients))
	}
	fake.browser.web = newWeb
	fake.browser.serverURL = newServer.URL()
	fake.tray.click(t, "change-server")
	waitCondition(t, func() bool { return prompter.prompted })
	fake.waitState(t, client.TrayNormal)
	// 旧凭证必须在旧平台上撤销，而不是发给新地址。
	if clients := listClients(t, oldServer, webLi); len(clients) != 0 {
		t.Fatalf("切换后旧平台的客户端凭证应已撤销，实际还剩 %d", len(clients))
	}
	cancel()
	waitClient(t, done)
}

type fakeGitHubRelease struct {
	*httptest.Server
}

func newFakeGitHubRelease(t *testing.T, version string, exe []byte, corrupt bool) *fakeGitHubRelease {
	t.Helper()
	sum := sha256.Sum256(exe)
	sha := hex.EncodeToString(sum[:])
	if corrupt {
		sha = strings.Repeat("0", 64)
	}
	manifest := release.Manifest{Component: "client", Version: version, Files: []release.FileEntry{{
		Name: "taskboard-client.exe", Sha256: sha, Size: int64(len(exe)),
	}}}
	manifestURL := "/releases/download/client/v" + version + "/manifest.json"
	exeURL := "/releases/download/client/v" + version + "/taskboard-client.exe"
	var server *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/download/channel-stable/latest.json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(release.Latest{Client: &release.LatestEntry{
			Version: version, Tag: "client/v" + version, ManifestURL: server.URL + manifestURL,
		}})
	})
	mux.HandleFunc(manifestURL, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(manifest)
	})
	mux.HandleFunc(exeURL, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(exe)
	})
	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	t.Setenv("TASKBOARD_CLIENT_UPDATE_BASE", server.URL)
	return &fakeGitHubRelease{Server: server}
}

type updateFixture struct {
	dir       string
	exePath   string
	lifecycle client.LifecycleDependencies
	updater   *fakeUpdateAdapter
}

func newUpdateFixture(t testing.TB, oldExe []byte) updateFixture {
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

type fakeUpdateAdapter struct {
	mu         sync.Mutex
	starts     []fakeStart
	stopped    []string
	signaled   chan struct{}
	once       sync.Once
	autoReady  bool
	quitOld    func()
	quit       chan struct{}
	quitClosed bool
	resetCount int
}

func newFakeUpdateAdapter() *fakeUpdateAdapter {
	return &fakeUpdateAdapter{signaled: make(chan struct{}), quit: make(chan struct{}), autoReady: true}
}

func (u *fakeUpdateAdapter) Start(path string, args []string) (client.UpdateProcess, error) {
	u.mu.Lock()
	u.starts = append(u.starts, fakeStart{path: path, args: args})
	u.mu.Unlock()
	if u.autoReady {
		go func() {
			_, _ = u.SendReady(), struct{}{}
		}()
	}
	return fakeUpdateProcess{owner: u, path: path}, nil
}

func (u *fakeUpdateAdapter) Ready() <-chan struct{} { return u.signaled }
func (u *fakeUpdateAdapter) Quit() <-chan struct{}  { return u.quit }
func (u *fakeUpdateAdapter) SendReady() error {
	u.once.Do(func() { close(u.signaled) })
	return nil
}
func (u *fakeUpdateAdapter) SendQuit() error {
	u.mu.Lock()
	quit := u.quitOld
	if !u.quitClosed && u.quit != nil {
		close(u.quit)
		u.quitClosed = true
	}
	u.mu.Unlock()
	if quit != nil {
		quit()
	}
	return nil
}
func (u *fakeUpdateAdapter) ResetQuit() error { u.resetCount++; return nil }
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
