package ops_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/notifications"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/store"
)

func Test写目标失败时本机兜底且同时段只兜底一次(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(blocker, "backups")
	b, clk, dataDir := newBackups(t, target)
	ctx := context.Background()

	rec, err := b.BackupNow(ctx, ops.KindDaily)
	if err != nil {
		t.Fatalf("兜底成功时不应返回错误：%v", err)
	}
	if rec.Location != ops.LocationLocal || rec.Err == "" || !rec.IntegrityOK {
		t.Fatalf("兜底记录 = %+v", rec)
	}
	local := filepath.Join(dataDir, "backups", rec.Filename)
	if _, err := os.Stat(local); err != nil {
		t.Fatalf("本机兜底副本不存在：%v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "backups", "attachments")); !os.IsNotExist(err) {
		t.Fatalf("兜底期间不应做附件镜像：%v", err)
	}
	status := b.Status(ctx)
	if status.LastSuccessAt != nil || !status.Overdue || !status.TargetUnreachable || status.LastLocalFallbackAt == nil {
		t.Fatalf("兜底后状态 = %+v", status)
	}

	clk.Advance(time.Hour)
	rec, err = b.BackupNow(ctx, ops.KindDaily)
	if err == nil || rec.Location != ops.LocationTarget || rec.Filename != "" {
		t.Fatalf("同一时段重试应只试目标：%+v, err=%v", rec, err)
	}
	entries, err := os.ReadDir(filepath.Join(dataDir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	dbFiles := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".db" {
			dbFiles++
		}
	}
	if dbFiles != 1 {
		t.Fatalf("同一时段应只有一份本机兜底，得到 %d 份 .db", dbFiles)
	}

	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Hour)
	rec, err = b.BackupNow(ctx, ops.KindDaily)
	if err != nil || rec.Location != ops.LocationTarget || !rec.Success() {
		t.Fatalf("目标恢复后应写到目标：%+v, err=%v", rec, err)
	}
	status = b.Status(ctx)
	if status.LastSuccessAt == nil || status.Overdue || status.TargetUnreachable {
		t.Fatalf("目标恢复后的状态 = %+v", status)
	}
	if status.LastLocalFallbackAt == nil {
		t.Fatal("最近一次本机兜底时间应保留")
	}
}

func Test目标和本机兜底都失败时错误包含两个原因(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(blocker, "backups")
	b, _, dataDir := newBackups(t, target)
	name := ops.BackupFileName(beijing(2026, 10, 16, 3, 0, 5), ops.KindDaily)
	if err := os.MkdirAll(filepath.Join(dataDir, "backups", name), 0o755); err != nil {
		t.Fatal(err)
	}

	rec, err := b.BackupNow(context.Background(), ops.KindDaily)
	if err == nil {
		t.Fatal("目标和本机兜底都失败时应返回错误")
	}
	msg := err.Error()
	if !strings.Contains(msg, "备份目标不可达") || !strings.Contains(msg, target) {
		t.Fatalf("错误缺少目标失败原因：%v", err)
	}
	if !strings.Contains(msg, "本机兜底也失败") || !strings.Contains(msg, name) {
		t.Fatalf("错误缺少本机兜底失败原因：%v", err)
	}
	if rec.Location != ops.LocationTarget || rec.Filename == "" || rec.IntegrityOK {
		t.Fatalf("双方失败记录 = %+v", rec)
	}
}

func Test快照失败时不做本机兜底(t *testing.T) {
	target := t.TempDir()
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	db := st.DB
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(beijing(2026, 10, 16, 3, 0, 5))
	b, err := ops.NewBackups(db, dataDir, ops.BackupOptions{Target: target, DailyAt: "03:00"}, clk)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := b.BackupNow(context.Background(), ops.KindDaily)
	if err == nil || rec.Location != ops.LocationTarget || rec.IntegrityOK {
		t.Fatalf("快照失败应普通失败且不兜底：%+v, err=%v", rec, err)
	}
	entries, err := os.ReadDir(filepath.Join(dataDir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".db" {
			t.Fatalf("快照失败不应留下兜底副本：%s", e.Name())
		}
	}
}

func Test备份告警第一次二十四小时提醒恢复并跨越服务重启(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(blocker, "backups")
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	clk := clock.NewFake(beijing(2026, 10, 16, 3, 0, 0))
	if _, err := st.DB.Exec(`INSERT INTO members (username,name,password_hash,is_admin,created_at)
		VALUES ('admin','管理员','x',1,?)`, clk.Now().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	newPair := func() (*ops.Backups, *notifications.Manager) {
		b, err := ops.NewBackups(st.DB, dataDir, ops.BackupOptions{Target: target, DailyAt: "03:00"}, clk)
		if err != nil {
			t.Fatal(err)
		}
		n := notifications.NewManager(st.DB, clk)
		b.SetNotifier(n)
		return b, n
	}
	ctx := context.Background()
	b, n := newPair()
	if _, err := b.Preupdate(ctx); err != nil {
		t.Fatalf("更新前备份兜底成功应可继续：%v", err)
	}
	if got := len(notificationList(t, n)); got != 0 {
		t.Fatalf("更新前备份失败不应告警，通知数 = %d", got)
	}
	b, _ = newPair()
	if _, err := b.BackupNow(ctx, ops.KindDaily); err != nil {
		t.Fatal(err)
	}
	list := notificationList(t, n)
	if len(list) != 1 || list[0].Reason != notifications.ReasonBackupFailing || list[0].ReadAt != nil ||
		list[0].URL != "/admin#backup" || list[0].Number != "" {
		t.Fatalf("第一次告警 = %+v", list)
	}

	// 模拟服务重启：状态完全从库和通知表重建，一小时内重试不重复告警。
	b, n = newPair()
	clk.Advance(time.Hour)
	if _, err := b.BackupNow(ctx, ops.KindDaily); err == nil {
		t.Fatal("目标仍不可达，重试应失败")
	}
	if got := len(notificationList(t, n)); got != 1 {
		t.Fatalf("服务重启后不应重复首告警，通知数 = %d", got)
	}

	clk.Advance(24 * time.Hour)
	if _, err := b.BackupNow(ctx, ops.KindDaily); err != nil {
		t.Fatalf("下一个每日时段应再次本机兜底：%v", err)
	}
	list = notificationList(t, n)
	if len(list) != 2 || list[0].Reason != notifications.ReasonBackupFailing || list[0].Text != "备份仍未恢复，已持续 25 小时" {
		t.Fatalf("24 小时提醒 = %+v", list)
	}

	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Hour)
	if _, err := b.BackupNow(ctx, ops.KindDaily); err != nil {
		t.Fatal(err)
	}
	list = notificationList(t, n)
	if len(list) != 3 || list[0].Reason != notifications.ReasonBackupRecovered || list[0].ReadAt != nil {
		t.Fatalf("恢复通知 = %+v", list)
	}
	if list[1].ReadAt == nil || list[2].ReadAt == nil {
		t.Fatalf("旧告警应标已读：%+v", list[1:])
	}
}

func notificationList(t *testing.T, n *notifications.Manager) []notifications.Item {
	t.Helper()
	out, err := n.List(context.Background(), notifications.ListQuery{MemberID: 1, Limit: 10, IncludeSystem: true})
	if err != nil {
		t.Fatal(err)
	}
	return out.Items
}
