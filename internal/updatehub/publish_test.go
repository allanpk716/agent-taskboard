package updatehub

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/release"
)

// fakeGitHub 用固定下载地址的形式（/releases/download/{tag}/{file}）提供
// Release 产物，替身 github.com。可以按文件篡改内容，用来测 SHA256 校验。

type fakeGitHub struct {
	mu    sync.Mutex
	base  string
	files map[string][]byte // 相对路径，如 server/v1.2.3/manifest.json
	hits  int
}

func (g *fakeGitHub) put(path string, content []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.files[path] = content
}

func (g *fakeGitHub) tamper(path string, content []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.files[path] = content
}

func (g *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.hits++
	data, ok := g.files[strings.TrimPrefix(r.URL.Path, "/releases/download/")]
	g.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Write(data)
}

func startGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{files: map[string][]byte{}}
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	g.base = srv.URL + "/releases/download/"
	return g
}

// putRelease 在 fakeGitHub 上发布一个 server Release：manifest.json + 两个产物。
func putServerRelease(t *testing.T, g *fakeGitHub, tag, version string) []byte {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"taskboard.exe":         "windows-binary\n",
		"taskboard-linux-amd64": "linux-binary\n",
	}
	names := make([]string, 0, len(files))
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	m, err := release.BuildManifest("server", version, version, dir, names)
	if err != nil {
		t.Fatal(err)
	}
	manifestJSON, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	g.put(tag+"/manifest.json", manifestJSON)
	for name, content := range files {
		g.put(tag+"/"+name, []byte(content))
	}
	return manifestJSON
}

func baseOpts(g *fakeGitHub, hub *fakeHub, tag string) PublishOptions {
	hubURL, hubToken := "", ""
	if hub != nil {
		hubURL, hubToken = hub.baseURL, hub.token
	}
	return PublishOptions{
		DownloadBaseURL: g.base + tag + "/",
		Tag:             tag,
		Channel:         "stable",
		HubBaseURL:      hubURL,
		HubToken:        hubToken,
	}
}

func TestPublish下载校验打包并上传到UpdateHub(t *testing.T) {
	g := startGitHub(t)
	manifestJSON := putServerRelease(t, g, "server/v1.2.3", "1.2.3")
	hub := startHub(t, "secret-token")

	res, err := Publish(context.Background(), baseOpts(g, hub, "server/v1.2.3"))
	if err != nil {
		t.Fatalf("Publish：%v", err)
	}
	if res.AppID != "agent-taskboard-server" || res.Channel != "stable" || res.Version != "1.2.3" {
		t.Fatalf("发布结果不对：%+v", res)
	}
	if res.NupkgName != "agent-taskboard-server-1.2.3-full.nupkg" {
		t.Fatalf("nupkg 命名不对：%s", res.NupkgName)
	}

	// UpdateHub 侧：nupkg 入库，feed 被登记为该应用该通道的最新版本。
	stored := hub.file("agent-taskboard-server", "stable", res.NupkgName)
	if len(stored) == 0 {
		t.Fatal("UpdateHub 上没有收到 nupkg")
	}
	sum := sha1.Sum(stored)
	if res.NupkgSHA1 != hex.EncodeToString(sum[:]) || res.NupkgSize != int64(len(stored)) {
		t.Fatalf("发布结果里的 SHA1/Size 与 UpdateHub 收到的不符：%+v", res)
	}
	var feed release.ReleaseFeed
	if err := json.Unmarshal(hub.feeds["agent-taskboard-server/stable/releases.win.json"], &feed); err != nil {
		t.Fatalf("UpdateHub 侧 feed：%v", err)
	}
	if len(feed.Assets) != 1 || feed.Assets[0].PackageId != "agent-taskboard-server" ||
		feed.Assets[0].Version != "1.2.3" || feed.Assets[0].SHA1 != res.NupkgSHA1 {
		t.Fatalf("feed 条目不对：%+v", feed.Assets)
	}

	// nupkg 布局：nuspec 的 id 与 appId 一致，manifest.json 原样带入，产物在 payload/ 下。
	zr, err := zip.NewReader(bytes.NewReader(stored), int64(len(stored)))
	if err != nil {
		t.Fatalf("解包 nupkg：%v", err)
	}
	entries := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		entries[f.Name] = data
	}
	if !bytes.Equal(entries["manifest.json"], manifestJSON) {
		t.Fatalf("nupkg 里的 manifest.json 与 Release 上的不一致：%s", entries["manifest.json"])
	}
	if string(entries["payload/taskboard.exe"]) != "windows-binary\n" {
		t.Fatalf("payload/taskboard.exe 内容不对：%q", entries["payload/taskboard.exe"])
	}
	if !strings.Contains(string(entries["agent-taskboard-server.nuspec"]), "<id>agent-taskboard-server</id>") {
		t.Fatalf("nuspec 的 id 不对：%s", entries["agent-taskboard-server.nuspec"])
	}

	// OutDir 里留有 nupkg、feed 和 manifest，便于复核。
	if _, err := os.Stat(filepath.Join(res.OutDir, res.NupkgName)); err != nil {
		t.Fatalf("OutDir 里应有 nupkg：%v", err)
	}
}

func TestPublish在产物SHA256不符时中止(t *testing.T) {
	g := startGitHub(t)
	putServerRelease(t, g, "server/v1.2.3", "1.2.3")
	g.tamper("server/v1.2.3/taskboard.exe", []byte("tampered\n"))
	hub := startHub(t, "secret-token")

	if _, err := Publish(context.Background(), baseOpts(g, hub, "server/v1.2.3")); err == nil {
		t.Fatal("产物被篡改时 Publish 应失败")
	} else if !strings.Contains(err.Error(), "taskboard.exe") {
		t.Fatalf("报错应指出是哪个产物：%v", err)
	}
	if hub.uploads != 0 {
		t.Fatal("校验失败就不应上传")
	}
}

func TestPublish在tag与清单不符时中止(t *testing.T) {
	g := startGitHub(t)
	// Release 上放的是 web 的清单，tag 却写 server/。
	manifestJSON := []byte(`{"component":"web","version":"1.4.0","min_server":"1.2.0","files":[{"name":"taskboard-web-1.4.0.zip","sha256":"00","size":1}]}`)
	g.put("server/v1.2.3/manifest.json", manifestJSON)
	hub := startHub(t, "secret-token")

	if _, err := Publish(context.Background(), baseOpts(g, hub, "server/v1.2.3")); err == nil {
		t.Fatal("tag 与清单不一致时应失败")
	}
	if hub.uploads != 0 {
		t.Fatal("校验失败就不应上传")
	}
}

func TestPublishDryRun只下载打包不上传(t *testing.T) {
	g := startGitHub(t)
	putServerRelease(t, g, "server/v1.2.3", "1.2.3")

	opt := baseOpts(g, startHub(t, "secret-token"), "server/v1.2.3")
	opt.DryRun = true
	opt.HubBaseURL, opt.HubToken = "", "" // 不配置 UpdateHub 也可以演练
	res, err := Publish(context.Background(), opt)
	if err != nil {
		t.Fatalf("DryRun：%v", err)
	}
	nupkgPath := filepath.Join(res.OutDir, res.NupkgName)
	if _, err := os.Stat(nupkgPath); err != nil {
		t.Fatalf("DryRun 也应产出 nupkg：%v", err)
	}
	feedPath := filepath.Join(res.OutDir, "releases.win.json")
	if _, err := os.Stat(feedPath); err != nil {
		t.Fatalf("DryRun 也应产出 feed：%v", err)
	}
}
