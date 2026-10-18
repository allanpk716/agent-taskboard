package server_test

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/clientstamp"
	"github.com/allanpk716/agent-taskboard/internal/release"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
	"github.com/allanpk716/agent-taskboard/internal/updater"
)

func testClientExe(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 0x80)
	copy(b, "MZ")
	binary.LittleEndian.PutUint32(b[0x3c:], 0x40)
	copy(b[0x40:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(b[0x44:], 0x8664)
	return append(b, "desktop client\n"...)
}

func sha256Sum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func installTestClient(t *testing.T, s *tbtest.Server) updater.ClientPackage {
	t.Helper()
	data := testClientExe(t)
	sum := sha256Sum(data)
	pkg := updater.ClientPackage{Version: "1.2.0", Sha256: sum, Size: int64(len(data))}
	if err := updater.InstallClient(s.DataDir(), data, pkg); err != nil {
		t.Fatal(err)
	}
	return pkg
}

func Test客户端最新版本和下载仅成员或客户端凭证可调(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("admin", "pass-word")
	s.CreateMember("wang", "pass-word")
	web := s.Login("wang", "pass-word")
	pkg := installTestClient(t, s)
	credential := s.IssueClientCredential(t, web, "WORKSTATION-01")
	client := s.ClientCredentialClient(credential.Token)
	_, agentToken := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(agentToken)
	guest := s.GuestClient(tbguestKey(t, s, s.Login("admin", "pass-word")))

	for _, path := range []string{"/api/v1/client/latest", "/api/v1/client/download"} {
		res := s.Do(t, s.NewClient(), http.MethodGet, path, nil, nil)
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s 未登录状态码 = %d, 想 401", path, res.StatusCode)
		}
		res = s.Do(t, guest, http.MethodGet, path, nil, nil)
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("%s 访客状态码 = %d, 想 403", path, res.StatusCode)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "members_only" {
			t.Fatalf("%s 访客错误码 = %q, 想 members_only", path, got.Error.Code)
		}
		res = s.Do(t, agent, http.MethodGet, path, nil, nil)
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("%s 成员 Agent 状态码 = %d, 想 403", path, res.StatusCode)
		}
		got = errorBody{}
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "agent_not_allowed" {
			t.Fatalf("%s 成员 Agent 错误码 = %q, 想 agent_not_allowed", path, got.Error.Code)
		}
	}

	var latest struct {
		Version     string `json:"version"`
		Sha256      string `json:"sha256"`
		Size        int64  `json:"size"`
		DownloadURL string `json:"download_url"`
	}
	res := s.Do(t, client, http.MethodGet, "/api/v1/client/latest", nil, &latest)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("客户端凭证查 latest 状态码 = %d", res.StatusCode)
	}
	data := testClientExe(t)
	stamped, err := clientstamp.Append(data, s.URL())
	if err != nil {
		t.Fatal(err)
	}
	if latest != (struct {
		Version     string `json:"version"`
		Sha256      string `json:"sha256"`
		Size        int64  `json:"size"`
		DownloadURL string `json:"download_url"`
	}{pkg.Version, sha256Sum(stamped), int64(len(stamped)), "/api/v1/client/download"}) {
		t.Fatalf("latest 响应不对：%+v", latest)
	}

	res = s.Do(t, web, http.MethodGet, "/api/v1/client/download", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("成员下载状态码 = %d", res.StatusCode)
	}
	if got := res.Header.Get("Content-Disposition"); !strings.Contains(got, `taskboard-client.exe`) {
		t.Fatalf("Content-Disposition 不对：%q", got)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(stamped) {
		t.Fatalf("下载内容不含请求地址的尾部戳")
	}
	stampURL, ok, err := clientstamp.ReadFromBytes(body)
	if err != nil || !ok || stampURL != s.URL() {
		t.Fatalf("下载尾部地址 = %q, %v, %v；想 %q", stampURL, ok, err, s.URL())
	}
}

func Test客户端下载戳缓存最多八个地址(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("admin", "pass-word")
	admin := s.Login("admin", "pass-word")
	installTestClient(t, s)
	current, err := updater.CurrentClientPackage(s.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(current.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.WriteFile(current.Path, original, 0o755) })

	for i := 1; i <= 8; i++ {
		host := "cache-" + strconv.Itoa(i) + ".example.com"
		res := s.DoWithHeaders(t, admin, http.MethodGet, "/api/v1/client/latest",
			map[string]string{"X-Forwarded-Host": host}, nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("预热缓存 %d 状态码 = %d", i, res.StatusCode)
		}
	}
	if err := os.WriteFile(current.Path, append(original, 'x'), 0o755); err != nil {
		t.Fatal(err)
	}
	res := s.DoWithHeaders(t, admin, http.MethodGet, "/api/v1/client/latest",
		map[string]string{"X-Forwarded-Host": "cache-1.example.com"}, nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("命中缓存不应重读整包，状态码 = %d", res.StatusCode)
	}
	res = s.DoWithHeaders(t, admin, http.MethodGet, "/api/v1/client/latest",
		map[string]string{"X-Forwarded-Host": "cache-9.example.com"}, nil, nil)
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("第 9 个地址应清空缓存并重新校验，状态码 = %d", res.StatusCode)
	}
}

