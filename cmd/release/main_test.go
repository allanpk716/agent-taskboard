package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/release"
)

// CI 和本地演练共用同一个 release 工具：manifest 子命令生成 Release 的
// manifest.json，latest 子命令读改写 channel-stable 的 latest.json。
// 这里编译真实二进制走一遍命令行。

func buildReleaseBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "release")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "github.com/allanpk716/agent-taskboard/cmd/release")
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("编译 release 失败：%v\n%s", err, out)
	}
	return bin
}

func projectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(filepath.Dir(dir)) // cmd/release/ -> 仓库根
}

func runRelease(t *testing.T, bin string, args ...string) string {
	t.Helper()
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("release %s 失败：%v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func Test命令行生成的manifest记录SHA256(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过：需要编译真实二进制")
	}
	bin := buildReleaseBinary(t)
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "taskboard.exe"), []byte("windows-binary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "taskboard-linux-amd64"), []byte("linux-binary\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runRelease(t, bin, "manifest", "-component", "server", "-version", "1.2.3", "-dir", dist)

	data, err := os.ReadFile(filepath.Join(dist, "manifest.json"))
	if err != nil {
		t.Fatalf("应写入 dist/manifest.json：%v", err)
	}
	var m release.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("解析 manifest.json：%v\n%s", err, data)
	}
	if m.Component != "server" || m.Version != "1.2.3" || m.MinServer != "1.2.3" {
		t.Fatalf("清单头不对：%+v", m)
	}
	want := map[string]string{
		"taskboard.exe":         "f9e537f375f9b8da666b1fe5addf9ec0a793cc841cc88e7549596a6615bfe6d0",
		"taskboard-linux-amd64": "c8129af264901ca76e037945f033c1dc7b05f000e2eeec96d74ea0a69d9bfb61",
	}
	if len(m.Files) != len(want) {
		t.Fatalf("manifest 应记录全部产物：%+v", m.Files)
	}
	for _, f := range m.Files {
		if want[f.Name] != f.Sha256 {
			t.Errorf("%s 的 SHA256 不对：%s（期望 %s）", f.Name, f.Sha256, want[f.Name])
		}
	}
}

func Test命令行合并latest不覆盖另一组件(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过：需要编译真实二进制")
	}
	bin := buildReleaseBinary(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "latest.json")

	// 第一次发布后端：文件还不存在，从空开始。
	runRelease(t, bin, "latest", "-component", "server", "-version", "1.2.0",
		"-tag", "server/v1.2.0", "-repo", "acme/board", "-file", file)
	// 再发布前端：默认写回同一个文件。
	runRelease(t, bin, "latest", "-component", "web", "-version", "1.4.0",
		"-tag", "web/v1.4.0", "-repo", "acme/board", "-file", file)

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var l release.Latest
	if err := json.Unmarshal(data, &l); err != nil {
		t.Fatalf("解析 latest.json：%v\n%s", err, data)
	}
	if l.Server == nil || l.Server.Version != "1.2.0" ||
		l.Server.ManifestURL != "https://github.com/acme/board/releases/download/server/v1.2.0/manifest.json" {
		t.Fatalf("server 条目不对：%+v", l.Server)
	}
	if l.Web == nil || l.Web.Version != "1.4.0" {
		t.Fatalf("web 条目不对：%+v", l.Web)
	}
}

func Test命令行拒绝未知子命令(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过：需要编译真实二进制")
	}
	bin := buildReleaseBinary(t)
	out, err := exec.Command(bin, "deploy").CombinedOutput()
	if err == nil {
		t.Fatal("未知子命令应退出非零")
	}
	if !strings.Contains(string(out), "deploy") {
		t.Fatalf("报错应包含子命令名：%s", out)
	}
}

// check-exe 子命令：发布流水线构建完核对产物平台（server/v0.1.4 的
// taskboard.exe 其实是 Linux 二进制）。拿 release 自己的二进制当样本：
// 按本平台检查通过，按别的平台或对纯文本检查失败。
func Test命令行核对产物平台(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过：需要编译真实二进制")
	}
	bin := buildReleaseBinary(t)
	runRelease(t, bin, "check-exe", "-goos", runtime.GOOS, "-goarch", runtime.GOARCH, bin)

	other := map[string]string{"windows": "linux", "linux": "windows", "darwin": "linux"}[runtime.GOOS]
	if out, err := exec.Command(bin, "check-exe", "-goos", other, "-goarch", runtime.GOARCH, bin).CombinedOutput(); err == nil {
		t.Fatalf("按 %s 检查 %s 的二进制应失败：%s", other, runtime.GOOS, out)
	}
	text := filepath.Join(t.TempDir(), "taskboard.exe")
	if err := os.WriteFile(text, []byte("not an exe"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "check-exe", "-goos", "windows", "-goarch", "amd64", text).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "taskboard.exe") {
		t.Fatalf("纯文本应检查失败且报出文件名：%v\n%s", err, out)
	}
}
