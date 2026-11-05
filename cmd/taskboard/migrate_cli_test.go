package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func Test帮助输出包含附件迁移命令(t *testing.T) {
	out := &bytes.Buffer{}
	usage(out)
	got := out.String()
	for _, want := range []string{
		"taskboard migrate-attachments -to oss|local [flags]",
		"-dry-run",
		"-accept-missing",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("帮助输出缺少 %q：\n%s", want, got)
		}
	}
}

func Test附件迁移命令在服务运行时拒绝且不打开库(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	configPath := filepath.Join(dir, "config.toml")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听临时端口失败：%v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(
		"data_dir = '%s'\nport = %d\n", filepath.ToSlash(dataDir), port,
	)), 0o644); err != nil {
		t.Fatal(err)
	}

	err = run([]string{"migrate-attachments", "-to", "oss", "-config", configPath})
	if err == nil || !strings.Contains(err.Error(), "仍有服务在监听") {
		t.Fatalf("服务运行时 err = %v, 想拒绝执行", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "taskboard.db")); !os.IsNotExist(err) {
		t.Fatalf("拒绝执行时不应打开数据库：%v", err)
	}
}