func Test客户端下载尾部使用public_url并与latest一致(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithPublicURL("https://taskboard.example.com"))
	s.CreateMember("wang", "pass-word")
	web := s.Login("wang", "pass-word")
	data := testClientExe(t)
	if err := updater.InstallClient(s.DataDir(), data, updater.ClientPackage{
		Version: "1.2.0", Sha256: sha256Sum(data), Size: int64(len(data)),
	}); err != nil {
		t.Fatal(err)
	}

	var latest struct {
		Sha256 string `json:"sha256"`
		Size   int64  `json:"size"`
	}
	res := s.Do(t, web, http.MethodGet, "/api/v1/client/latest", nil, &latest)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("latest 状态码 = %d", res.StatusCode)
	}
	res = s.Do(t, web, http.MethodGet, "/api/v1/client/download", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("下载状态码 = %d", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	stamped, err := clientstamp.Append(data, "https://taskboard.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(stamped) {
		t.Fatal("下载内容应使用 public_url 的尾部戳")
	}
	if latest.Sha256 != sha256Sum(body) || latest.Size != int64(len(body)) {
		t.Fatalf("latest 元数据与下载内容不一致：%+v，下载 %d 字节", latest, len(body))
	}
}

func Test客户端未代管时latest和下载返回clientNotAvailable(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateMember("wang", "pass-word")
	web := s.Login("wang", "pass-word")

	for _, path := range []string{"/api/v1/client/latest", "/api/v1/client/download"} {
		res := s.Do(t, web, http.MethodGet, path, nil, nil)
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("%s 状态码 = %d, 想 404", path, res.StatusCode)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "client_not_available" {
			t.Fatalf("%s 错误码 = %q, 想 client_not_available", path, got.Error.Code)
		}
	}
}

func Test管理员更新可只代管客户端并保留上一版(t *testing.T) {
	fx, s := newAdminUpdateFixture(t, "0.0.0", "0.0.0", "0.0.0")
	old := testClientExe(t)
	if err := updater.InstallClient(s.DataDir(), old, updater.ClientPackage{
		Version: "1.0.0", Sha256: sha256Sum(old), Size: int64(len(old)),
	}); err != nil {
		t.Fatal(err)
	}
	exe := testClientExe(t)
	exe = append(exe, "1.2.0"...)
	files := map[string][]byte{"taskboard-client.exe": exe}
	manifest := release.Manifest{Component: "client", Version: "1.2.0", Files: fileEntriesShared(files)}
	fx.addClient(t, "1.2.0", manifest, files)
	s.CreateAdmin("admin", "pass-word")
	admin := s.Login("admin", "pass-word")

	var status updateStatusBody
	res := s.Do(t, admin, http.MethodGet, "/api/v1/admin/update", nil, &status)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("检查状态码 = %d", res.StatusCode)
	}
	if status.Client.Current != "1.0.0" || status.Client.Latest != "1.2.0" || !status.Client.UpdateAvailable {
		t.Fatalf("client 状态不对：%+v", status.Client)
	}
	var result updateResultBody
	res = s.Do(t, admin, http.MethodPost, "/api/v1/admin/update", nil, &result)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("执行状态码 = %d", res.StatusCode)
	}
	if !result.ClientUpdated || result.ServerUpdated || result.WebUpdated || result.RestartNeeded {
		t.Fatalf("应只更新 client：%+v", result)
	}
	currentPkg, err := updater.CurrentClientPackage(s.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(currentPkg.Path)
	if err != nil || string(got) != string(exe) {
		t.Fatalf("当前代管客户端不对：%q (%v)", got, err)
	}
	prevPkg, err := updater.PreviousClientPackage(s.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	prev, err := os.ReadFile(prevPkg.Path)
	if err != nil || string(prev) != string(old) {
		t.Fatalf("上一版代管客户端不对：%q (%v)", prev, err)
	}
}

func Test管理员更新客户端SHA256不符时不替换(t *testing.T) {
	fx, s := newAdminUpdateFixture(t, "0.0.0", "0.0.0", "0.0.0")
	before := testClientExe(t)
	if err := updater.InstallClient(s.DataDir(), before, updater.ClientPackage{
		Version: "1.0.0", Sha256: sha256Sum(before), Size: int64(len(before)),
	}); err != nil {
		t.Fatal(err)
	}
	exe := testClientExe(t)
	files := map[string][]byte{"taskboard-client.exe": exe}
	manifest := release.Manifest{Component: "client", Version: "1.2.0", Files: fileEntriesShared(files)}
	fx.addClient(t, "1.2.0", manifest, files)
	fx.corrupt["taskboard-client.exe"] = true
	s.CreateAdmin("admin", "pass-word")
	admin := s.Login("admin", "pass-word")

	var result updateResultBody
	res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/update", nil, &result)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d, 想 200", res.StatusCode)
	}
	if result.ClientUpdated || result.ClientError == "" {
		t.Fatalf("client 组件失败结果 = %+v", result)
	}
	currentPkg, err := updater.CurrentClientPackage(s.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(currentPkg.Path)
	if err != nil || !strings.HasSuffix(string(data), "desktop client\n") {
		t.Fatalf("SHA256 不符时不应替换：%q (%v)", data, err)
	}
}
