// 运维模块（#43 备份与恢复）的黑盒测试：只观察磁盘上的备份文件、镜像目录
// 和对外结构（Record / Status），不碰内部表。定时、轮换、恢复见同包其他文件。
package ops_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/store"
	_ "modernc.org/sqlite"
)

// newBackups 在临时 data_dir 上开一个真实库并建备份管理器；target 为空时
// 用默认（data_dir\backups\）。
func newBackups(t *testing.T, target string) (*ops.Backups, *clock.Fake, string) {
	t.Helper()
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("打开数据库失败：%v", err)
	}
	t.Cleanup(func() { st.Close() })
	opts := ops.BackupOptions{DailyAt: "03:00", KeepDaily: 30, KeepMonthly: 12, KeepPreupdate: 5}
	if target != "" {
		opts.Target = target
	}
	clk := clock.NewFake(beijing(2026, 10, 16, 3, 0, 5))
	b, err := ops.NewBackups(st.DB, dataDir, opts, clk)
	if err != nil {
		t.Fatalf("建备份管理器失败：%v", err)
	}
	return b, clk, dataDir
}

func beijing(y int, mo time.Month, d, h, mi, s int) time.Time {
	return time.Date(y, mo, d, h, mi, s, 0, clock.Beijing)
}

// newBackupsWithKeep 用指定的保留份数建备份管理器（轮换测试用）。
func newBackupsWithKeep(t *testing.T, dataDir, target string, keepDaily, keepMonthly, keepPreupdate int) (*ops.Backups, *clock.Fake) {
	t.Helper()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("打开数据库失败：%v", err)
	}
	t.Cleanup(func() { st.Close() })
	clk := clock.NewFake(beijing(2026, 10, 16, 3, 0, 5))
	b, err := ops.NewBackups(st.DB, dataDir, ops.BackupOptions{
		Target: target, DailyAt: "03:00",
		KeepDaily: keepDaily, KeepMonthly: keepMonthly, KeepPreupdate: keepPreupdate,
	}, clk)
	if err != nil {
		t.Fatalf("建备份管理器失败：%v", err)
	}
	return b, clk
}

