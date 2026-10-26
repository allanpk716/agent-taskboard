// 更新源（#45）：GitHub 固定地址源与 UpdateHub 源。测试用本地 HTTP 服务
// 伪装两个源（不碰真实 GitHub 和 UpdateHub），验证镜像前缀回退、按
// [update].sources 顺序回退，以及“记录实际用了哪个源”。
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/release"
	"github.com/allanpk716/agent-taskboard/internal/updatehub"
)

// sha256Hex 是测试侧自己算的摘要（不经过被测代码）。
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fileEntries(files map[string][]byte) []release.FileEntry {
	entries := make([]release.FileEntry, 0, len(files))
	for name, content := range files {
		entries = append(entries, release.FileEntry{
			Name: name, Sha256: sha256Hex(content), Size: int64(len(content)),
		})
	}
	return entries
}

func testManifest(component, ver, minServer string, files map[string][]byte) release.Manifest {
	return release.Manifest{
		Component: component, Version: ver, MinServer: minServer, Files: fileEntries(files),
	}
}

// fakeGitHub 伪装 GitHub Release 的固定下载地址（不调用 api.github.com）。
// 请求 URL 形如 {base}/m/https://github.com/example/example/releases/download/...，
// 处理时按 “releases/download/ 后面的部分” 匹配。
type fakeGitHub struct {
	t        *testing.T
	server   *httptest.Server
	requests []string
}

func newFakeGitHub(t *testing.T, latest release.Latest, bySuffix map[string][]byte) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{t: t}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.URL.Path)
		// 固定下载地址在 releases/download/<tag>/<file>；取这一段做键。
		idx := strings.Index(r.URL.Path, "releases/download/")
		if idx < 0 {
			http.NotFound(w, r)
			return
		}
		key := r.URL.Path[idx:]
		if key == "releases/download/channel-stable/latest.json" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(latest)
			return
		}
		if content, ok := bySuffix[key]; ok {
			w.Write(content)
			return
		}
		http.NotFound(w, r)
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

// githubFixture 一次造出 latest.json、两份 manifest 和全部产物。
type githubFixture struct {
	latest      release.Latest
	bySuffix    map[string][]byte
	serverFiles map[string][]byte
}

func newGitHubFixture(serverVer, webVer, webMinServer string) *githubFixture {
	fx := &githubFixture{
		serverFiles: map[string][]byte{
			"taskboard.exe":         []byte("fake windows exe " + serverVer),
			"taskboard-linux-amd64": []byte("fake linux exe " + serverVer),
		},
	}
	webFiles := map[string][]byte{
		"taskboard-web-" + webVer + ".zip": []byte("fake web zip " + webVer),
	}
	fx.bySuffix = map[string][]byte{}
	fx.latest = release.Latest{
		Server: &release.LatestEntry{
			Version: serverVer, Tag: "server/v" + serverVer,
			ManifestURL: "https://github.com/example/example/releases/download/server/v" + serverVer + "/manifest.json",
		},
		Web: &release.LatestEntry{
			Version: webVer, Tag: "web/v" + webVer,
			ManifestURL: "https://github.com/example/example/releases/download/web/v" + webVer + "/manifest.json",
		},
	}
	for component, m := range map[string]release.Manifest{
		"server": testManifest("server", serverVer, serverVer, fx.serverFiles),
		"web":    testManifest("web", webVer, webMinServer, webFiles),
	} {
		data, _ := json.Marshal(m)
		fx.bySuffix["releases/download/"+component+"/v"+m.Version+"/manifest.json"] = data
	}
	for name, content := range fx.serverFiles {
		fx.bySuffix["releases/download/server/v"+serverVer+"/"+name] = content
	}
	for name, content := range webFiles {
		fx.bySuffix["releases/download/web/v"+webVer+"/"+name] = content
	}
	return fx
}

