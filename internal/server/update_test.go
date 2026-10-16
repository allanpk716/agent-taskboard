// 管理员更新接口（#45）：GET /api/v1/admin/update 检查更新（管理页提示有新
// 版本用），POST /api/v1/admin/update 执行更新。更新源指向本地假 HTTP 服务。
package server_test

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
	"path/filepath"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/release"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// updateStatusBody 是 GET /api/v1/admin/update 的响应。
type updateStatusBody struct {
	SourceUsed          string              `json:"source_used"`
	Server              componentStatusBody `json:"server"`
	Web                 componentStatusBody `json:"web"`
	WebNeedsNewerServer bool                `json:"web_needs_newer_server"`
}

type componentStatusBody struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
	MinServer       string `json:"min_server"`
}

// updateResultBody 是 POST /api/v1/admin/update 的响应。
type updateResultBody struct {
	SourceUsed      string   `json:"source_used"`
	Steps           []string `json:"steps"`
	ServerUpdated   bool     `json:"server_updated"`
	WebUpdated      bool     `json:"web_updated"`
	RestartNeeded   bool     `json:"restart_needed"`
	PreupdateBackup string   `json:"preupdate_backup"`
}

// adminUpdateFixture 造一个假 GitHub 更新源，并按它启动真实服务。
type adminUpdateFixture struct {
	server  *httptest.Server
	corrupt map[string]bool
	webDir  string
}

