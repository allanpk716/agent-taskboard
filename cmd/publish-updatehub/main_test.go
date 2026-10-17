package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

// publish-updatehub 在开发机上把 GitHub Release 的产物转发到内网
// UpdateHub。这里用本地的假 GitHub（固定下载地址）和假 UpdateHub
// （按其上传接口的校验逻辑收请求）端到端地走一遍命令行。

func buildPublishBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "publish-updatehub")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "github.com/allanpk716/agent-taskboard/cmd/publish-updatehub")
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("编译 publish-updatehub 失败：%v\n%s", err, out)
	}
	return bin
}

func projectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(filepath.Dir(dir)) // cmd/publish-updatehub/ -> 仓库根
}

// fakeReleaseServer 提供固定下载地址形式（/releases/download/{tag}/{file}）的 Release 产物。
type fakeReleaseServer struct {
	mu    sync.Mutex
	files map[string][]byte
	base  string
}

func (s *fakeReleaseServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	data, ok := s.files[strings.TrimPrefix(r.URL.Path, "/releases/download/")]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Write(data)
}

// fakeUpdateHub 只实现上传接口的校验：Bearer token、multipart、feed 的
// PackageId 与 appId 一致（不区分大小写）、按 .nupkg 文件名提取版本。
type fakeUpdateHub struct {
	mu      sync.Mutex
	token   string
	files   map[string][]byte
	feeds   map[string][]byte
	uploads int
}

func (h *fakeUpdateHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 与 UpdateHub 一致：GET 非 /api 路径（Velopack 静态/feed 下载）不做认证。
	if r.Method == http.MethodGet && !strings.HasPrefix(r.URL.Path, "/api/") {
		m := regexp.MustCompile(`^/([^/]+)/([^/]+)/releases\.([a-zA-Z0-9_]+)\.json$`).FindStringSubmatch(r.URL.Path)
		if m == nil {
			http.NotFound(w, r)
			return
		}
		h.mu.Lock()
		feed := h.feeds[m[1]+"/"+m[2]+"/releases."+m[3]+".json"]
		h.mu.Unlock()
		if feed == nil {
			feed = []byte(`{"Assets":[]}`)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(feed)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+h.token {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid token"})
		return
	}
	m := regexp.MustCompile(`^/api/apps/([^/]+)/channels/([^/]+)/releases$`).FindStringSubmatch(r.URL.Path)
	if m == nil || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	appID, channel := m[1], m[2]
	if err := r.ParseMultipartForm(200 << 20); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	versions := map[string]bool{}
	n := 0
	for _, fhs := range r.MultipartForm.File {
		for _, fh := range fhs {
			f, _ := fh.Open()
			data, _ := io.ReadAll(f)
			f.Close()
			h.mu.Lock()
			if strings.HasPrefix(fh.Filename, "releases.") && strings.HasSuffix(fh.Filename, ".json") {
				var feed struct {
					Assets []struct {
						PackageId string `json:"PackageId"`
						Version   string `json:"Version"`
					} `json:"Assets"`
				}
				if err := json.Unmarshal(data, &feed); err != nil {
					h.mu.Unlock()
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				for _, a := range feed.Assets {
					if a.PackageId != "" && !strings.EqualFold(a.PackageId, appID) {
						h.mu.Unlock()
						w.WriteHeader(http.StatusInternalServerError)
						json.NewEncoder(w).Encode(map[string]string{"error": "package ID mismatch"})
						return
					}
					if a.Version != "" {
						versions[a.Version] = true
					}
				}
				h.feeds[appID+"/"+channel+"/"+fh.Filename] = data
			} else {
				h.files[appID+"/"+channel+"/"+fh.Filename] = data
				if strings.HasSuffix(fh.Filename, ".nupkg") {
					parts := strings.Split(strings.TrimSuffix(fh.Filename, ".nupkg"), "-")
					if len(parts) >= 2 {
						versions[parts[len(parts)-2]] = true
					}
				}
			}
			h.mu.Unlock()
			n++
		}
	}
	h.mu.Lock()
	h.uploads++
	h.mu.Unlock()
	versionList := make([]string, 0, len(versions))
	for v := range versions {
		versionList = append(versionList, v)
	}
	sort.Strings(versionList)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"channel": channel, "files_received": n, "versions_added": versionList,
	})
}

func Test命令行把Release产物转发到UpdateHub(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过：需要编译真实二进制")
	}
	bin := buildPublishBinary(t)

	// 假 GitHub：一个 server Release。
	gh := &fakeReleaseServer{files: map[string][]byte{}}
	ghSrv := httptest.NewServer(gh)
	t.Cleanup(ghSrv.Close)
	gh.base = ghSrv.URL + "/releases/download/"
	exe := []byte("windows-binary\n")
	linux := []byte("linux-binary\n")
	manifest := `{
  "component": "server",
  "version": "1.2.3",
  "min_server": "1.2.3",
  "files": [
    {"name": "taskboard.exe", "sha256": "f9e537f375f9b8da666b1fe5addf9ec0a793cc841cc88e7549596a6615bfe6d0", "size": 15},
    {"name": "taskboard-linux-amd64", "sha256": "c8129af264901ca76e037945f033c1dc7b05f000e2eeec96d74ea0a69d9bfb61", "size": 13}
  ]
}`
	gh.mu.Lock()
	gh.files["server/v1.2.3/manifest.json"] = []byte(manifest)
	gh.files["server/v1.2.3/taskboard.exe"] = exe
	gh.files["server/v1.2.3/taskboard-linux-amd64"] = linux
	gh.mu.Unlock()

	// 假 UpdateHub。
	hub := &fakeUpdateHub{token: "the-token", files: map[string][]byte{}, feeds: map[string][]byte{}}
	hubSrv := httptest.NewServer(hub)
	t.Cleanup(hubSrv.Close)
	t.Setenv("UPDATEHUB_BASE_URL", hubSrv.URL)
	t.Setenv("UPDATEHUB_TOKEN", "the-token")

	out := filepath.Join(t.TempDir(), "out")
	cmd := exec.Command(bin,
		"-tag", "server/v1.2.3",
		"-download-base", gh.base+"server/v1.2.3/",
		"-out", out)
	stdout, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("publish-updatehub 失败：%v\n%s", err, stdout)
	}

	// UpdateHub 收到 nupkg，并登记成 feed。
	hub.mu.Lock()
	nupkg := hub.files["agent-taskboard-server/stable/agent-taskboard-server-1.2.3-full.nupkg"]
	feed := hub.feeds["agent-taskboard-server/stable/releases.win.json"]
	uploads := hub.uploads
	hub.mu.Unlock()
	if uploads != 1 {
		t.Fatalf("应恰好上传一次，实际 %d 次", uploads)
	}
	if len(nupkg) == 0 {
		t.Fatal("UpdateHub 没有收到 nupkg")
	}
	if !strings.Contains(string(feed), `"PackageId": "agent-taskboard-server"`) {
		t.Fatalf("feed 的 PackageId 应与 appId 一致：%s", feed)
	}
	// 开发机目录里留有可复核的 nupkg 和 feed。
	for _, name := range []string{"agent-taskboard-server-1.2.3-full.nupkg", "releases.win.json", "manifest.json", "taskboard.exe"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("输出目录应有 %s：%v", name, err)
		}
	}
	if !strings.Contains(string(stdout), "agent-taskboard-server") {
		t.Fatalf("输出应提到应用名：%s", stdout)
	}
}