// GitHub 源：通过镜像前缀取 latest.json 与 manifest，产物按 manifest 的 SHA256 校验。
func TestGitHub源经镜像取得最新版本并校验产物(t *testing.T) {
	fx := newGitHubFixture("1.2.3", "1.4.0", "1.0.0")
	fake := newFakeGitHub(t, fx.latest, fx.bySuffix)

	src := &GitHubSource{Repo: "example/example", Mirror: []string{fake.server.URL + "/m/"}}
	latest, err := src.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if latest.Server == nil || latest.Server.Manifest.Version != "1.2.3" {
		t.Fatalf("server 最新版本 = %+v, 想 1.2.3", latest.Server)
	}
	if latest.Web == nil || latest.Web.Manifest.MinServer != "1.0.0" {
		t.Fatalf("web min_server = %+v, 想 1.0.0", latest.Web)
	}
	got, err := latest.Server.File(context.Background(), "taskboard.exe")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "fake windows exe 1.2.3" {
		t.Fatalf("产物内容不对：%q", got)
	}
	// 只用 releases/download/ 下的固定地址（不出现 api.github.com）。
	for _, p := range fake.requests {
		if !strings.Contains(p, "releases/download/") {
			t.Errorf("请求了固定地址之外的路径：%s", p)
		}
	}
}

func TestGitHub源忽略客户端组件(t *testing.T) {
	fx := newGitHubFixture("1.0.0", "1.0.0", "1.0.0")
	files := map[string][]byte{"taskboard-client.exe": []byte("client exe")}
	m := testManifest("client", "1.0.0", "", files)
	data, _ := json.Marshal(m)
	fx.bySuffix["releases/download/client/v1.0.0/manifest.json"] = data
	fx.bySuffix["releases/download/client/v1.0.0/taskboard-client.exe"] = files["taskboard-client.exe"]
	fx.latest.Client = &release.LatestEntry{
		Version: "1.0.0", Tag: "client/v1.0.0",
		ManifestURL: "https://github.com/example/example/releases/download/client/v1.0.0/manifest.json",
	}
	fake := newFakeGitHub(t, fx.latest, fx.bySuffix)

	src := &GitHubSource{Repo: "example/example", Mirror: []string{fake.server.URL + "/m/"}}
	latest, err := src.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if latest.Server == nil || latest.Web == nil {
		t.Fatalf("应解析平台组件：%+v", latest)
	}
}

func Test客户端尚未发布不影响其余组件(t *testing.T) {
	fx := newGitHubFixture("1.0.0", "1.0.0", "1.0.0")
	fake := newFakeGitHub(t, fx.latest, fx.bySuffix)

	src := &GitHubSource{Repo: "example/example", Mirror: []string{fake.server.URL + "/m/"}}
	latest, err := src.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if latest.Server == nil || latest.Web == nil {
		t.Fatalf("client 未发布不应导致整体失败：%+v", latest)
	}
}

// 镜像前缀按顺序尝试：第一个连不上时用第二个。
func TestGitHub源镜像按顺序回退(t *testing.T) {
	fx := newGitHubFixture("1.0.0", "1.1.0", "1.0.0")
	fake := newFakeGitHub(t, fx.latest, fx.bySuffix)

	dead := "http://127.0.0.1:1/"
	src := &GitHubSource{Repo: "example/example", Mirror: []string{dead, fake.server.URL + "/m/"}}
	latest, err := src.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if latest.Server.Manifest.Version != "1.0.0" {
		t.Fatalf("应经第二个镜像取到 1.0.0")
	}
}

// UpdateHub 源：feed 里的 SHA1 校验 nupkg，解包后按 manifest 的 SHA256 校验产物。
func TestUpdateHub源按SHA1与SHA256双重校验(t *testing.T) {
	hub := newFakeUpdateHub(t)
	serverFiles := map[string][]byte{"taskboard.exe": []byte("hub windows exe 2.0.0")}
	hub.publish(t, "server", "2.0.0", testManifest("server", "2.0.0", "2.0.0", serverFiles), serverFiles)

	src := &UpdateHubSource{BaseURL: hub.base, Channel: "stable"}
	latest, err := src.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if latest.Server == nil || latest.Server.Manifest.Version != "2.0.0" {
		t.Fatalf("server 最新版本 = %+v, 想 2.0.0", latest.Server)
	}
	got, err := latest.Server.File(context.Background(), "taskboard.exe")
	if err != nil || string(got) != "hub windows exe 2.0.0" {
		t.Fatalf("产物内容 = %q, err %v", got, err)
	}
	if latest.Web != nil {
		t.Fatalf("源上没有 web 组件时应为 nil：%+v", latest.Web)
	}
}

