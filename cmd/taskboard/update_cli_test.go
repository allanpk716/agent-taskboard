// taskboard update / rollback 的 CLI 测试（#45）：本地 HTTP 服务伪装 GitHub
// 源和 UpdateHub 源（不碰真实网络），更新操作针对临时安装目录，绝不替换
// 测试进程自己的 exe。
package main_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/release"
)

// updateFixture 是一次更新测试的安装环境：安装目录（exe + web）、data_dir、
// 指向假源的 config.toml。exePath 是“正在运行的 exe”的替身（临时文件）。
type updateFixture struct {
	t          *testing.T
	bin        string
	dir        string
	install    string
	exePath    string
	dataDir    string
	webDir     string
	configPath string
	port       int
}

func startUpdateFixture(t *testing.T) *updateFixture {
	t.Helper()
	bin := buildBinary(t)
	dir := t.TempDir()
	f := &updateFixture{
		t: t, bin: bin, dir: dir,
		install:  filepath.Join(dir, "install"),
		dataDir:  filepath.Join(dir, "data"),
		webDir:   filepath.Join(dir, "install", "web"),
	}
	if err := os.MkdirAll(f.install, 0o755); err != nil {
		t.Fatal(err)
	}
	f.exePath = filepath.Join(f.install, exeName())
	// “正在运行的 exe”：随便一份内容，只比对字节，绝不动真实测试二进制。
	fakeExe := []byte("old taskboard exe 0.0.0 build")
	if err := os.WriteFile(f.exePath, fakeExe, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.webDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.webDir, "index.html"), []byte("<!doctype html><title>web 0.0.0</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWebVersion(t, f.webDir, "0.0.0")
	f.port = freePort(t)

	f.configPath = filepath.Join(dir, "config.toml")
	return f
}

// serve 启动服务直到返回的 stop 被调用（与 backup_cli_test 的 fixture 同一套做法）。
func (f *updateFixture) serve() (base string, stop func()) {
	f.t.Helper()
	serve := exec.Command(f.bin, "serve", "-config", f.configPath)
	serve.Stdout = os.Stdout
	serve.Stderr = os.Stderr
	if err := serve.Start(); err != nil {
		f.t.Fatal(err)
	}
	base = fmt.Sprintf("http://127.0.0.1:%d", f.port)
	waitReady(f.t, base)
	return base, func() {
		serve.Process.Kill()
		serve.Wait()
	}
}

func exeName() string {
	if os.IsPathSeparator('\\') {
		return "taskboard.exe"
	}
	return "taskboard"
}

// writeWebVersion 写前端目录的版本标记（更新器解压新版本时会写同名文件）。
func writeWebVersion(t *testing.T, webDir, ver string) {
	t.Helper()
	data := fmt.Sprintf(`{"component":"web","version":%q}`, ver) + "\n"
	if err := os.WriteFile(filepath.Join(webDir, "version.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeConfig 写 config.toml，update 小节指向 sources 配置的假源。
func (f *updateFixture) writeConfig(updateToml string) {
	f.t.Helper()
	toml := fmt.Sprintf("data_dir = '%s'\nport = %d\nweb_dir = '%s'\n%s",
		filepath.ToSlash(f.dataDir), f.port, filepath.ToSlash(f.webDir), updateToml)
	if err := os.WriteFile(f.configPath, []byte(toml), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// updateTomlGitHub 返回指向 fake GitHub 源的 [update] 小节。
func updateTomlGitHub(mirror string) string {
	return fmt.Sprintf(`
[update]
sources = ["github"]
[update.github]
repo = "example/example"
mirror = ["%s"]
`, strings.TrimRight(mirror, "/")+"/")
}

// updateTomlFallback 返回 updatehub 在前（不可用）、github 在后的源列表。
func updateTomlFallback(mirror string) string {
	return fmt.Sprintf(`
[update]
sources = ["updatehub", "github"]
[update.updatehub]
base_url = "http://127.0.0.1:1/"
[update.github]
repo = "example/example"
mirror = ["%s"]
`, strings.TrimRight(mirror, "/")+"/")
}

// fakeUpdateSource 伪装 GitHub Release 的固定下载地址，服务 server/web 两个
// 组件的一个版本；可注入坏哈希模拟篡改。
type fakeUpdateSource struct {
	t      *testing.T
	server *httptest.Server
	// corrupt 记录要破坏哈希的产物名（先配好再启动请求）。
	corrupt map[string]bool
}

// sha256Hex 是测试侧自己算的摘要。
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// newFakeUpdateSource 发布 serverVer 的后端（按本机 GOOS 给 exe）和 webVer 的前端。
func newFakeUpdateSource(t *testing.T, serverVer, webVer, webMinServer string) (*fakeUpdateSource, map[string][]byte) {
	t.Helper()
	files := map[string][]byte{
		"taskboard.exe":         []byte("new windows exe " + serverVer),
		"taskboard-linux-amd64": []byte("new linux exe " + serverVer),
	}
	webZip := makeWebZip(t, webVer)
	files["taskboard-web-"+webVer+".zip"] = webZip

	src := &fakeUpdateSource{t: t, corrupt: map[string]bool{}}
	byPath := map[string][]byte{}
	manifests := map[string]release.Manifest{
		"server": {Component: "server", Version: serverVer, MinServer: serverVer, Files: fileEntriesOf(files)},
		"web":    {Component: "web", Version: webVer, MinServer: webMinServer, Files: fileEntriesOf(map[string][]byte{"taskboard-web-" + webVer + ".zip": webZip})},
	}
	latest := release.Latest{
		Server: &release.LatestEntry{Version: serverVer, Tag: "server/v" + serverVer,
			ManifestURL: "https://github.com/example/example/releases/download/server/v" + serverVer + "/manifest.json"},
		Web: &release.LatestEntry{Version: webVer, Tag: "web/v" + webVer,
			ManifestURL: "https://github.com/example/example/releases/download/web/v" + webVer + "/manifest.json"},
	}
	latestJSON, _ := json.Marshal(latest)
	byPath["releases/download/channel-stable/latest.json"] = latestJSON
	for component, m := range manifests {
		data, _ := json.Marshal(m)
		byPath["releases/download/"+component+"/v"+m.Version+"/manifest.json"] = data
	}
	for _, m := range manifests {
		for _, fe := range m.Files {
			byPath["releases/download/"+m.Component+"/v"+m.Version+"/"+fe.Name] = files[fe.Name]
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		idx := strings.Index(r.URL.Path, "releases/download/")
		if idx < 0 {
			http.NotFound(w, r)
			return
		}
		key := r.URL.Path[idx:]
		content, ok := byPath[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		// 破坏指定产物的哈希（内容加一个字节）。
		name := key[strings.LastIndex(key, "/")+1:]
		if src.corrupt[name] {
			content = append(content, '!')
		}
		w.Write(content)
	})
	src.server = httptest.NewServer(mux)
	t.Cleanup(src.server.Close)
	return src, files
}

func fileEntriesOf(files map[string][]byte) []release.FileEntry {
	entries := make([]release.FileEntry, 0, len(files))
	for name, content := range files {
		entries = append(entries, release.FileEntry{Name: name, Sha256: sha256Hex(content), Size: int64(len(content))})
	}
	return entries
}

// makeWebZip 造一个前端发布 zip：index.html + assets/app-<ver>.js。
func makeWebZip(t *testing.T, ver string) []byte {
	t.Helper()
	return zipBytes(t, map[string]string{
		"index.html":    fmt.Sprintf("<!doctype html><title>web %s</title>", ver),
		"assets/app.js": fmt.Sprintf("console.log('%s')", ver),
	})
}

func zipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// runUpdate 运行 taskboard update，返回合并输出与错误。
func runUpdate(f *updateFixture, extra ...string) (string, error) {
	f.t.Helper()
	args := append([]string{"update", "-config", f.configPath, "-exe", f.exePath}, extra...)
	cmd := exec.Command(f.bin, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// taskboard update -check：第一个源不可用时回退到第二个，并记录实际用的源。
func Test命令行更新检查回退到第二个源并记录实际源(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过：需要编译真实二进制")
	}
	f := startUpdateFixture(t)
	src, _ := newFakeUpdateSource(t, "1.2.3", "1.4.0", "1.0.0")
	f.writeConfig(updateTomlFallback(src.server.URL))

	out, err := runUpdate(f, "-check")
	if err != nil {
		t.Fatalf("update -check 失败：%v\n%s", err, out)
	}
	for _, want := range []string{"github", "1.2.3", "1.4.0", "0.0.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("update -check 输出应包含 %q：%s", want, out)
		}
	}
	if !strings.Contains(out, "实际使用源") && !strings.Contains(out, "实际用的源") {
		t.Errorf("输出应说明实际使用的源：%s", out)
	}
}

// taskboard update -check：所有源不可用时退出码非 0，错误说明每个源的情况。
func Test命令行更新检查所有源不可用时失败(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过：需要编译真实二进制")
	}
	f := startUpdateFixture(t)
	f.writeConfig(`
[update]
sources = ["github"]
[update.github]
repo = "example/example"
mirror = ["http://127.0.0.1:1/m/"]
`)
	out, err := runUpdate(f, "-check")
	if err == nil {
		t.Fatalf("所有源不可用时应失败：%s", out)
	}
	if !strings.Contains(out, "更新源") {
		t.Errorf("错误信息应提到更新源：%s", out)
	}
}

// readExe 读回 exe 的字节（断言没被换掉时用）。
func readExe(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// 哈希不符时拒绝更新：exe 与前端都保持原样。
func Test命令行更新哈希不符时拒绝(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过：需要编译真实二进制")
	}
	f := startUpdateFixture(t)
	src, _ := newFakeUpdateSource(t, "1.2.3", "1.4.0", "1.0.0")
	f.writeConfig(updateTomlGitHub(src.server.URL))

	// 篡改当前平台实际会下载的后端产物（Linux 上取的是 taskboard-linux-amd64）。
	serverArtifact := "taskboard.exe"
	if runtime.GOOS != "windows" {
		serverArtifact = "taskboard-linux-amd64"
	}
	for _, corrupt := range []string{serverArtifact, "taskboard-web-1.4.0.zip"} {
		src.corrupt[corrupt] = true
		out, err := runUpdate(f)
		if err == nil {
			t.Fatalf("产物 %s 哈希不符时应拒绝更新：%s", corrupt, out)
		}
		if !strings.Contains(out, "SHA256") {
			t.Errorf("错误信息应提到 SHA256 不符：%s", out)
		}
		if got := readExe(t, f.exePath); got != "old taskboard exe 0.0.0 build" {
			t.Errorf("哈希不符时不应替换 exe，得到 %q", got)
		}
		if _, err := os.Stat(f.exePath + ".prev"); !os.IsNotExist(err) {
			t.Errorf("哈希不符时不应产生 .prev")
		}
		// 前端也没被切换。
		if ver := webVersionOf(t, f.webDir); ver != "0.0.0" {
			t.Errorf("前端版本应保持 0.0.0，得到 %s", ver)
		}
		src.corrupt[corrupt] = false
	}
}

// webVersionOf 读前端目录的版本标记（绕过被测代码，直接看磁盘）。
func webVersionOf(t *testing.T, webDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(webDir, "version.json"))
	if err != nil {
		return ""
	}
	var v struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &v) != nil {
		return ""
	}
	return v.Version
}

// 更新前备份失败时中止，不替换 exe。
func Test命令行更新备份失败时中止不换exe(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过：需要编译真实二进制")
	}
	f := startUpdateFixture(t)
	src, _ := newFakeUpdateSource(t, "1.2.3", "1.4.0", "1.0.0")
	// 备份目标指向一个文件下面的子路径：MkdirAll 必然失败（连不上目标）。
	blocker := filepath.Join(f.dir, "blocker")
	if err := os.WriteFile(blocker, []byte("文件"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.writeConfig(updateTomlGitHub(src.server.URL) + fmt.Sprintf("\n[backup]\ntarget = '%s'\n", filepath.ToSlash(filepath.Join(blocker, "nas"))))

	out, err := runUpdate(f)
	if err == nil {
		t.Fatalf("备份失败时应中止：%s", out)
	}
	if !strings.Contains(out, "更新前备份") {
		t.Errorf("错误信息应说明是更新前备份失败：%s", out)
	}
	if got := readExe(t, f.exePath); got != "old taskboard exe 0.0.0 build" {
		t.Errorf("备份失败时不应替换 exe，得到 %q", got)
	}
	if _, err := os.Stat(f.exePath + ".prev"); !os.IsNotExist(err) {
		t.Errorf("备份失败时不应产生 .prev")
	}
}

// 一次完整的更新：exe 换成新版（旧版留在 .prev），前端解压并切换到新版本目录。
func Test命令行更新替换exe并切换前端(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过：需要编译真实二进制")
	}
	f := startUpdateFixture(t)
	src, files := newFakeUpdateSource(t, "1.2.3", "1.4.0", "0.0.0")
	f.writeConfig(updateTomlGitHub(src.server.URL))

	out, err := runUpdate(f)
	if err != nil {
		t.Fatalf("更新失败：%v\n%s", err, out)
	}
	wantExe := string(files["taskboard.exe"])
	if runtime.GOOS != "windows" {
		wantExe = string(files["taskboard-linux-amd64"])
	}
	if got := readExe(t, f.exePath); got != wantExe {
		t.Errorf("exe 应换成新版本内容")
	}
	if got := readExe(t, f.exePath+".prev"); got != "old taskboard exe 0.0.0 build" {
		t.Errorf(".prev 应是旧版 exe，得到 %q", got)
	}
	// 前端切到 1.4.0：web/ 指向 web-1.4.0/，版本标记更新。
	if ver := webVersionOf(t, f.webDir); ver != "1.4.0" {
		t.Fatalf("前端版本 = %s, 想 1.4.0", ver)
	}
	index, err := os.ReadFile(filepath.Join(f.webDir, "index.html"))
	if err != nil || !strings.Contains(string(index), "web 1.4.0") {
		t.Errorf("web/index.html 应是新版本：%q (%v)", index, err)
	}
	// 更新前备份也做了（后端更新必配一份）。
	matches, _ := filepath.Glob(filepath.Join(f.dataDir, "backups", "taskboard-*-preupdate.db"))
	if len(matches) != 1 {
		t.Errorf("应有一份更新前备份：%v", matches)
	}
	if !strings.Contains(out, "请重启服务") {
		t.Errorf("后端更新后应提示重启：%s", out)
	}
}

// min_server 不满足时先升级后端：步骤顺序是「后端」在「前端」之前。
func Test命令行更新min_server不满足时先升级后端(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过：需要编译真实二进制")
	}
	f := startUpdateFixture(t)
	src, _ := newFakeUpdateSource(t, "1.2.3", "1.4.0", "1.0.0") // 当前后端 0.0.0 < 1.0.0
	f.writeConfig(updateTomlGitHub(src.server.URL))

	out, err := runUpdate(f)
	if err != nil {
		t.Fatalf("更新失败：%v\n%s", err, out)
	}
	iServer := strings.Index(out, "后端")
	iWeb := strings.Index(out, "前端")
	if iServer < 0 || iWeb < 0 || iServer > iWeb {
		t.Fatalf("应先升级后端再更新前端（输出顺序）：%s", out)
	}
	if got := readExe(t, f.exePath); got == "old taskboard exe 0.0.0 build" {
		t.Fatal("后端应已被替换")
	}
	if ver := webVersionOf(t, f.webDir); ver != "1.4.0" {
		t.Fatalf("前端版本 = %s, 想 1.4.0", ver)
	}
}

// 前端 min_server 比最新后端还高时拒绝更新前端（先发布满足要求的后端）。
func Test命令行更新前端要求的后端不存在时拒绝(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过：需要编译真实二进制")
	}
	f := startUpdateFixture(t)
	src, _ := newFakeUpdateSource(t, "1.0.0", "1.4.0", "9.9.9") // 最新后端 1.0.0 < min_server 9.9.9
	f.writeConfig(updateTomlGitHub(src.server.URL))

	out, err := runUpdate(f)
	if err == nil {
		t.Fatalf("应拒绝更新：%s", out)
	}
	if !strings.Contains(out, "9.9.9") {
		t.Errorf("错误信息应说明需要的最低后端版本：%s", out)
	}
	if got := readExe(t, f.exePath); got != "old taskboard exe 0.0.0 build" {
		t.Errorf("拒绝时不应替换 exe")
	}
}

// 已经是最新时什么也不做。
func Test命令行更新已是最新时不动作(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过：需要编译真实二进制")
	}
	f := startUpdateFixture(t)
	// 假源发布 0.0.0：与测试二进制的版本（编译时未注入 → 0.0.0）相同。
	src, _ := newFakeUpdateSource(t, "0.0.0", "0.0.0", "0.0.0")
	f.writeConfig(updateTomlGitHub(src.server.URL))

	out, err := runUpdate(f)
	if err != nil {
		t.Fatalf("已是最新应成功：%v\n%s", err, out)
	}
	if !strings.Contains(out, "已是最新") {
		t.Errorf("输出应说明已是最新：%s", out)
	}
	if got := readExe(t, f.exePath); got != "old taskboard exe 0.0.0 build" {
		t.Errorf("已是最新时不应替换 exe")
	}
}

// 回滚：换回 .prev 的 exe 和更新前的备份；更新后新产生的数据回滚掉。
// 服务运行中执行回滚被拒绝（对运行中的库做热替换不安全）。
func Test命令行回滚恢复版本与数据库(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过：需要编译真实二进制")
	}
	f := startUpdateFixture(t)

	src, _ := newFakeUpdateSource(t, "1.2.3", "1.4.0", "0.0.0")
	f.writeConfig(updateTomlGitHub(src.server.URL))

	// 更新前的数据：一张工单。
	runTaskboard(t, f.bin, "create-admin", "-config", f.configPath, "-username", "admin", "-password", "secret-pass")
	base, stop := f.serve()
	c := f.cookieClient(base)
	f.createTicket(c, base, "更新前的工单")

	// 执行更新：旧 exe 进 .prev，做一份更新前备份，前端切到 1.4.0。
	if out, err := runUpdate(f); err != nil {
		t.Fatalf("更新失败：%v\n%s", err, out)
	}
	if got := readExe(t, f.exePath); got == "old taskboard exe 0.0.0 build" {
		t.Fatal("更新后 exe 应已替换")
	}

	// 更新后新版本运行期间又建了一张工单（回滚后应当不在）。
	f.createTicket(c, base, "更新后的工单")

	// 服务还在跑时回滚被拒绝。
	cmd := exec.Command(f.bin, "rollback", "-config", f.configPath, "-exe", f.exePath)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("服务运行中应拒绝回滚：%s", out)
	}
	stop()

	// 回滚：exe 和数据库都回到更新前。
	out := runTaskboard(t, f.bin, "rollback", "-config", f.configPath, "-exe", f.exePath)
	for _, want := range []string{"已换回上一版", "已恢复更新前备份", "回滚完成"} {
		if !strings.Contains(out, want) {
			t.Errorf("rollback 输出应包含 %q：%s", want, out)
		}
	}
	if got := readExe(t, f.exePath); got != "old taskboard exe 0.0.0 build" {
		t.Errorf("exe 应换回旧版，得到 %q", got)
	}
	if _, err := os.Stat(f.exePath + ".prev"); !os.IsNotExist(err) {
		t.Errorf("回滚后 .prev 应被用掉")
	}

	// 重启服务：更新前的工单在，更新后的工单没了。
	base, stop = f.serve()
	defer stop()
	c = f.cookieClient(base)
	for ref, want := range map[string]int{"1": 200, "2": 404} {
		res, err := c.Get(base + "/api/v1/tickets/" + ref)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("回滚后工单 %s 状态码 = %d, 想 %d", ref, res.StatusCode, want)
		}
	}
}

// cookieClient 登录管理员拿会话 cookie（沿用 backup_cli_test 的做法）。
func (f *updateFixture) cookieClient(base string) *http.Client {
	f.t.Helper()
	res, err := http.Post(base+"/api/v1/session", "application/json",
		strings.NewReader(`{"username":"admin","password":"secret-pass"}`))
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	for _, c := range res.Cookies() {
		if c.Name == "tb_session" && c.Value != "" {
			return &http.Client{Transport: fixedCookieTransport{c.Value}}
		}
	}
	f.t.Fatal("登录后没有会话 cookie")
	return nil
}

// createTicket 建一张工单（回滚测试里造数据库状态用）。
func (f *updateFixture) createTicket(c *http.Client, base, title string) string {
	f.t.Helper()
	body := `{"kind":"task","title":"` + title + `","brief_md":"说明","labels":["机型:UD-2300"]}`
	res, err := c.Post(base+"/api/v1/tickets", "application/json", strings.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		out, _ := io.ReadAll(res.Body)
		f.t.Fatalf("建单 %s 失败：%d %s", title, res.StatusCode, out)
	}
	var created struct {
		Number string `json:"number"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		f.t.Fatal(err)
	}
	return created.Number
}