func Test命令行缺少UpdateHub环境变量时报错(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过：需要编译真实二进制")
	}
	bin := buildPublishBinary(t)
	t.Setenv("UPDATEHUB_BASE_URL", "")
	t.Setenv("UPDATEHUB_TOKEN", "")
	out, err := exec.Command(bin, "-tag", "server/v1.2.3").CombinedOutput()
	if err == nil {
		t.Fatal("缺少环境变量应失败")
	}
	if !strings.Contains(string(out), "UPDATEHUB_BASE_URL") {
		t.Fatalf("报错应说明需要 UPDATEHUB_BASE_URL：%s", out)
	}
}

func Test命令行dryrun不需要UpdateHub配置(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过：需要编译真实二进制")
	}
	bin := buildPublishBinary(t)

	gh := &fakeReleaseServer{files: map[string][]byte{}}
	ghSrv := httptest.NewServer(gh)
	t.Cleanup(ghSrv.Close)
	gh.base = ghSrv.URL + "/releases/download/"
	gh.mu.Lock()
	gh.files["web/v1.4.0/manifest.json"] = []byte(`{"component":"web","version":"1.4.0","min_server":"1.2.0","files":[{"name":"taskboard-web-1.4.0.zip","sha256":"0263829989b6fd954f72baaf2fc64bc2e2f01d692d4de72986ea808f6e99813f","size":2}]}`)
	gh.files["web/v1.4.0/taskboard-web-1.4.0.zip"] = []byte("b\n")
	gh.mu.Unlock()
	t.Setenv("UPDATEHUB_BASE_URL", "")
	t.Setenv("UPDATEHUB_TOKEN", "")

	out := filepath.Join(t.TempDir(), "out")
	if stdout, err := exec.Command(bin, "-tag", "web/v1.4.0", "-download-base", gh.base+"web/v1.4.0/", "-out", out, "-dry-run").CombinedOutput(); err != nil {
		t.Fatalf("dry-run 不需要 UpdateHub 配置：%v\n%s", err, stdout)
	}
	if _, err := os.Stat(filepath.Join(out, "agent-taskboard-web-1.4.0-full.nupkg")); err != nil {
		t.Fatalf("dry-run 应产出 nupkg：%v", err)
	}
}
