package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/release"
)

func testWebZip(t *testing.T, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create("index.html")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("web " + version)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// windowsAMD64Exe 构造 debug/pe 能识别的最小 PE 文件；代管包永远是
// windows/amd64 产物，测试不能依赖宿主平台。
func windowsAMD64Exe(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 0x80)
	copy(b, "MZ")
	binary.LittleEndian.PutUint32(b[0x3c:], 0x40)
	copy(b[0x40:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(b[0x44:], 0x8664)
	return append(b, "taskboard-client\n"...)
}

// nativeExe 返回当前平台可执行文件；服务端产物要能通过更新器的本机架构校验。
func nativeExe(t *testing.T) []byte {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func readClient(t *testing.T, dataDir string) ([]byte, ClientPackage) {
	t.Helper()
	pkg, err := CurrentClientPackage(dataDir)
	if err != nil {
		t.Fatalf("读取代管客户端：%v", err)
	}
	data, err := os.ReadFile(pkg.Path)
	if err != nil {
		t.Fatal(err)
	}
	return data, *pkg
}

func Test安装代管客户端校验并保留上一版(t *testing.T) {
	dataDir := t.TempDir()
	v1 := windowsAMD64Exe(t)
	v2 := append(windowsAMD64Exe(t), "2.0.0"...)
	if err := InstallClient(dataDir, v1, clientManifest("1.0.0", v1)); err != nil {
		t.Fatalf("安装 1.0.0：%v", err)
	}
	if err := InstallClient(dataDir, v2, clientManifest("2.0.0", v2)); err != nil {
		t.Fatalf("安装 2.0.0：%v", err)
	}
	got, pkg := readClient(t, dataDir)
	if pkg.Version != "2.0.0" || string(got) != string(v2) {
		t.Fatalf("当前客户端不对：%s %+v", got, pkg)
	}
	prevPkg, err := PreviousClientPackage(dataDir)
	if err != nil {
		t.Fatalf("读取上一版：%v", err)
	}
	if prevPkg.Version != "1.0.0" || prevPkg.Path != filepath.Join(dataDir, clientDirName, "taskboard-client-1.0.0.exe") {
		t.Fatalf("上一版 = %+v, 想 1.0.0 的版本化文件", prevPkg)
	}
	prev, err := os.ReadFile(prevPkg.Path)
	if err != nil || string(prev) != string(v1) {
		t.Fatalf("上一版内容不对：%q, %v", prev, err)
	}
}

func Test不是Windows客户端或内容不符时拒绝安装(t *testing.T) {
	dataDir := t.TempDir()
	current := windowsAMD64Exe(t)
	if err := InstallClient(dataDir, current, clientManifest("1.0.0", current)); err != nil {
		t.Fatalf("安装初始版本：%v", err)
	}
	if err := InstallClient(dataDir, []byte("not an exe"), clientManifest("2.0.0", []byte("not an exe"))); err == nil {
		t.Fatal("非 Windows 可执行文件应拒绝")
	}
	if err := InstallClient(dataDir, append(current, '!'), clientManifest("2.0.0", current)); err == nil {
		t.Fatal("内容与清单不符应拒绝")
	}
	got, pkg := readClient(t, dataDir)
	if pkg.Version != "1.0.0" || string(got) != string(current) {
		t.Fatalf("拒绝后当前版本不应变化：%+v", pkg)
	}
	if _, err := os.Stat(pkg.Path + ".prev"); !os.IsNotExist(err) {
		t.Fatal("拒绝时不应产生上一版")
	}
}

func clientManifest(version string, data []byte) ClientPackage {
	sum := sha256.Sum256(data)
	return ClientPackage{
		Version: version,
		Sha256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(data)),
	}
}

func Test没有代管客户端时版本为空(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := os.Stat(filepath.Join(dataDir, clientDirName)); !os.IsNotExist(err) {
		t.Fatal("初始数据目录不应创建 client 目录")
	}
	if got := ClientVersion(dataDir); got != "" {
		t.Fatalf("未代管版本 = %q, 想空串", got)
	}
}

func Test客户端版本查询只读元数据(t *testing.T) {
	dataDir := t.TempDir()
	current := windowsAMD64Exe(t)
	if err := InstallClient(dataDir, current, clientManifest("1.0.0", current)); err != nil {
		t.Fatal(err)
	}
	pkg, err := CurrentClientPackage(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(pkg.Path); err != nil {
		t.Fatal(err)
	}
	if got := ClientVersion(dataDir); got != "1.0.0" {
		t.Fatalf("只读元数据时版本 = %q, 想 1.0.0", got)
	}
}
func clientOnlyFixture(t *testing.T) *githubFixture {
	t.Helper()
	fx := newGitHubFixture("0.0.0", "0.0.0", "0.0.0")
	exe := windowsAMD64Exe(t)
	files := map[string][]byte{"taskboard-client.exe": exe}
	m := testManifest("client", "1.0.0", "", files)
	data, _ := json.Marshal(m)
	fx.bySuffix["releases/download/client/v1.0.0/manifest.json"] = data
	fx.bySuffix["releases/download/client/v1.0.0/taskboard-client.exe"] = exe
	fx.latest.Client = &release.LatestEntry{
		Version: "1.0.0", Tag: "client/v1.0.0",
		ManifestURL: "https://github.com/example/example/releases/download/client/v1.0.0/manifest.json",
	}
	return fx
}

func Test更新器独立更新客户端并保留上一版(t *testing.T) {
	fx := clientOnlyFixture(t)
	fake := newFakeGitHub(t, fx.latest, fx.bySuffix)
	dataDir := t.TempDir()
	opt := Options{
		Update:  config.Update{Sources: []string{"github"}, GitHub: config.UpdateGitHub{Repo: "example/example", Mirror: []string{fake.server.URL + "/m/"}}},
		DataDir: dataDir, WebDir: t.TempDir(), ServerVersion: "0.0.0",
	}
	if err := os.WriteFile(filepath.Join(opt.WebDir, "version.json"), []byte(`{"component":"web","version":"0.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Apply(context.Background(), opt)
	if err != nil {
		t.Fatalf("Apply：%v", err)
	}
	if !res.ClientUpdated || res.ServerUpdated || res.WebUpdated || res.RestartNeeded {
		t.Fatalf("应只更新客户端：%+v", res)
	}
	got, pkg := readClient(t, dataDir)
	if pkg.Version != "1.0.0" || string(got) != string(fx.bySuffix["releases/download/client/v1.0.0/taskboard-client.exe"]) {
		t.Fatalf("代管客户端不对：%s %+v", got, pkg)
	}
}

func Test客户端SHA256不符时不替换(t *testing.T) {
	fx := clientOnlyFixture(t)
	fx.bySuffix["releases/download/client/v1.0.0/taskboard-client.exe"] = append(windowsAMD64Exe(t), "tampered"...)
	fake := newFakeGitHub(t, fx.latest, fx.bySuffix)
	dataDir := t.TempDir()
	before := windowsAMD64Exe(t)
	if err := InstallClient(dataDir, before, clientManifest("0.9.0", before)); err != nil {
		t.Fatal(err)
	}
	opt := Options{
		Update:  config.Update{Sources: []string{"github"}, GitHub: config.UpdateGitHub{Repo: "example/example", Mirror: []string{fake.server.URL + "/m/"}}},
		DataDir: dataDir, WebDir: t.TempDir(), ServerVersion: "0.0.0",
	}
	res, err := Apply(context.Background(), opt)
	if err != nil || !strings.Contains(res.ClientError, "SHA256") {
		t.Fatalf("SHA256 不符应成为组件失败：%v/%+v", err, res)
	}
	got, pkg := readClient(t, dataDir)
	if pkg.Version != "0.9.0" || string(got) != string(before) {
		t.Fatalf("拒绝后不应替换：%+v", pkg)
	}
}

func Test客户端失败不阻断服务端和前端更新(t *testing.T) {
	fx := newGitHubFixture("1.0.0", "1.0.0", "")
	serverExe := nativeExe(t)
	serverFiles := map[string][]byte{
		"taskboard.exe": serverExe, "taskboard-linux-amd64": serverExe,
	}
	serverManifest := testManifest("server", "1.0.0", "1.0.0", serverFiles)
	serverManifestRaw, _ := json.Marshal(serverManifest)
	fx.bySuffix["releases/download/server/v1.0.0/manifest.json"] = serverManifestRaw
	for name, content := range serverFiles {
		fx.bySuffix["releases/download/server/v1.0.0/"+name] = content
	}
	webZip := testWebZip(t, "1.0.0")
	webFiles := map[string][]byte{"taskboard-web-1.0.0.zip": webZip}
	webManifest := testManifest("web", "1.0.0", "", webFiles)
	webManifestRaw, _ := json.Marshal(webManifest)
	fx.bySuffix["releases/download/web/v1.0.0/manifest.json"] = webManifestRaw
	fx.bySuffix["releases/download/web/v1.0.0/taskboard-web-1.0.0.zip"] = webZip
	clientExe := windowsAMD64Exe(t)
	clientFiles := map[string][]byte{"taskboard-client.exe": clientExe}
	clientManifest := testManifest("client", "1.0.0", "", clientFiles)
	clientManifestRaw, _ := json.Marshal(clientManifest)
	fx.bySuffix["releases/download/client/v1.0.0/manifest.json"] = clientManifestRaw
	fx.bySuffix["releases/download/client/v1.0.0/taskboard-client.exe"] = append(windowsAMD64Exe(t), "tampered"...)
	fx.latest.Client = &release.LatestEntry{
		Version: "1.0.0", Tag: "client/v1.0.0",
		ManifestURL: "https://github.com/example/example/releases/download/client/v1.0.0/manifest.json",
	}
	fake := newFakeGitHub(t, fx.latest, fx.bySuffix)

	dataDir, webDir, exeDir := t.TempDir(), t.TempDir(), t.TempDir()
	exePath := filepath.Join(exeDir, "taskboard.exe")
	if err := os.WriteFile(exePath, windowsAMD64Exe(t), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "version.json"), []byte(`{"component":"web","version":"0.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	logs := make([]string, 0, 4)
	res, err := Apply(context.Background(), Options{
		Update: config.Update{Sources: []string{"github"}, GitHub: config.UpdateGitHub{
			Repo: "example/example", Mirror: []string{fake.server.URL + "/m/"},
		}}, DataDir: dataDir, WebDir: webDir, ExePath: exePath, ServerVersion: "0.0.0",
		Preupdate: func(context.Context) (PreupdateBackup, error) {
			return PreupdateBackup{Filename: "backup.sql"}, nil
		},
		Log: func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) },
	})
	if err != nil {
		t.Fatalf("client 失败不应让整次更新失败：%v", err)
	}
	if !res.ServerUpdated || !res.WebUpdated || res.ClientUpdated {
		t.Fatalf("组件更新结果 = %+v, 想 server/web 成功且 client 失败", res)
	}
	if !strings.Contains(res.ClientError, "SHA256") {
		t.Fatalf("client 组件错误 = %q, 想包含 SHA256", res.ClientError)
	}
	foundClientStep := false
	for _, step := range res.Steps {
		if strings.Contains(step, "客户端 未知 → 1.0.0 失败：") && strings.Contains(step, "SHA256") {
			foundClientStep = true
		}
	}
	if !foundClientStep {
		t.Fatalf("更新步骤缺少客户端失败说明：%v", res.Steps)
	}
	if got := WebVersion(webDir); got != "1.0.0" {
		t.Fatalf("前端版本 = %q, 想 1.0.0", got)
	}
	if got := ClientVersion(dataDir); got != "" {
		t.Fatalf("client 不应被替换，当前版本 = %q", got)
	}
	foundBackupLog := false
	for _, line := range logs {
		if strings.Contains(line, "backup.sql") {
			foundBackupLog = true
		}
	}
	if !foundBackupLog {
		t.Fatalf("后端更新日志缺少备份文件名：%v", logs)
	}
}
