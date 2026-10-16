package updater

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// makeWebDir 在 parent 下建一个目录（带一个占位文件）并把修改时间拨到 mod，
// 返回完整路径。
func makeWebDir(t *testing.T, parent, name string, mod time.Time) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dir, mod, mod); err != nil {
		t.Fatal(err)
	}
	return dir
}

func mustExist(t *testing.T, path, what string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("%s（%s）不应被删除：%v", what, path, err)
	}
}

func mustNotExist(t *testing.T, path, what string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("%s（%s）应被删除（err=%v）", what, path, err)
	}
}

// pruneWebVersions 只删更新器自己命名的目录（评审 P1-2）：web-src 这样的
// 同级源码目录无论多旧都绝不能动。
func TestPruneWebVersions保留同级webSrc(t *testing.T) {
	parent := t.TempDir()
	base := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	v100 := makeWebDir(t, parent, "web-1.0.0", base) // 最旧
	makeWebDir(t, parent, "web-1.1.0", base.Add(time.Hour))
	makeWebDir(t, parent, "web-1.2.0", base.Add(2*time.Hour))
	src := makeWebDir(t, parent, "web-src", base.Add(-24*time.Hour)) // 比所有版本都旧

	pruneWebVersions(parent, 2)

	mustNotExist(t, v100, "最旧的版本目录")
	for _, p := range []struct{ path, what string }{
		{filepath.Join(parent, "web-1.1.0"), "保留的版本目录"},
		{filepath.Join(parent, "web-1.2.0"), "保留的版本目录"},
		{src, "前端源码目录 web-src"},
	} {
		mustExist(t, p.path, p.what)
	}
}

// 更新器自己的其余命名（web-retired-*、web-initial*）参与清理；别的 web-*
// 名字（备份、手工放的目录）即使超出保留份数也不删。
func TestPruneWebVersions只清自己的命名(t *testing.T) {
	parent := t.TempDir()
	base := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	retired := makeWebDir(t, parent, "web-retired-20261001-080000", base) // 最旧
	makeWebDir(t, parent, "web-1.1.0", base.Add(time.Hour))
	makeWebDir(t, parent, "web-1.2.0", base.Add(2*time.Hour))
	src := makeWebDir(t, parent, "web-src", base.Add(-24*time.Hour))
	backup := makeWebDir(t, parent, "web-backup", base.Add(-12*time.Hour))

	pruneWebVersions(parent, 2) // 可清理的有 3 份，最旧的 web-retired-* 被删

	mustNotExist(t, retired, "最旧的 web-retired-*")
	for _, p := range []struct{ path, what string }{
		{filepath.Join(parent, "web-1.1.0"), "保留的版本目录"},
		{filepath.Join(parent, "web-1.2.0"), "保留的版本目录"},
		{src, "前端源码目录 web-src"},
		{backup, "非更新器创建的目录"},
	} {
		mustExist(t, p.path, p.what)
	}
}
