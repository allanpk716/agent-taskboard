package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 每个发布 Release 附带 manifest.json，记录 component、version、min_server
// 和每个产物的 SHA256，更新器据此校验下载。SHA256 期望值来自独立的
// sha256sum 计算结果，避免和实现共享同一条计算路径。

func writeDistFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestBuildManifest为每个产物计算SHA256和大小(t *testing.T) {
	dir := writeDistFiles(t, map[string]string{
		"taskboard.exe":         "windows-binary\n",
		"taskboard-linux-amd64": "linux-binary\n",
		"manifest.json":         "旧的清单不应被自动计入",
	})

	m, err := BuildManifest("server", "1.2.3", "1.2.3", dir, nil)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	if m.Component != "server" || m.Version != "1.2.3" || m.MinServer != "1.2.3" {
		t.Fatalf("清单头不对：%+v", m)
	}
	want := []FileEntry{
		{Name: "taskboard-linux-amd64", Sha256: "c8129af264901ca76e037945f033c1dc7b05f000e2eeec96d74ea0a69d9bfb61", Size: 13},
		{Name: "taskboard.exe", Sha256: "f9e537f375f9b8da666b1fe5addf9ec0a793cc841cc88e7549596a6615bfe6d0", Size: 15},
	}
	if len(m.Files) != len(want) {
		t.Fatalf("期望 %d 个产物，得到 %d：%+v", len(want), len(m.Files), m.Files)
	}
	for i, e := range m.Files {
		if e != want[i] {
			t.Errorf("产物 %d：期望 %+v，得到 %+v", i, want[i], e)
		}
	}
}

func TestBuildManifest只对指定的产物生成清单(t *testing.T) {
	dir := writeDistFiles(t, map[string]string{
		"a.zip":         "a\n",
		"b.zip":         "b\n",
		"manifest.json": "应被忽略",
	})

	m, err := BuildManifest("web", "1.4.0", "1.2.0", dir, []string{"b.zip", "a.zip"})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	if len(m.Files) != 2 || m.Files[0].Name != "a.zip" || m.Files[1].Name != "b.zip" {
		t.Fatalf("应按名称排序且只含指定产物：%+v", m.Files)
	}
}

func TestBuildManifest拒绝未知组件(t *testing.T) {
	dir := writeDistFiles(t, map[string]string{"a.zip": "a\n"})
	if _, err := BuildManifest("cli", "1.0.0", "1.0.0", dir, nil); err == nil {
		t.Fatal("component 只能是 server 或 web，cli 应被拒绝")
	}
}

func TestBuildManifest支持客户端且minServer可为空(t *testing.T) {
	dir := writeDistFiles(t, map[string]string{"taskboard-client.exe": "client-binary\n"})
	m, err := BuildManifest("client", "1.0.0", "", dir, nil)
	if err != nil {
		t.Fatalf("BuildManifest client：%v", err)
	}
	if m.Component != "client" || m.Version != "1.0.0" || m.MinServer != "" {
		t.Fatalf("client 清单头不对：%+v", m)
	}
	if len(m.Files) != 1 || m.Files[0].Name != "taskboard-client.exe" || m.Files[0].Sha256 == "" {
		t.Fatalf("client 清单应记录 exe：%+v", m.Files)
	}
}

func TestBuildManifest拒绝非语义化版本(t *testing.T) {
	dir := writeDistFiles(t, map[string]string{"a.zip": "a\n"})
	for _, v := range []string{"", "1.2", "latest", "1.2.3.4"} {
		if _, err := BuildManifest("server", v, "1.0.0", dir, nil); err == nil {
			t.Fatalf("版本 %q 应被拒绝", v)
		}
	}
	// 允许带 v 前缀（与 tag 名一致），写入清单时去掉前缀。
	m, err := BuildManifest("server", "v1.2.3", "1.2.3", dir, nil)
	if err != nil || m.Version != "1.2.3" {
		t.Fatalf("v 前缀应被接受并归一化：%v %+v", err, m)
	}
}

func TestBuildManifest在产物缺失时报错(t *testing.T) {
	dir := writeDistFiles(t, map[string]string{"a.zip": "a\n"})
	_, err := BuildManifest("server", "1.2.3", "1.2.3", dir, []string{"a.zip", "missing.zip"})
	if err == nil || !strings.Contains(err.Error(), "missing.zip") {
		t.Fatalf("缺失产物应报错并指出文件名：%v", err)
	}
}