func newAdminUpdateFixture(t *testing.T, serverVer, webVer, webMinServer string) (*adminUpdateFixture, *tbtest.Server) {
	t.Helper()
	fx := &adminUpdateFixture{corrupt: map[string]bool{}}

	webZip := zipBytesShared(map[string]string{
		"index.html":    fmt.Sprintf("<!doctype html><title>web %s</title>", webVer),
		"assets/app.js": fmt.Sprintf("console.log('%s')", webVer),
	})
	webFiles := map[string][]byte{"taskboard-web-" + webVer + ".zip": webZip}
	manifests := map[string]release.Manifest{
		"server": {Component: "server", Version: serverVer, MinServer: serverVer},
		"web": {Component: "web", Version: webVer, MinServer: webMinServer,
			Files: fileEntriesShared(webFiles)},
	}
	latest := release.Latest{
		Server: &release.LatestEntry{Version: serverVer, Tag: "server/v" + serverVer,
			ManifestURL: "https://github.com/example/example/releases/download/server/v" + serverVer + "/manifest.json"},
		Web: &release.LatestEntry{Version: webVer, Tag: "web/v" + webVer,
			ManifestURL: "https://github.com/example/example/releases/download/web/v" + webVer + "/manifest.json"},
	}
	byPath := map[string][]byte{}
	for component, m := range manifests {
		data, _ := json.Marshal(m)
		byPath["releases/download/"+component+"/v"+m.Version+"/manifest.json"] = data
	}
	for name, content := range webFiles {
		byPath["releases/download/web/v"+webVer+"/"+name] = content
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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
		content, ok := byPath[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if fx.corrupt[key[strings.LastIndex(key, "/")+1:]] {
			content = append(content, '!')
		}
		w.Write(content)
	})
	fx.server = httptest.NewServer(mux)
	t.Cleanup(fx.server.Close)

	// web 目录带版本标记，好让“前端有更新”成立。
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<!doctype html><title>web 0.0.0</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "version.json"), []byte(`{"component":"web","version":"0.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.webDir = webDir

	cfgUpdate := config.Update{
		Sources: []string{"github"},
		GitHub:  config.UpdateGitHub{Repo: "example/example", Mirror: []string{fx.server.URL + "/m/"}},
	}
	s := tbtest.Start(t, tbtest.WithWebDir(webDir), tbtest.WithUpdate(cfgUpdate))
	return fx, s
}

func fileEntriesShared(files map[string][]byte) []release.FileEntry {
	entries := make([]release.FileEntry, 0, len(files))
	for name, content := range files {
		sum := sha256.Sum256(content)
		entries = append(entries, release.FileEntry{
			Name: name, Sha256: hex.EncodeToString(sum[:]), Size: int64(len(content)),
		})
	}
	return entries
}

func zipBytesShared(files map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(content))
	}
	zw.Close()
	return buf.Bytes()
}

// 管理员检查更新：返回实际用的源、前后端当前与最新版本；普通成员 403、
// 未登录 401；所有源连不上时 502。
func Test管理员可检查更新(t *testing.T) {
	// 后端“最新”与测试二进制版本相同（0.0.0）：只有前端有更新。
	fx, s := newAdminUpdateFixture(t, "0.0.0", "1.4.0", "0.0.0")
	s.CreateAdmin("admin", "pass-word")
	s.CreateMember("chen", "pass-word")
	admin := s.Login("admin", "pass-word")
	member := s.Login("chen", "pass-word")
	_ = fx

	res := s.Do(t, s.NewClient(), http.MethodGet, "/api/v1/admin/update", nil, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录状态码 = %d, 想 401", res.StatusCode)
	}
	res = s.Do(t, member, http.MethodGet, "/api/v1/admin/update", nil, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("普通成员状态码 = %d, 想 403", res.StatusCode)
	}

	var status updateStatusBody
	res = s.Do(t, admin, http.MethodGet, "/api/v1/admin/update", nil, &status)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("管理员状态码 = %d, 想 200", res.StatusCode)
	}
	_ = fx
	if status.SourceUsed != "github" {
		t.Errorf("实际使用源 = %q, 想 github", status.SourceUsed)
	}
	if status.Server.UpdateAvailable {
		t.Errorf("后端已是最新，不应提示更新")
	}
	if !status.Web.UpdateAvailable || status.Web.Latest != "1.4.0" {
		t.Errorf("前端应提示有 1.4.0 更新：%+v", status)
	}
	_ = fx
}

// 没有配置更新源时：502 明确报错，而不是连任何默认地址。
func Test检查更新没有配置源时明确报错(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("admin", "pass-word")
	admin := s.Login("admin", "pass-word")

	res := s.Do(t, admin, http.MethodGet, "/api/v1/admin/update", nil, nil)
	if res.StatusCode != http.StatusBadGateway {
		t.Fatalf("状态码 = %d, 想 502", res.StatusCode)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	tbtest.DecodeJSON(t, res, &body)
	if body.Error.Code != "update_source_unreachable" {
		t.Errorf("错误码 = %q, 想 update_source_unreachable", body.Error.Code)
	}
}

// 管理员执行更新（只有前端有新版）：响应记录步骤；之后静态页面直接是新
// 版本（服务不重启）。
func Test管理员执行前端更新不重启即生效(t *testing.T) {
	fx, s := newAdminUpdateFixture(t, "0.0.0", "1.4.0", "0.0.0")
	s.CreateAdmin("admin", "pass-word")
	s.CreateMember("chen", "pass-word")
	admin := s.Login("admin", "pass-word")
	member := s.Login("chen", "pass-word")
	_ = fx

	res := s.Do(t, member, http.MethodPost, "/api/v1/admin/update", nil, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("普通成员状态码 = %d, 想 403", res.StatusCode)
	}

	var result updateResultBody
	res = s.Do(t, admin, http.MethodPost, "/api/v1/admin/update", nil, &result)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("管理员执行更新状态码 = %d, 想 200", res.StatusCode)
	}
	if !result.WebUpdated || result.ServerUpdated || result.RestartNeeded {
		t.Fatalf("应只更新前端：%+v", result)
	}
	if len(result.Steps) != 1 || !strings.Contains(result.Steps[0], "前端") {
		t.Fatalf("步骤 = %v", result.Steps)
	}

	// 静态页面立即是新版本。
	page := s.Do(t, s.NewClient(), http.MethodGet, "/", nil, nil)
	body := make([]byte, 4096)
	n, _ := page.Body.Read(body)
	if !strings.Contains(string(body[:n]), "web 1.4.0") {
		t.Fatalf("更新后首页应是新版本：%q", string(body[:n]))
	}
}

// 产物哈希不符时拒绝更新：500 + update_failed，前端保持原版本。
func Test管理员执行更新哈希不符时拒绝(t *testing.T) {
	fx, s := newAdminUpdateFixture(t, "0.0.0", "1.4.0", "0.0.0")
	s.CreateAdmin("admin", "pass-word")
	admin := s.Login("admin", "pass-word")
	fx.corrupt["taskboard-web-1.4.0.zip"] = true

	res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/update", nil, nil)
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d, 想 500", res.StatusCode)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	tbtest.DecodeJSON(t, res, &body)
	if body.Error.Code != "update_failed" {
		t.Errorf("错误码 = %q, 想 update_failed", body.Error.Code)
	}
	page := s.Do(t, s.NewClient(), http.MethodGet, "/", nil, nil)
	buf := make([]byte, 4096)
	n, _ := page.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "web 0.0.0") {
		t.Fatalf("拒绝更新后首页应保持原版本：%q", string(buf[:n]))
	}
}

// 健康检查：公开、不需要登录（更新后由新版本启动时自检用，也是 Caddy 的
// 探活地址）。
func Test健康检查公开可用(t *testing.T) {
	s := tbtest.Start(t)
	res := s.Do(t, s.NewClient(), http.MethodGet, "/api/v1/health", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d, 想 200", res.StatusCode)
	}
	var body struct {
		Status string `json:"status"`
	}
	tbtest.DecodeJSON(t, res, &body)
	if body.Status != "ok" {
		t.Errorf("status = %q, 想 ok", body.Status)
	}
}

// 访客调用管理接口（更新、备份状态）得 403 members_only（决议 4：仅成员的
// 接口对访客统一这一个错误码，评审 P1-5）；管理员的 Agent token 与本人会话
// 一样可以查看更新状态（与标签治理一致）。
func Test管理接口对访客是membersOnly(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("admin", "pass-word")
	admin := s.Login("admin", "pass-word")
	_, token := s.IssueAgentToken(t, admin, "秘书")
	adminAgent := s.AgentClient(token)
	guest := s.GuestClient(tbguestKey(t, s, admin))

	cases := []struct {
		name string
		path string
	}{
		{"更新状态", "/api/v1/admin/update"},
		{"备份状态", "/api/v1/admin/backups"},
	}
	for _, tc := range cases {
		res := s.Do(t, guest, http.MethodGet, tc.path, nil, nil)
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("%s：访客状态码 = %d, 想 403", tc.name, res.StatusCode)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "members_only" {
			t.Errorf("%s：访客错误码 = %q, 想 members_only", tc.name, got.Error.Code)
		}

		// 管理员的 Agent token 同样可以（没有配置更新源时是 502 update_source_unreachable，
		// 说明已过了权限检查，仅限成员本人的入口不在此列）。
		res = s.Do(t, adminAgent, http.MethodGet, tc.path, nil, nil)
		if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
			t.Errorf("%s：管理员 Agent 不应被权限挡住（%d：%s）", tc.name, res.StatusCode, readAll(t, res))
		}
	}
}
