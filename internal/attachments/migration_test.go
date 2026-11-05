package attachments_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/store"
)

func newMigrationDatabase(t *testing.T) (*store.Store, string) {
	t.Helper()
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("打开数据库失败：%v", err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.DB.Exec(`INSERT INTO tickets (seq, number, kind, title, created_at)
		VALUES (1, '20261105-001', 'task', '迁移测试', '2026-11-05T09:00:00+08:00')`); err != nil {
		t.Fatalf("造工单失败：%v", err)
	}
	return st, dataDir
}

func putMigrationAttachment(t *testing.T, storage attachments.Storage, id int64, content string) {
	t.Helper()
	if err := storage.Put(context.Background(), id, strings.NewReader(content),
		int64(len(content)), attachments.PutMeta{ContentType: "text/plain"}); err != nil {
		t.Fatalf("写入源附件 %d 失败：%v", id, err)
	}
}

func addMigrationRecord(t *testing.T, st *store.Store, id int64, filename, content, state string) {
	t.Helper()
	if _, err := st.DB.Exec(`INSERT INTO attachments (id, ticket_seq, filename, size, state, uploaded_at)
		VALUES (?, 1, ?, ?, ?, '2026-11-05T09:00:00+08:00')`,
		id, filename, len(content), state); err != nil {
		t.Fatalf("写入附件记录 %d 失败：%v", id, err)
	}
}

func readAttachment(t *testing.T, storage attachments.Storage, id int64) string {
	t.Helper()
	body, _, err := storage.Open(context.Background(), id)
	if err != nil {
		t.Fatalf("打开附件 %d 失败：%v", id, err)
	}
	defer body.Close()
	content, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("读取附件 %d 失败：%v", id, err)
	}
	return string(content)
}

func Test附件迁移本地到OSS全量后标记与内容正确(t *testing.T) {
	ctx := context.Background()
	st, dataDir := newMigrationDatabase(t)
	if err := attachments.WriteBackendMarker(ctx, st.DB, attachments.BackendMarker{
		Backend: attachments.AttachmentBackendLocal,
	}); err != nil {
		t.Fatalf("写入源后端标记失败：%v", err)
	}
	source, err := attachments.NewLocalStorage(dataDir)
	if err != nil {
		t.Fatalf("创建本地存储失败：%v", err)
	}
	putMigrationAttachment(t, source, 1, "已挂附件")
	putMigrationAttachment(t, source, 2, "待用附件")
	addMigrationRecord(t, st, 1, "已挂.txt", "已挂附件", "attached")
	addMigrationRecord(t, st, 2, "待用.txt", "待用附件", "pending")

	target := attachments.NewMemoryStorage()
	wantMarker := attachments.BackendMarker{
		Backend: attachments.AttachmentBackendOSS, Bucket: "files", Prefix: "agent-taskboard/prod/",
	}
	res, err := attachments.MigrateAttachments(ctx, st.DB, source, target, attachments.MigrationOptions{
		Target: wantMarker,
	})
	if err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	if res.Copied != 2 || res.Skipped != 0 || res.Failed != 0 || res.Missing != 0 {
		t.Fatalf("迁移结果 = %+v, 想 2 个复制且无其他状态", res)
	}
	marker, exists, err := attachments.ReadBackendMarker(ctx, st.DB)
	if err != nil || !exists {
		t.Fatalf("读取迁移后标记 = (%+v, %v, %v)", marker, exists, err)
	}
	if marker != wantMarker {
		t.Fatalf("迁移后标记 = %+v, 想 %+v", marker, wantMarker)
	}
	if got := readAttachment(t, target, 1); got != "已挂附件" {
		t.Fatalf("OSS 附件 1 = %q", got)
	}
	if got := readAttachment(t, target, 2); got != "待用附件" {
		t.Fatalf("OSS 待用附件 2 = %q", got)
	}
	if got := readAttachment(t, source, 1); got != "已挂附件" {
		t.Fatalf("本地源附件 1 迁移后被改动：%q", got)
	}
}
