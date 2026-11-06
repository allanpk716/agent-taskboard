package ops_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/store"
)

// 瘦身移出的附件只留库记录、对象已删除：备份镜像应跳过它们，备份照常成功。
func Test备份镜像跳过已瘦身附件(t *testing.T) {
	target := t.TempDir()
	b, clk, dataDir := newBackups(t, target)
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}
	defer st.Close()
	if _, err := st.DB.Exec(`INSERT INTO tickets (seq, number, kind, title, created_at)
		VALUES (1, '20261016-001', 'task', '瘦身备份工单', ?)`,
		clk.Now().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO attachments
		(ticket_seq, filename, size, state, attached_to, uploaded_at, slim_removed_at, slim_package_name)
		VALUES (1, '已瘦身.txt', 6, 'attached', '说明', ?, ?, 'slim-1.zip')`,
		clk.Now().Format(time.RFC3339Nano), clk.Now().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}

	rec, err := b.BackupNow(context.Background(), ops.KindDaily)
	if err != nil {
		t.Fatalf("有瘦身附件时备份失败：%v", err)
	}
	if !rec.Success() || rec.FilesAdded != 0 {
		t.Fatalf("备份记录 = %+v，想成功且不镜像瘦身附件", rec)
	}
}
