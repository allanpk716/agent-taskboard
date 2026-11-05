package attachments_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/store"
)

func newUploadManager(t *testing.T, mem *attachments.MemoryStorage) (*attachments.Manager, *store.Store) {
	t.Helper()
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("打开数据库失败：%v", err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.DB.Exec(`INSERT INTO tickets (seq, number, kind, title, created_at)
		VALUES (1, '20261016-001', 'task', '测试工单', '2026-10-16T09:00:00+08:00')`); err != nil {
		t.Fatalf("造工单失败：%v", err)
	}
	if _, err := st.DB.Exec(`INSERT INTO members (id, username, name, password_hash, created_at)
		VALUES (1, 'chen', '陈工', 'x', '2026-10-16T09:00:00+08:00')`); err != nil {
		t.Fatalf("造成员失败：%v", err)
	}
	m, err := attachments.NewManagerWithStorage(st.DB, dataDir, mem,
		clock.NewFake(time.Date(2026, 10, 16, 9, 0, 0, 0, clock.Beijing)), attachments.LimitsOf(5, 5, 10, 50))
	if err != nil {
		t.Fatalf("建附件管理器失败：%v", err)
	}
	return m, st
}

// 上传走流式：内容交给存储接口，元数据入库，返回值与旧接口一致。
func TestUpload流式写进存储接口(t *testing.T) {
	mem := attachments.NewMemoryStorage()
	m, _ := newUploadManager(t, mem)
	content := "流式上传的附件内容"
	meta := attachments.PutMeta{ContentType: "text/plain; charset=utf-8"}

	info, markdown, err := m.UploadStream(context.Background(), 1, "报告.txt", int64(len(content)),
		strings.NewReader(content), meta, attachments.Principal{MemberID: 1})
	if err != nil {
		t.Fatalf("Upload 失败：%v", err)
	}
	if info.ID != 1 || info.Size != int64(len(content)) || info.State != "pending" {
		t.Fatalf("返回的附件信息不对：%+v", info)
	}
	if markdown != "[报告.txt](att:att_1)" {
		t.Fatalf("markdown = %q", markdown)
	}
	obj, ok := mem.Object(1)
	if !ok {
		t.Fatal("对象应写进存储接口")
	}
	if obj.Size != int64(len(content)) || string(obj.Content) != content || obj.ContentType != meta.ContentType {
		t.Fatalf("存储里的对象不对：%+v", obj)
	}
	got, err := m.Get(context.Background(), "att_1")
	if err != nil || got.Filename != "报告.txt" {
		t.Fatalf("数据库行缺失：got=%+v err=%v", got, err)
	}
}

// Put 失败时回滚：数据库不留行，存储不留对象。
func TestUploadPut失败回滚不留行与对象(t *testing.T) {
	mem := attachments.NewMemoryStorage()
	mem.PutErr = errors.New("模拟存储故障")
	m, st := newUploadManager(t, mem)
	content := "写不进去的内容"

	_, _, err := m.UploadStream(context.Background(), 1, "失败.txt", int64(len(content)),
		strings.NewReader(content), attachments.PutMeta{}, attachments.Principal{MemberID: 1})
	if err == nil {
		t.Fatal("Put 失败应返回错误")
	}
	if ids := mem.IDs(); len(ids) != 0 {
		t.Fatalf("失败的 Put 不应留对象：%v", ids)
	}
	var count int
	if err := st.DB.QueryRow("SELECT COUNT(*) FROM attachments").Scan(&count); err != nil {
		t.Fatalf("查附件行失败：%v", err)
	}
	if count != 0 {
		t.Fatalf("Put 失败后数据库不应留行：count = %d", count)
	}
}

// 提交失败时删除已写入的对象，不留孤儿。
func TestUpload提交失败删除对象(t *testing.T) {
	mem := attachments.NewMemoryStorage()
	ctx, cancel := context.WithCancel(context.Background())
	mem.AfterPut = func(id int64) { cancel() }
	defer cancel()
	m, _ := newUploadManager(t, mem)
	content := "提交不上的内容"

	_, _, err := m.UploadStream(ctx, 1, "提交失败.txt", int64(len(content)),
		strings.NewReader(content), attachments.PutMeta{}, attachments.Principal{MemberID: 1})
	if err == nil {
		t.Fatal("提交失败应返回错误")
	}
	if ids := mem.IDs(); len(ids) != 0 {
		t.Fatalf("提交失败后对象应被删除：%v", ids)
	}
}
