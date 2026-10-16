// 内置定时调度（#43）：每天 daily_at 执行；错过的时刻在下次检查时补一次；
// 目标连不上时每小时重试一次，直到成功。
package ops_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/store"
)

// 到达每日时刻才备份；错过时刻（服务 03:00 没在跑）下次启动补一次，
// 补过后同一天不再重复。
func Test定时备份在每日时刻执行且错过补一次(t *testing.T) {
	target := t.TempDir()
	b, clk, _ := newBackups(t, target)
	ctx := context.Background()
	clk.Set(beijing(2026, 10, 16, 14, 0, 0)) // 已过今天 03:00

	// 补做今天错过的那一次。
	if !b.CheckSchedule(ctx) {
		t.Fatal("14:00 时今天 03:00 的备份还没做，应当补一次")
	}
	if names := backupFiles(t, target, "daily"); len(names) != 1 {
		t.Fatalf("应补出一份每日备份：%v", names)
	}
	// 补过之后，同一天不再重复。
	if b.CheckSchedule(ctx) {
		t.Fatal("当天的备份已经做过，不应再备")
	}

	// 第二天 02:59 还没到点，不备。
	clk.Set(beijing(2026, 10, 17, 2, 59, 0))
	if b.CheckSchedule(ctx) {
		t.Fatal("02:59 不应触发每日备份")
	}
	// 03:00 到点，备份。
	clk.Set(beijing(2026, 10, 17, 3, 0, 0))
	if !b.CheckSchedule(ctx) {
		t.Fatal("03:00 应触发每日备份")
	}
	names := backupFiles(t, target, "daily")
	if len(names) != 2 || names[0] != "taskboard-20261017-030000-daily.db" {
		t.Fatalf("应有两份每日备份（新→旧）：%v", names)
	}

	// 每月 1 日那份是 monthly。
	clk.Set(beijing(2026, 11, 1, 3, 0, 0))
	if !b.CheckSchedule(ctx) {
		t.Fatal("11-01 03:00 应触发备份")
	}
	if names := backupFiles(t, target, "monthly"); len(names) != 1 {
		t.Fatalf("每月 1 日应是 monthly 备份：%v", names)
	}
}

// 目标连不上时记一次失败，之后每小时重试一次；恢复可达后重试成功。
func Test目标连不上时记失败并每小时重试(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(blocker, "backups") // blocker 是文件，建不出目录
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	clk := clock.NewFake(beijing(2026, 10, 16, 14, 0, 0))
	b, err := ops.NewBackups(st.DB, dataDir, ops.BackupOptions{
		Target: target, DailyAt: "03:00", KeepDaily: 3, KeepMonthly: 2, KeepPreupdate: 2,
	}, clk)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// 首次尝试失败，记一次失败。
	if !b.CheckSchedule(ctx) {
		t.Fatal("首次调度应当尝试备份")
	}
	if b.Status(ctx).LastFailure == nil {
		t.Fatal("目标连不上应记录失败")
	}
	// 半小时后还不到重试时刻。
	clk.Advance(30 * time.Minute)
	if b.CheckSchedule(ctx) {
		t.Fatal("失败后一小时内不应重试")
	}
	// 一小时后再试，仍失败（第二次失败记录）。
	clk.Advance(31 * time.Minute)
	if !b.CheckSchedule(ctx) {
		t.Fatal("一小时后应重试")
	}
	if got := len(b.Status(ctx).Records); got != 2 {
		t.Fatalf("应有两条失败记录，得到 %d", got)
	}
	// 目标恢复可达，下一次重试成功。
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Hour)
	if !b.CheckSchedule(ctx) {
		t.Fatal("目标恢复后应重试成功")
	}
	status := b.Status(ctx)
	if status.LastSuccessAt == nil || status.Overdue {
		t.Fatalf("重试成功后应有最近成功备份：%+v", status)
	}
	if len(status.Records) != 3 {
		t.Fatalf("应有三条记录（两败一成）：%d", len(status.Records))
	}
}

// 调度循环（真实计时器 + 注入时钟）：到点后自动执行，不需要外部触发。
func Test调度循环随时钟推进自动备份(t *testing.T) {
	target := t.TempDir()
	b, clk, _ := newBackups(t, target)
	clk.Set(beijing(2026, 10, 16, 14, 0, 0)) // 今天 03:00 已错过 → 启动即补

	done := make(chan struct{})
	go b.RunSchedule(done, 20*time.Millisecond)
	defer close(done)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if names := backupFiles(t, target, "daily"); len(names) == 1 {
			return // 调度循环自己补出了今天的备份
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("调度循环在 3 秒内没有执行备份")
}