// 备份文件命名正确（taskboard-YYYYMMDD-HHMMSS-<kind>.db），副本是库的真实
// 快照并通过完整性校验，结果（时间、大小、是否通过、附件新增文件数）写库。
func Test备份文件命名正确并通过完整性校验(t *testing.T) {
	target := t.TempDir()
	b, clk, _ := newBackups(t, target)

	rec, err := b.Now(context.Background(), ops.KindDaily)
	if err != nil {
		t.Fatalf("备份失败：%v", err)
	}
	name := "taskboard-20261016-030005-daily.db"
	if rec.Filename != name {
		t.Fatalf("文件名 = %q, 想 %q", rec.Filename, name)
	}
	if rec.At != clk.Now() {
		t.Fatalf("记录时间 %v 不等于备份时刻 %v", rec.At, clk.Now())
	}
	if !rec.IntegrityOK || !rec.Success() {
		t.Fatalf("副本应通过完整性校验：%+v", rec)
	}
	if rec.Size <= 0 {
		t.Fatalf("副本大小应大于 0：%d", rec.Size)
	}
	// 副本是库的真实快照：里面有迁移建出的表。
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(target, name))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'tickets'`).Scan(&n); err != nil {
		t.Fatalf("读副本失败：%v", err)
	}
	if n != 1 {
		t.Fatal("副本里没有 tickets 表，不是库的快照")
	}
	// 结果写库：Status 里能看到这一条。
	status := b.Status(context.Background())
	if status.LastSuccessAt == nil || !status.LastSuccessAt.Equal(clk.Now()) {
		t.Fatalf("最近一次成功备份时间 = %v, 想 %v", status.LastSuccessAt, clk.Now())
	}
	if status.Overdue {
		t.Fatal("刚备份完不应超过 36 小时")
	}
	if len(status.Records) != 1 || status.Records[0].Filename != name {
		t.Fatalf("记录里应有这一条备份：%+v", status.Records)
	}
}

// 每月 1 日的备份标记为 monthly，其余为 daily（KindForDate 按北京时间判）。
func Test每月一日的那份备份是monthly(t *testing.T) {
	if got := ops.KindForDate(beijing(2026, 11, 1, 3, 0, 0)); got != ops.KindMonthly {
		t.Fatalf("1 日 = %q, 想 monthly", got)
	}
	if got := ops.KindForDate(beijing(2026, 11, 2, 3, 0, 0)); got != ops.KindDaily {
		t.Fatalf("2 日 = %q, 想 daily", got)
	}
	target := t.TempDir()
	b, clk, _ := newBackups(t, target)
	clk.Set(beijing(2026, 11, 1, 3, 0, 5))
	rec, err := b.Now(context.Background(), ops.KindMonthly)
	if err != nil {
		t.Fatalf("备份失败：%v", err)
	}
	if rec.Filename != "taskboard-20261101-030005-monthly.db" {
		t.Fatalf("文件名 = %q", rec.Filename)
	}
}

// 附件做增量镜像，只增不删：新增文件按次统计，主存储删掉的文件镜像仍保留。
func Test附件镜像只增不删并统计新增文件数(t *testing.T) {
	target := t.TempDir()
	b, clk, dataDir := newBackups(t, target)
	atts := filepath.Join(dataDir, "attachments")
	writeFile := func(name, content string) {
		t.Helper()
		if err := os.MkdirAll(atts, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(atts, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeFile("1", "one")
	writeFile("2", "two")
	rec, err := b.Now(context.Background(), ops.KindDaily)
	if err != nil {
		t.Fatal(err)
	}
	if rec.FilesAdded != 2 {
		t.Fatalf("第一次镜像应新增 2 个文件，得到 %d", rec.FilesAdded)
	}
	mirror := filepath.Join(target, "attachments")
	if read(t, filepath.Join(mirror, "1")) != "one" || read(t, filepath.Join(mirror, "2")) != "two" {
		t.Fatal("镜像里应有附件 1 和 2 的内容")
	}

	// 第二次只同步新文件。
	writeFile("3", "three")
	clk.Advance(time.Hour)
	rec, err = b.Now(context.Background(), ops.KindDaily)
	if err != nil {
		t.Fatal(err)
	}
	if rec.FilesAdded != 1 {
		t.Fatalf("第二次镜像应只新增 1 个文件，得到 %d", rec.FilesAdded)
	}

	// 主存储删除后镜像不删（只增不删；未引用附件的清除是主存储的事）。
	if err := os.Remove(filepath.Join(atts, "1")); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Hour)
	if _, err := b.Now(context.Background(), ops.KindDaily); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(mirror, "1")) != "one" {
		t.Fatal("镜像不应删除主存储里已不存在的附件")
	}
}

// 未配置备份目标时备份存到 data_dir\backups\，并在 Status 里标注“备份未离开本机”
// （local_only；管理页据此提示）。
func Test未配置目标时备份存数据目录并标注未离开本机(t *testing.T) {
	b, _, dataDir := newBackups(t, "")
	if _, err := b.Now(context.Background(), ops.KindDaily); err != nil {
		t.Fatal(err)
	}
	if got := b.TargetDir(); got != filepath.Join(dataDir, "backups") {
		t.Fatalf("未配置目标时应落到 data_dir\\backups\\，得到 %q", got)
	}
	name := filepath.Join(dataDir, "backups", "taskboard-20261016-030005-daily.db")
	if _, err := os.Stat(name); err != nil {
		t.Fatalf("默认目标里没有备份文件：%v", err)
	}
	status := b.Status(context.Background())
	if !status.LocalOnly {
		t.Fatal("未配置目标时 Status 应标注 local_only")
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s 失败：%v", path, err)
	}
	return string(b)
}