// fakeUpdateHub 伪装 UpdateHub 的公开查询接口：feed 与 nupkg 下载。
type fakeUpdateHub struct {
	t    *testing.T
	base string
	mux  *http.ServeMux
}

func newFakeUpdateHub(t *testing.T) *fakeUpdateHub {
	t.Helper()
	h := &fakeUpdateHub{t: t, mux: http.NewServeMux()}
	server := httptest.NewServer(h.mux)
	t.Cleanup(server.Close)
	h.base = server.URL
	return h
}

// publish 把一个组件的 manifest 和产物打成 nupkg（复用 #44 的打包代码），
// 生成 feed 并挂到 {base}/{appId}/{channel}/ 下。
func (h *fakeUpdateHub) publish(t *testing.T, component, ver string, manifest release.Manifest, files map[string][]byte) {
	t.Helper()
	appID, err := updatehub.AppIDForComponent(component)
	if err != nil {
		t.Fatal(err)
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	payloads := make([]release.PayloadFile, 0, len(files))
	for name, content := range files {
		payloads = append(payloads, release.PayloadFile{Name: name, Content: content})
	}
	nupkg, err := release.PackNupkg(release.PackInput{
		AppID: appID, Version: ver, Manifest: manifestJSON, Files: payloads,
	})
	if err != nil {
		t.Fatal(err)
	}
	feed, err := release.ReleaseFeedJSON(appID, ver, nupkg)
	if err != nil {
		t.Fatal(err)
	}
	channel := "stable"
	h.mux.HandleFunc("/"+appID+"/"+channel+"/releases.win.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write(feed)
	})
	h.mux.HandleFunc("/"+appID+"/"+channel+"/"+release.NupkgFileName(appID, ver), func(w http.ResponseWriter, r *http.Request) {
		w.Write(nupkg)
	})
}

// 按 [update].sources 顺序回退：第一个源连不上时用第二个，并记录实际用的源。
func Test更新源按顺序回退并记录实际源(t *testing.T) {
	fx := newGitHubFixture("1.0.0", "1.1.0", "1.0.0")
	fake := newFakeGitHub(t, fx.latest, fx.bySuffix)

	cfg := config.Update{
		Sources:   []string{"updatehub", "github"},
		UpdateHub: config.UpdateHub{BaseURL: "http://127.0.0.1:1/", Channel: "stable"},
		GitHub:    config.UpdateGitHub{Repo: "example/example", Mirror: []string{fake.server.URL + "/m/"}},
	}
	latest, err := resolveLatest(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Source != "github" {
		t.Fatalf("实际使用的源 = %q, 想 github（updatehub 连不上应回退）", latest.Source)
	}
	if latest.Server.Manifest.Version != "1.0.0" {
		t.Fatalf("server 最新版本 = %q, 想 1.0.0", latest.Server.Manifest.Version)
	}
}

// 没有配置任何更新源时明确报错，而不是去连默认地址。
func Test没有配置更新源时报错(t *testing.T) {
	_, err := resolveLatest(context.Background(), config.Update{}, nil)
	if err == nil {
		t.Fatal("应报错：没有配置更新源")
	}
}

// 所有源都连不上时报错，并把每个源的失败原因带上。
func Test所有源都连不上时报错带原因(t *testing.T) {
	cfg := config.Update{
		Sources:   []string{"updatehub", "github"},
		UpdateHub: config.UpdateHub{BaseURL: "http://127.0.0.1:1/", Channel: "stable"},
		GitHub:    config.UpdateGitHub{Repo: "example/example", Mirror: []string{"http://127.0.0.1:1/m/"}},
	}
	_, err := resolveLatest(context.Background(), cfg, nil)
	if err == nil {
		t.Fatal("应报错：所有源都连不上")
	}
	for _, want := range []string{"updatehub", "github"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息应包含源 %q 的失败原因：%v", want, err)
		}
	}
}

