// 目录与数据库文件大小统计的单元测试（#234 管理页「存储统计」的底座）。
package storage_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/storage"
)

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 目录不存在、不可读时按 0 处理；只统计普通文件，递归子目录。
func TestDirBytes递归统计普通文件(t *testing.T) {
	dir := t.TempDir()
	if got := storage.DirBytes(dir); got != 0 {
		t.Fatalf("空目录 DirBytes = %d, 想 0", got)
	}
	writeFile(t, filepath.Join(dir, "a.txt"), 100)
	writeFile(t, filepath.Join(dir, "sub", "b.txt"), 50)
	writeFile(t, filepath.Join(dir, "sub", "deep", "c.txt"), 25)
	if err := os.Symlink(filepath.Join(dir, "a.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Skip("当前环境不能建符号链接")
	}
	if got := storage.DirBytes(dir); got != 175 {
		t.Fatalf("DirBytes = %d, 想 175（符号链接不是普通文件）", got)
	}
	if got := storage.DirBytes(filepath.Join(dir, "不存在")); got != 0 {
		t.Fatalf("目录不存在时 DirBytes = %d, 想 0", got)
	}
}

// 附件目录与备份目录互相嵌套时各计各的：被排除目录里的文件不计入。
func TestDirBytes排除嵌套目录(t *testing.T) {
	dir := t.TempDir()
	attachments := filepath.Join(dir, "attachments")
	backups := filepath.Join(attachments, "backups")
	writeFile(t, filepath.Join(attachments, "1"), 100)
	writeFile(t, filepath.Join(backups, "mirror.db"), 500)
	if got := storage.DirBytes(attachments, backups); got != 100 {
		t.Fatalf("附件目录（排除备份镜像）= %d, 想 100", got)
	}
	if got := storage.DirBytes(backups, attachments); got != 500 {
		t.Fatalf("备份目录 = %d, 想 500", got)
	}
}

// database_bytes = taskboard.db + -wal + -shm，缺的按 0。
func TestDatabaseBytes合计库与wal与shm(t *testing.T) {
	dir := t.TempDir()
	if got := storage.DatabaseBytes(dir); got != 0 {
		t.Fatalf("没有库文件时 DatabaseBytes = %d, 想 0", got)
	}
	writeFile(t, filepath.Join(dir, "taskboard.db"), 1000)
	writeFile(t, filepath.Join(dir, "taskboard.db-wal"), 200)
	writeFile(t, filepath.Join(dir, "taskboard.db-shm"), 32)
	writeFile(t, filepath.Join(dir, "别的.db"), 999)
	if got := storage.DatabaseBytes(dir); got != 1232 {
		t.Fatalf("DatabaseBytes = %d, 想 1232", got)
	}
}
