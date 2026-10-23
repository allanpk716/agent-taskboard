package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/client"
)

func Test首次运行弹框探测通过后保存平台地址(t *testing.T) {
	var path string
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health" {
			http.NotFound(w, r)
			return
		}
		path = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]string{"server_version": "1.0.0"})
	}))
	t.Cleanup(health.Close)
	store := &fakeServerConfig{}
	prompter := &fakeServerURLPrompter{value: health.URL}

	got, ok, err := client.ResolveInteractiveServerURL(context.Background(), "", "", store, prompter, nil)
	if err != nil || !ok {
		t.Fatalf("解析 = %q, %v, %v", got, ok, err)
	}
	if got != health.URL || store.savedURL != health.URL {
		t.Fatalf("平台地址 = %q/%q", got, store.savedURL)
	}
	if path != "/api/v1/health" {
		t.Fatalf("没有探测 health：%q", path)
	}
}

func Test探测失败时在弹框中显示错误并允许重填(t *testing.T) {
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(health.Close)
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"server_version":"1.0.0"}`))
	}))
	t.Cleanup(good.Close)
	store := &fakeServerConfig{}
	prompter := &recordingPrompter{values: []string{health.URL, good.URL}}

	got, ok, err := client.ResolveInteractiveServerURL(context.Background(), "", "", store, prompter, nil)
	if err != nil || !ok || got != good.URL {
		t.Fatalf("解析 = %q, %v, %v；想 %q", got, ok, err, good.URL)
	}
	if len(prompter.errs) != 2 || prompter.errs[0] != nil || prompter.errs[1] == nil {
		t.Fatalf("弹框错误序列 = %+v", prompter.errs)
	}
	if store.savedURL != good.URL {
		t.Fatalf("失败地址不应保存，实际 = %q", store.savedURL)
	}
}

func Test平台地址按参数环境变量已保存地址依次解析(t *testing.T) {
	cases := []struct {
		name string
		arg  string
		env  string
		want string
		ok   bool
	}{
		{"参数最高", "https://arg.example.com", "https://env.example.com", "https://arg.example.com", true},
		{"环境变量高于已保存", "", "https://env.example.com", "https://env.example.com", true},
		{"已保存", "", "", "https://saved.example.com", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeServerConfig{savedURL: "https://saved.example.com"}
			got, err := client.ResolveServerURL(tc.arg, tc.env, store)
			if tc.ok && (err != nil || got != tc.want) {
				t.Fatalf("解析 = %q, %v；想 %q", got, err, tc.want)
			}
			if !tc.ok && err == nil {
				t.Fatalf("应报错，得到 %q", got)
			}
		})
	}
	if _, err := client.ResolveServerURL("", "", nil); err == nil {
		t.Fatal("没有任何地址来源时应报错")
	}
}

func Test安装目录外启动会复制保存地址写自启并重启(t *testing.T) {
	fx := newInstallFixture(t)
	fx.files.executable = fx.download

	installed, err := client.InstallIfNeeded(fx.deps(), "https://taskboard.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Fatal("应请求本进程退出并从安装位置重启")
	}
	if got := readFile(t, fx.files.installPath); string(got) != string(readFile(t, fx.download)) {
		t.Fatal("安装目录内容应是下载的 exe")
	}
	if fx.config.savedURL != "https://taskboard.example.com" {
		t.Fatalf("保存的平台地址 = %q", fx.config.savedURL)
	}
	if fx.autostart.command != `"`+fx.files.installPath+`" --autostart` {
		t.Fatalf("自启命令 = %q", fx.autostart.command)
	}
	if len(fx.process.started) != 1 || fx.process.started[0].path != fx.files.installPath {
		t.Fatalf("重启进程 = %+v", fx.process.started)
	}
}

func Test首次安装提示在重启前显示且安装目录内不显示(t *testing.T) {
	fx := newInstallFixture(t)
	fx.files.executable = fx.download
	if _, err := client.InstallIfNeeded(fx.deps(), "https://taskboard.example.com"); err != nil {
		t.Fatal(err)
	}
	if got := *fx.events; len(got) != 2 || got[0] != "dialog" || got[1] != "start" {
		t.Fatalf("首次安装事件顺序 = %v，想 [dialog start]", got)
	}

	fx.files.executable = fx.files.installPath
	*fx.events = nil
	if _, err := client.InstallIfNeeded(fx.deps(), "https://taskboard.example.com"); err != nil {
		t.Fatal(err)
	}
	if len(*fx.events) != 0 {
		t.Fatalf("安装目录内启动不应弹首次安装提示：%v", *fx.events)
	}
}

func Test覆盖安装且安装目录内不再复制(t *testing.T) {
	fx := newInstallFixture(t)
	fx.files.executable = fx.download
	if _, err := client.InstallIfNeeded(fx.deps(), "https://taskboard.example.com"); err != nil {
		t.Fatal(err)
	}
	fx.files.executable = fx.files.installPath
	fx.process.started = nil
	installed, err := client.InstallIfNeeded(fx.deps(), "https://new.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if installed {
		t.Fatal("安装目录内启动不应退出重启")
	}
	if len(fx.files.copies) != 1 {
		t.Fatalf("不应再次复制：%+v", fx.files.copies)
	}
	if fx.config.savedURL != "https://new.example.com" {
		t.Fatalf("新平台地址 = %q", fx.config.savedURL)
	}
}

func Test旧版运行时覆盖安装先改名并通知退出(t *testing.T) {
	fx := newInstallFixture(t)
	if err := os.WriteFile(fx.files.installPath, []byte("MZ old running"), 0o755); err != nil {
		t.Fatal(err)
	}
	fx.files.executable = fx.download
	fx.files.occupiedTarget = true

	installed, err := client.InstallIfNeeded(fx.deps(), "https://taskboard.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Fatal("覆盖安装后仍应从安装位置重启")
	}
	if got := readFile(t, fx.files.installPath); string(got) != "MZ downloaded" {
		t.Fatalf("安装后的 exe = %q, 想新下载内容", got)
	}
	if got := readFile(t, fx.files.installPath+".old"); string(got) != "MZ old running" {
		t.Fatalf("旧版备份 = %q, 想原运行中的内容", got)
	}
	if fx.process.quitExisting != 1 {
		t.Fatalf("通知旧实例退出次数 = %d, 想 1", fx.process.quitExisting)
	}

	fx.files.executable = fx.files.installPath
	fx.files.occupiedTarget = false
	if _, err := client.InstallIfNeeded(fx.deps(), "https://taskboard.example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fx.files.installPath + ".old"); !os.IsNotExist(err) {
		t.Fatalf("下次启动应尽力删除 .old：%v", err)
	}
}

func Test覆盖安装通过文件适配器识别已存在目标(t *testing.T) {
	fx := newInstallFixture(t)
	fx.files.executable = fx.download
	fx.files.targetExists = true

	installed, err := client.InstallIfNeeded(fx.deps(), "https://taskboard.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Fatal("覆盖安装后仍应从安装位置重启")
	}
	if !fx.files.targetExistsChecked {
		t.Fatal("目标是否存在必须经过 FileSystem 适配层判断")
	}
	if len(fx.files.renames) != 1 || fx.files.renames[0].src != fx.files.installPath ||
		fx.files.renames[0].dest != fx.files.installPath+".old" {
		t.Fatalf("备份旧实例 = %+v", fx.files.renames)
	}
	if len(fx.files.copies) != 1 || fx.files.copies[0] != fx.files.installPath {
		t.Fatalf("复制新实例 = %v", fx.files.copies)
	}
	if fx.process.quitExisting != 1 {
		t.Fatalf("通知旧实例退出次数 = %d, 想 1", fx.process.quitExisting)
	}
	if len(fx.process.started) != 1 || len(fx.process.started[0].args) != 1 ||
		fx.process.started[0].args[0] != "--after-install" {
		t.Fatalf("重启参数 = %+v, 想 --after-install", fx.process.started)
	}
}

func Test托盘卸载撤销凭证清理本机并请求删除自身(t *testing.T) {
	fake := newFakeDesktop(t)
	s := fake.startService(t)
	webLi := s.Login("李工", "secret-pass")
	fake.browser.web = webLi
	fake.browser.serverURL = s.URL()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fake.startClient(t, ctx, s.URL())
	fake.waitState(t, client.TrayNormal)
	fx := newInstallFixture(t)
	fx.files.executable = fx.files.installPath
	fake.lifecycle = fx.deps()

	fake.tray.click(t, "uninstall")
	waitClient(t, done)
	if clients := listClients(t, s, webLi); len(clients) != 0 {
		t.Fatalf("卸载后服务端电脑数 = %d", len(clients))
	}
	if fake.store.current(t) != "" || fx.config.savedURL != "" || fx.autostart.enabled {
		t.Fatal("卸载后应清理本机凭据、平台地址和自启项")
	}
	if !fx.toast.unregistered || len(fx.process.deletedLater) != 1 || fx.process.deletedLater[0] != fx.files.installPath {
		t.Fatalf("AUMID/延迟删除 = %v/%+v", fx.toast.unregistered, fx.process.deletedLater)
	}
}

type installFixture struct {
	download     string
	files        *fakeFileSystem
	config       *fakeServerConfig
	autostart    *fakeAutostart
	toast        *fakeToastRegistration
	process      *fakeProcessManager
	events       *[]string
	installAlert *fakeInstallAlert
}

func newInstallFixture(t testing.TB) installFixture {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "programs", "taskboard-client")
	installPath := filepath.Join(installDir, "taskboard-client.exe")
	download := filepath.Join(dir, "Downloads", "taskboard-client.exe")
	for _, p := range []string{installPath, download} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(download, []byte("MZ downloaded"), 0o755); err != nil {
		t.Fatal(err)
	}
	events := &[]string{}
	return installFixture{
		download: download,
		files: &fakeFileSystem{download: download, installDir: installDir, installPath: installPath,
			executable: download},
		config: &fakeServerConfig{}, autostart: &fakeAutostart{enabled: true}, toast: &fakeToastRegistration{},
		process: &fakeProcessManager{events: events},
		events:  events, installAlert: &fakeInstallAlert{events: events},
	}
}

func (f installFixture) deps() client.LifecycleDependencies {
	return client.LifecycleDependencies{Config: f.config, Autostart: f.autostart, Toast: f.toast,
		Files: f.files, Process: f.process, InstallAlert: f.installAlert}
}

func readFile(t testing.TB, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type fakeServerConfig struct {
	savedURL string
}

type recordingPrompter struct {
	values []string
	errs   []error
}

func (p *recordingPrompter) PromptServerURL(_ string, probeErr error) (string, bool, error) {
	p.errs = append(p.errs, probeErr)
	value := p.values[0]
	p.values = p.values[1:]
	return value, true, nil
}

func (s *fakeServerConfig) ServerURL() (string, bool, error) {
	return s.savedURL, s.savedURL != "", nil
}
func (s *fakeServerConfig) SaveServerURL(rawURL string) error { s.savedURL = rawURL; return nil }
func (s *fakeServerConfig) DeleteServerURL() error            { s.savedURL = ""; return nil }

type fakeAutostart struct {
	enabled bool
	command string
}

type fakeToastRegistration struct{ unregistered bool }

func (a *fakeAutostart) Enable(command string) error {
	a.enabled, a.command = true, command
	return nil
}
func (a *fakeAutostart) Disable() error            { a.enabled = false; return nil }
func (t *fakeToastRegistration) Unregister() error { t.unregistered = true; return nil }

type fakeFileSystem struct {
	download            string
	installDir          string
	installPath         string
	executable          string
	copies              []string
	renames             []fakeRename
	occupiedTarget      bool
	targetExists        bool
	targetExistsChecked bool
}

type fakeProcessManager struct {
	started      []fakeStart
	deletedLater []string
	quitExisting int
	events       *[]string
}

type fakeInstallAlert struct{ events *[]string }

func (a fakeInstallAlert) NotifyFirstInstall() error {
	*a.events = append(*a.events, "dialog")
	return nil
}

func (f *fakeFileSystem) InstallDir() (string, error) { return f.installDir, nil }
func (f *fakeFileSystem) Executable() (string, error) { return f.executable, nil }

func (f *fakeFileSystem) Exists(path string) (bool, error) {
	f.targetExistsChecked = true
	if f.targetExists {
		return true, nil
	}
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (f *fakeFileSystem) CopyExecutable(dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if f.occupiedTarget {
		if _, err := os.Stat(dest); err == nil {
			return os.ErrPermission
		}
	}
	data, err := os.ReadFile(f.download)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dest, data, 0o755); err != nil {
		return err
	}
	f.copies = append(f.copies, dest)
	return nil
}

func (f *fakeFileSystem) RenameExecutable(src, dest string) error {
	f.renames = append(f.renames, fakeRename{src: src, dest: dest})
	if f.targetExists {
		return nil
	}
	if err := os.Rename(src, dest); err != nil {
		return err
	}
	return nil
}

func (f *fakeFileSystem) RemoveExecutable(path string) error { return os.Remove(path) }

func (p *fakeProcessManager) Start(path string, args []string) error {
	p.started = append(p.started, fakeStart{path: path, args: args})
	*p.events = append(*p.events, "start")
	return nil
}
func (p *fakeProcessManager) DeleteLater(path string) error {
	p.deletedLater = append(p.deletedLater, path)
	return nil
}

func (p *fakeProcessManager) QuitExisting() error {
	p.quitExisting++
	return nil
}

type fakeStart struct {
	path string
	args []string
}

type fakeRename struct {
	src  string
	dest string
}