// UpdateHub 源的两道校验各自拒绝对应被篡改的包：feed 里的 SHA1 不符拒绝；
// nupkg 本身对的但里面的产物被改过，解包后按 manifest 的 SHA256 拒绝。
func TestUpdateHub源被篡改时拒绝(t *testing.T) {
	serverFiles := map[string][]byte{"taskboard.exe": []byte("hub windows exe 2.0.0")}
	manifest := testManifest("server", "2.0.0", "2.0.0", serverFiles)

	// nupkg 本身被改（feed 的 SHA1 对不上）。
	badSHA1 := newFakeUpdateHub(t)
	badSHA1.publishTampered(t, "server", "2.0.0", manifest, serverFiles, func(nupkg []byte) []byte {
		return append(nupkg, 'x')
	}, func(feed []byte) []byte { return feed })
	src := &UpdateHubSource{BaseURL: badSHA1.base, Channel: "stable"}
	if _, err := src.Latest(context.Background()); err == nil {
		t.Error("SHA1 不符应拒绝")
	} else if !strings.Contains(err.Error(), "SHA1") {
		t.Errorf("错误信息应提到 SHA1：%v", err)
	}

	// nupkg 对，但打包时产物被换掉（SHA1 对，manifest 的 SHA256 对不上）。
	badPayload := newFakeUpdateHub(t)
	tampered := map[string][]byte{"taskboard.exe": []byte("被换掉的 exe")}
	badPayload.publishTampered(t, "server", "2.0.0", manifest, tampered,
		func(nupkg []byte) []byte { return nupkg }, func(feed []byte) []byte { return feed })
	src2 := &UpdateHubSource{BaseURL: badPayload.base, Channel: "stable"}
	latest, err := src2.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := latest.Server.File(context.Background(), "taskboard.exe"); err == nil {
		t.Error("manifest SHA256 不符应拒绝")
	} else if !strings.Contains(err.Error(), "SHA256") {
		t.Errorf("错误信息应提到 SHA256：%v", err)
	}
}

// publishTampered 打包一个可被篡改的版本（复用 publish 的流程，但允许替换
// nupkg 或 feed 的内容）。
func (h *fakeUpdateHub) publishTampered(t *testing.T, component, ver string, manifest release.Manifest,
	files map[string][]byte, tweakNupkg func([]byte) []byte, tweakFeed func([]byte) []byte) {
	t.Helper()
	appID, err := updatehub.AppIDForComponent(component)
	if err != nil {
		t.Fatal(err)
	}
	manifestJSON, _ := json.Marshal(manifest)
	payloads := make([]release.PayloadFile, 0, len(files))
	for name, content := range files {
		payloads = append(payloads, release.PayloadFile{Name: name, Content: content})
	}
	nupkg, err := release.PackNupkg(release.PackInput{
		AppID: appID, Version: ver, Manifest: manifestJSON, Files: payloads,
	})
	if err != nil {
		t.Fatal(err)
	}
	// feed 记录的是原始 nupkg 的 SHA1；对 nupkg 的篡改要在这之后。
	feed, err := release.ReleaseFeedJSON(appID, ver, nupkg)
	if err != nil {
		t.Fatal(err)
	}
	nupkg = tweakNupkg(nupkg)
	feed = tweakFeed(feed)
	h.mux.HandleFunc("/"+appID+"/stable/releases.win.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write(feed)
	})
	h.mux.HandleFunc("/"+appID+"/stable/"+release.NupkgFileName(appID, ver), func(w http.ResponseWriter, r *http.Request) {
		w.Write(nupkg)
	})
}

// GitHub 源的代理：配置了 [update.github].proxy 时请求经代理转发。
func TestGitHub源走配置的代理(t *testing.T) {
	fx := newGitHubFixture("1.0.0", "1.1.0", "1.0.0")
	fake := newFakeGitHub(t, fx.latest, fx.bySuffix)

	// 代理：原样转发，记录经过它的请求。
	var proxied []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied = append(proxied, r.URL.String())
		if r.URL.Host == "" { // 绝对形式才是经代理的请求
			http.Error(w, "proxy needs absolute-form URL", http.StatusBadRequest)
			return
		}
		proxyClient := &http.Client{}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, r.URL.String(), nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		res, err := proxyClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer res.Body.Close()
		w.WriteHeader(res.StatusCode)
		io.Copy(w, res.Body)
	}))
	t.Cleanup(proxy.Close)

	src := &GitHubSource{Repo: "example/example", Mirror: []string{fake.server.URL + "/m/"}, Proxy: proxy.URL}
	latest, err := src.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if latest.Server.Manifest.Version != "1.0.0" {
		t.Fatalf("经代理应取到最新版本")
	}
	if len(proxied) == 0 {
		t.Fatal("请求应经过配置的代理")
	}
}
