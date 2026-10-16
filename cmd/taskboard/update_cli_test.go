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
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/release"
)

// updateFixture 是一次更新测试的安装环境：安装目录（exe + web）、data_dir、
// 指向假源的 config.toml。exePath 是“正在运行的 exe”的替身（临时文件）。
type updateFixture struct {
	t        *testing.T
	bin      string
	dir      string
	install  string
	exePath  string
	dataDir  string
	webDir   string
	configPath string
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
	writeWebVersion(t, f.webDir, "0.0.0")
	f.configPath = filepath.Join(dir, "config.toml")
	return f
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
	toml := fmt.Sprintf("data_dir = '%s'\nweb_dir = '%s'\n%s",
		filepath.ToSlash(f.dataDir), filepath.ToSlash(f.webDir), updateToml)
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
