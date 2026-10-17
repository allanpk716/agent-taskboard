// 恢复（#43）：taskboard restore <备份文件> 依次执行：确认服务已停止 → 把当前
// 库另存一份 → 换成指定的备份 → 从镜像中补齐附件 → 提示启动服务。
package ops_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/store"
)

// 恢复后数据库和附件都从备份与镜像回来：库换成副本，主存储里缺的附件从
// 备份旁边的镜像目录补齐。恢复在服务停止后进行（库文件不被占用）。
func Test恢复换库并从镜像补齐附件(t *testing.T) {
	target := t.TempDir()
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(beijing(2026, 10, 16, 3, 0, 5))
	b, err := ops.NewBackups(st.DB, dataDir, ops.BackupOptions{
		Target: target, DailyAt: "03:00", KeepDaily: 30, KeepMonthly: 12, KeepPreupdate: 5,
	}, clk)
	if err != nil {
		t.Fatal(err)
	}
	// 造两张工单和两个附件（备份镜像里会有附件）：元数据入库，文件落盘。
	for seq, number := range map[int]string{1: "20261016-001", 2: "20261016-002"} {
		if _, err := st.DB.Exec(`INSERT INTO tickets (seq, number, kind, title, created_at)
			VALUES (?, ?, 'task', ?, ?)`, seq, number, "工单"+number,
			clk.Now().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	atts := filepath.Join(dataDir, "attachments")
	if err := os.MkdirAll(atts, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"1", "2"} {
		if _, err := st.DB.Exec(`INSERT INTO attachments
			(ticket_seq, filename, size, state, attached_to, uploaded_at, claimed_at)
			VALUES (1, ?, 4, 'attached', '说明', ?, ?)`, id+".txt",
			clk.Now().Format(time.RFC3339Nano), clk.Now().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(atts, id), []byte("附件"+id), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.BackupNow(context.Background(), ops.KindDaily); err != nil {
		t.Fatal(err)
	}
	st.Close() // 服务停止，释放库文件
	backup := filepath.Join(target, "taskboard-20261016-030005-daily.db")

	// 灾难：库和附件都没了。
	os.Remove(filepath.Join(dataDir, "taskboard.db"))
	os.Remove(filepath.Join(atts, "1"))

	res, err := ops.Restore(dataDir, 0, backup, time.Now())
	if err != nil {
		t.Fatalf("恢复失败：%v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "taskboard.db")); err != nil {
		t.Fatal("恢复后没有库文件")
	}
	if got := readFile(t, filepath.Join(atts, "1")); got != "附件1" {
		t.Fatalf("附件 1 应从镜像补回，得到 %q", got)
	}
	if res.AttachmentsRestored != 1 {
		t.Fatalf("应补回 1 个附件，得到 %d", res.AttachmentsRestored)
	}

	// 恢复后的库能正常打开（迁移幂等），工单数一致。
	st2, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("恢复后的库打不开：%v", err)
	}
	defer st2.Close()
	var tickets int
	if err := st2.DB.QueryRow(`SELECT COUNT(*) FROM tickets`).Scan(&tickets); err != nil || tickets != 2 {
		t.Fatalf("恢复后应有 2 张工单，得到 %d（err %v）", tickets, err)
	}
}

// 服务还在运行时拒绝恢复：对运行中的库做热替换不安全（Windows 下文件也被占用）。
func Test服务运行时拒绝恢复(t *testing.T) {
	target := t.TempDir()
	b, _, dataDir := newBackups(t, target)
	if _, err := b.BackupNow(context.Background(), ops.KindDaily); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(target, "taskboard-20261016-030005-daily.db")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	if _, err := ops.Restore(dataDir, port, backup, time.Now()); err == nil {
		t.Fatal("端口上有服务时应拒绝恢复")
	} else if !strings.Contains(err.Error(), "停止") {
		t.Fatalf("错误应提示先停止服务：%v", err)
	}
	// 库没有被换掉。
	if _, err := os.Stat(filepath.Join(dataDir, "taskboard.db")); err != nil {
		t.Fatal("拒绝恢复时不应动到当前库")
	}
}

// 拒绝恢复 .bad 副本和校验不通过的文件。
func Test拒绝恢复损坏的备份文件(t *testing.T) {
	target := t.TempDir()
	b, _, dataDir := newBackups(t, target)
	if _, err := b.BackupNow(context.Background(), ops.KindDaily); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(target, "taskboard-20261016-030005-daily.db")
	bad := good + ".bad"
	if err := os.WriteFile(bad, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ops.Restore(dataDir, 0, bad, time.Now()); err == nil {
		t.Fatal(".bad 副本应拒绝恢复")
	}
	// 一个不存在的文件。
	if _, err := ops.Restore(dataDir, 0, filepath.Join(target, "taskboard-20990101-000000-daily.db"), time.Now()); err == nil {
		t.Fatal("不存在的备份文件应报错")
	}
	// 校验不通过的普通文件。
	junk := filepath.Join(target, "junk.db")
	if err := os.WriteFile(junk, []byte("不是库"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ops.Restore(dataDir, 0, junk, time.Now()); err == nil {
		t.Fatal("校验不通过的文件应拒绝恢复")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// 在新机器上恢复（VM 损坏后的正路）：data_dir 还不存在，恢复要自己建出来，
// 不能因为目录不在就失败（#46 恢复演练时踩到）。
func Test恢复到不存在的数据目录(t *testing.T) {
	b, _, _ := newBackups(t, t.TempDir())
	rec, err := b.BackupNow(context.Background(), ops.KindDaily)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(b.TargetDir(), rec.Filename)

	fresh := filepath.Join(t.TempDir(), "new-machine", "data")
	if _, err := ops.Restore(fresh, 0, backup, time.Now()); err != nil {
		t.Fatalf("恢复到新目录失败：%v", err)
	}
	st, err := store.Open(fresh)
	if err != nil {
		t.Fatalf("恢复后的库打不开：%v", err)
	}
	st.Close()
}
