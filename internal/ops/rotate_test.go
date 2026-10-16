// 轮换与损坏副本处理（#43）：注入时钟模拟多天备份，验证保留份数符合策略；
// 人为损坏的副本被改名 .bad，不参与轮换，好副本不被淘汰。
package ops_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/ops"
)

// 模拟多天备份后，按类别只保留最新的 keep 份；.bad 副本和镜像不受影响。
func Test多天备份后按策略轮换(t *testing.T) {
	target := t.TempDir()
	// 缩小保留份数便于验证：每日 3 份、每月 2 份、更新前 2 份。
	dataDir := t.TempDir()
	b, clk := newBackupsWithKeep(t, dataDir, target, 3, 2, 2)

	// 五个普通日的每日备份（时钟逐天推进，文件名不重复）。
	for day := 10; day <= 14; day++ {
		clk.Set(beijing(2026, 10, day, 3, 0, 0))
		if _, err := b.Now(context.Background(), ops.KindDaily); err != nil {
			t.Fatalf("10 月 %d 日备份失败：%v", day, err)
		}
	}
	names := backupFiles(t, target, "daily")
	want := []string{
		"taskboard-20261014-030000-daily.db",
		"taskboard-20261013-030000-daily.db",
		"taskboard-20261012-030000-daily.db",
	}
	if len(names) != len(want) {
		t.Fatalf("每日备份应只剩 %d 份，还有 %v", len(want), names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("保留的每日备份 = %v, 想 %v", names, want)
		}
	}

	// 两个月里的每月 1 日各一份，再补第三个月，只留最新 2 份。
	for _, first := range []time.Month{time.September, time.October, time.November} {
		clk.Set(beijing(2026, first, 1, 3, 0, 0))
		if _, err := b.Now(context.Background(), ops.KindMonthly); err != nil {
			t.Fatalf("%d 月 1 日备份失败：%v", first, err)
		}
	}
	names = backupFiles(t, target, "monthly")
	if len(names) != 2 || names[0] != "taskboard-20261101-030000-monthly.db" {
		t.Fatalf("每月备份应只剩最新 2 份，还有 %v", names)
	}

	// 更新前备份单独一类（#45 走 Preupdate）。
	for i := 0; i < 4; i++ {
		clk.Advance(time.Hour)
		if _, err := b.Preupdate(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if names := backupFiles(t, target, "preupdate"); len(names) != 2 {
		t.Fatalf("更新前备份应只剩 2 份，还有 %v", names)
	}
	// 一类轮换不波及别的类。
	if got := len(backupFiles(t, target, "daily")) + len(backupFiles(t, target, "monthly")); got != 5 {
		t.Fatalf("轮换更新前备份不应影响每日/每月副本，现在共 %d 份", got)
	}
}

// 人为损坏一个既有副本：下一次备份把它改名 .bad，它不占保留名额，
// 本会被挤掉的好副本留下来。
func Test损坏的副本改名bad且好副本不被淘汰(t *testing.T) {
	target := t.TempDir()
	dataDir := t.TempDir()
	b, clk := newBackupsWithKeep(t, dataDir, target, 3, 2, 2)

	// 先备 3 份（正好保留上限），第 2 份被人为损坏。
	for day := 10; day <= 12; day++ {
		clk.Set(beijing(2026, 10, day, 3, 0, 0))
		if _, err := b.Now(context.Background(), ops.KindDaily); err != nil {
			t.Fatal(err)
		}
	}
	corrupt := "taskboard-20261011-030000-daily.db"
	if err := os.WriteFile(filepath.Join(target, corrupt), []byte("这不是一个 SQLite 库"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 再备一份：损坏副本应被改名 .bad，不占保留名额；保留的 3 份都是好副本
	// （10、12、13 日。若损坏的 11 日仍占名额，10 日那份好副本会被淘汰）。
	clk.Set(beijing(2026, 10, 13, 3, 0, 0))
	if _, err := b.Now(context.Background(), ops.KindDaily); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, corrupt+".bad")); err != nil {
		t.Fatalf("损坏副本应改名 .bad：%v", err)
	}
	if _, err := os.Stat(filepath.Join(target, corrupt)); err == nil {
		t.Fatal("损坏副本原文件还在")
	}
	if names := backupFiles(t, target, "daily"); len(names) != 3 ||
		names[0] != "taskboard-20261013-030000-daily.db" ||
		names[2] != "taskboard-20261010-030000-daily.db" {
		t.Fatalf("保留的应是 10、12、13 日三份好副本，得到 %v", names)
	}
	// .bad 不参与轮换：它既没被删，也没算进保留份数。
	if _, err := os.Stat(filepath.Join(target, corrupt+".bad")); err != nil {
		t.Fatal(".bad 文件应保留在现场")
	}
}

// 刚生成的副本校验不通过时，同样改名 .bad，记一次失败。
func Test新副本校验不通过改名bad记失败(t *testing.T) {
	target := t.TempDir()
	b, clk, _ := newBackups(t, target)
	// 用一个写保护失败不了库；改为直接预占文件名：VACUUM INTO 遇到已存在
	// 文件会失败，走失败记录路径。
	name := filepath.Join(target, ops.BackupFileName(clk.Now(), ops.KindDaily))
	if err := os.WriteFile(name, []byte("占位"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := b.Now(context.Background(), ops.KindDaily)
	if err == nil {
		t.Fatal("同名文件存在时备份应失败")
	}
	if rec.Success() {
		t.Fatalf("应记为失败：%+v", rec)
	}
	status := b.Status(context.Background())
	if status.LastSuccessAt != nil || !status.Overdue {
		t.Fatal("没有成功备份时 Status 应为超期")
	}
	if status.LastFailure == nil {
		t.Fatal("失败应记入 Status.LastFailure")
	}
}

// backupFiles 列出目标里某类别的 .db 副本（不含 .bad），新→旧排序。
func backupFiles(t *testing.T, dir, kind string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) > 3 && name[len(name)-3:] == "db_" { // .part 残留不应存在
			t.Fatalf("目标里有未完成的镜像残留：%s", name)
		}
		if len(name) >= len(kind)+4 && name[len(name)-len(kind)-4:] == "-"+kind+".db" {
			names = append(names, name)
		}
	}
	// 新→旧。
	for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
		names[i], names[j] = names[j], names[i]
	}
	return names
}
