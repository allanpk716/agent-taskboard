package attachments_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
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

func Test附件迁移OSS到本地前检查磁盘剩余空间(t *testing.T) {
	ctx := context.Background()
	st, dataDir := newMigrationDatabase(t)
	if err := attachments.WriteBackendMarker(ctx, st.DB, attachments.BackendMarker{
		Backend: attachments.AttachmentBackendOSS, Bucket: "files", Prefix: "agent-taskboard/prod/",
	}); err != nil {
		t.Fatalf("写入源后端标记失败：%v", err)
	}
	source := attachments.NewMemoryStorage()
	putMigrationAttachment(t, source, 1, strings.Repeat("a", 10))
	putMigrationAttachment(t, source, 2, strings.Repeat("b", 20))
	addMigrationRecord(t, st, 1, "一.txt", strings.Repeat("a", 10), "attached")
	addMigrationRecord(t, st, 2, "二.txt", strings.Repeat("b", 20), "pending")
	target, err := attachments.NewLocalStorage(dataDir)
	if err != nil {
		t.Fatalf("创建本地存储失败：%v", err)
	}

	var checkedPath string
	_, err = attachments.MigrateAttachments(ctx, st.DB, source, target, attachments.MigrationOptions{
		Target:  attachments.BackendMarker{Backend: attachments.AttachmentBackendLocal},
		DataDir: dataDir,
		FreeSpace: func(path string) (uint64, error) {
			checkedPath = path
			return 32, nil // 30 字节附件加 10% 余量后需要 33 字节。
		},
	})
	if err == nil || !strings.Contains(err.Error(), "磁盘剩余空间不足") {
		t.Fatalf("空间不足 err = %v, 想明确拒绝", err)
	}
	if checkedPath != filepath.Join(dataDir, "attachments") {
		t.Fatalf("检查目录 = %q, 想附件目录", checkedPath)
	}
	if _, ok, err := target.Stat(ctx, 1); err != nil || ok {
		t.Fatal("空间不足时不应开始复制")
	}
	marker, exists, err := attachments.ReadBackendMarker(ctx, st.DB)
	if err != nil || !exists || marker.Backend != attachments.AttachmentBackendOSS {
		t.Fatalf("失败后标记 = (%+v, %v, %v), 想保持 OSS", marker, exists, err)
	}
}

func Test附件迁移接受源端缺失时切换并列出清单(t *testing.T) {
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
	putMigrationAttachment(t, source, 1, "存在")
	addMigrationRecord(t, st, 1, "一.txt", "存在", "attached")
	addMigrationRecord(t, st, 2, "二.txt", "缺失", "pending")
	target := attachments.NewMemoryStorage()

	res, err := attachments.MigrateAttachments(ctx, st.DB, source, target, attachments.MigrationOptions{
		Target: attachments.BackendMarker{
			Backend: attachments.AttachmentBackendOSS, Bucket: "files", Prefix: "agent-taskboard/prod/",
		},
		AcceptMissing: true,
	})
	if err != nil {
		t.Fatalf("接受缺失时迁移失败：%v", err)
	}
	if res.Copied != 1 || res.Missing != 1 || len(res.MissingIDs) != 1 || res.MissingIDs[0] != 2 {
		t.Fatalf("迁移结果 = %+v, 想 1 个复制并列出缺失 2", res)
	}
	marker, exists, err := attachments.ReadBackendMarker(ctx, st.DB)
	if err != nil || !exists || marker.Backend != attachments.AttachmentBackendOSS {
		t.Fatalf("迁移后标记 = (%+v, %v, %v), 想 OSS", marker, exists, err)
	}
	if got := readAttachment(t, target, 1); got != "存在" {
		t.Fatalf("目标附件 = %q, 想存在", got)
	}
}

// 瘦身移出的附件对象已从源端删除，属于正常状态：迁移不应把它们算成缺失。
func Test附件迁移跳过已瘦身附件(t *testing.T) {
	ctx := context.Background()
	st, dataDir := newMigrationDatabase(t)
	source, err := attachments.NewLocalStorage(dataDir)
	if err != nil {
		t.Fatalf("创建本地存储失败：%v", err)
	}
	putMigrationAttachment(t, source, 1, "普通附件")
	addMigrationRecord(t, st, 1, "普通.txt", "普通附件", "attached")
	addMigrationRecord(t, st, 2, "已瘦身.txt", "已瘦身附件", "attached")
	if _, err := st.DB.Exec(`UPDATE attachments
		SET slim_removed_at = '2026-11-01T10:00:00+08:00', slim_package_name = 'slim-1.zip'
		WHERE id = 2`); err != nil {
		t.Fatalf("标记瘦身附件失败：%v", err)
	}
	target := attachments.NewMemoryStorage()

	res, err := attachments.MigrateAttachments(ctx, st.DB, source, target, attachments.MigrationOptions{
		Target: attachments.BackendMarker{Backend: attachments.AttachmentBackendOSS},
	})
	if err != nil {
		t.Fatalf("有瘦身附件时迁移失败：%v", err)
	}
	if res.Missing != 0 || len(res.MissingIDs) != 0 {
		t.Fatalf("迁移结果 = %+v，瘦身附件不应算缺失", res)
	}
	if res.Copied != 1 {
		t.Fatalf("迁移结果 = %+v，想只复制 1 个普通附件", res)
	}
	if _, exists := target.Object(2); exists {
		t.Fatal("瘦身附件没有源端对象，不应出现在目标端")
	}
}

func Test附件迁移DryRun只列计划不写入(t *testing.T) {
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
	putMigrationAttachment(t, source, 1, "一致")
	putMigrationAttachment(t, source, 2, "待复制")
	putMigrationAttachment(t, source, 99, "源端孤儿")
	addMigrationRecord(t, st, 1, "一.txt", "一致", "attached")
	addMigrationRecord(t, st, 2, "二.txt", "待复制", "pending")
	addMigrationRecord(t, st, 3, "三.txt", "缺失", "attached")
	target := attachments.NewMemoryStorage()
	putMigrationAttachment(t, target, 1, "一致")

	res, err := attachments.MigrateAttachments(ctx, st.DB, source, target, attachments.MigrationOptions{
		Target: attachments.BackendMarker{
			Backend: attachments.AttachmentBackendOSS, Bucket: "files", Prefix: "agent-taskboard/prod/",
		},
		DryRun: true,
	})
	if err == nil || !strings.Contains(err.Error(), "源端缺失") {
		t.Fatalf("dry-run err = %v, 想提示后续迁移会因缺失失败", err)
	}
	if res.DryRun != true || res.Planned != 1 || res.Skipped != 1 || res.Missing != 1 ||
		len(res.MissingIDs) != 1 || res.MissingIDs[0] != 3 {
		t.Fatalf("dry-run 计划 = %+v, 想 1 个待复制、1 个已一致、1 个缺失", res)
	}
	if res.TotalSize != int64(len("一致")+len("待复制")+len("缺失")) {
		t.Fatalf("dry-run 总大小 = %d", res.TotalSize)
	}
	if len(res.SourceOnlyIDs) != 1 || res.SourceOnlyIDs[0] != 99 {
		t.Fatalf("源端孤儿 = %v, 想 [99]", res.SourceOnlyIDs)
	}
	if _, ok, err := target.Stat(ctx, 2); err != nil || ok {
		t.Fatal("dry-run 不应写目标对象")
	}
	marker, exists, err := attachments.ReadBackendMarker(ctx, st.DB)
	if err != nil || !exists || marker.Backend != attachments.AttachmentBackendLocal {
		t.Fatalf("dry-run 后标记 = (%+v, %v, %v), 想保持 local", marker, exists, err)
	}
}

func Test附件迁移前检查服务是否仍在运行(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听临时端口失败：%v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	err = attachments.EnsureServiceStopped(port)
	if err == nil || !strings.Contains(err.Error(), "仍有服务在监听") {
		t.Fatalf("服务运行时 err = %v, 想拒绝迁移", err)
	}
	if err := attachments.EnsureServiceStopped(0); err != nil {
		t.Fatalf("未检查端口时 err = %v, 想 nil", err)
	}
}

func Test附件迁移按五十个文件汇报进度(t *testing.T) {
	ctx := context.Background()
	st, _ := newMigrationDatabase(t)
	if err := attachments.WriteBackendMarker(ctx, st.DB, attachments.BackendMarker{
		Backend: attachments.AttachmentBackendLocal,
	}); err != nil {
		t.Fatalf("写入源后端标记失败：%v", err)
	}
	source := attachments.NewMemoryStorage()
	for id := 1; id <= 101; id++ {
		content := fmt.Sprintf("附件%d", id)
		putMigrationAttachment(t, source, int64(id), content)
		addMigrationRecord(t, st, int64(id), "附件.txt", content, "attached")
	}

	var progressMu sync.Mutex
	progress := []int{}
	res, err := attachments.MigrateAttachments(ctx, st.DB, source, attachments.NewMemoryStorage(),
		attachments.MigrationOptions{
			Target: attachments.BackendMarker{
				Backend: attachments.AttachmentBackendOSS, Bucket: "files", Prefix: "agent-taskboard/prod/",
			},
			Progress: func(done, total int) {
				progressMu.Lock()
				defer progressMu.Unlock()
				progress = append(progress, done)
			},
		})
	if err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	if res.Copied != 101 {
		t.Fatalf("复制数 = %d, 想 101", res.Copied)
	}
	progressMu.Lock()
	gotProgress := append([]int(nil), progress...)
	progressMu.Unlock()
	if len(gotProgress) != 2 || gotProgress[0] != 50 || gotProgress[1] != 100 {
		t.Fatalf("进度 = %v, 想每 50 个一次", gotProgress)
	}
}

func Test附件迁移OSS到本地全量后标记与内容正确(t *testing.T) {
	ctx := context.Background()
	st, dataDir := newMigrationDatabase(t)
	if err := attachments.WriteBackendMarker(ctx, st.DB, attachments.BackendMarker{
		Backend: attachments.AttachmentBackendOSS, Bucket: "files", Prefix: "agent-taskboard/prod/",
	}); err != nil {
		t.Fatalf("写入源后端标记失败：%v", err)
	}
	source := attachments.NewMemoryStorage()
	putMigrationAttachment(t, source, 1, "回迁附件")
	putMigrationAttachment(t, source, 2, "回迁待用")
	addMigrationRecord(t, st, 1, "一.txt", "回迁附件", "attached")
	addMigrationRecord(t, st, 2, "二.txt", "回迁待用", "pending")
	target, err := attachments.NewLocalStorage(dataDir)
	if err != nil {
		t.Fatalf("创建本地存储失败：%v", err)
	}

	res, err := attachments.MigrateAttachments(ctx, st.DB, source, target, attachments.MigrationOptions{
		Target:    attachments.BackendMarker{Backend: attachments.AttachmentBackendLocal},
		DataDir:   dataDir,
		FreeSpace: func(string) (uint64, error) { return 1 << 40, nil },
	})
	if err != nil {
		t.Fatalf("回迁失败：%v", err)
	}
	if res.Copied != 2 || res.Skipped != 0 {
		t.Fatalf("回迁结果 = %+v, 想 2 个复制", res)
	}
	marker, exists, err := attachments.ReadBackendMarker(ctx, st.DB)
	if err != nil || !exists || marker != (attachments.BackendMarker{Backend: attachments.AttachmentBackendLocal}) {
		t.Fatalf("回迁后标记 = (%+v, %v, %v), 想 local", marker, exists, err)
	}
	if got := readAttachment(t, target, 1); got != "回迁附件" {
		t.Fatalf("本地附件 = %q", got)
	}
	if got := readAttachment(t, source, 2); got != "回迁待用" {
		t.Fatalf("OSS 源附件被改动：%q", got)
	}
}

func Test附件迁移重跑跳过已一致文件并纠正同大小坏对象(t *testing.T) {
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
	target := attachments.NewMemoryStorage()
	putMigrationAttachment(t, source, 1, "一致")
	putMigrationAttachment(t, source, 2, "源内容")
	putMigrationAttachment(t, target, 1, "一致")
	putMigrationAttachment(t, target, 2, "坏内容") // 与源内容同为 UTF-8 9 字节。
	addMigrationRecord(t, st, 1, "一.txt", "一致", "attached")
	addMigrationRecord(t, st, 2, "二.txt", "源内容", "pending")

	res, err := attachments.MigrateAttachments(ctx, st.DB, source, target, attachments.MigrationOptions{
		Target: attachments.BackendMarker{
			Backend: attachments.AttachmentBackendOSS, Bucket: "files", Prefix: "agent-taskboard/prod/",
		},
	})
	if err != nil {
		t.Fatalf("续跑失败：%v（%+v）", err, res)
	}
	if res.Skipped != 1 || res.Copied != 1 {
		t.Fatalf("续跑结果 = %+v, 想 1 个跳过、1 个重写", res)
	}
	if got := readAttachment(t, target, 2); got != "源内容" {
		t.Fatalf("纠正后的对象 = %q, 想源内容", got)
	}
}

func Test附件迁移源端缺失或复制失败时不切换(t *testing.T) {
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
	putMigrationAttachment(t, source, 1, "存在")
	addMigrationRecord(t, st, 1, "一.txt", "存在", "attached")
	addMigrationRecord(t, st, 2, "二.txt", "缺失", "pending")
	target := attachments.NewMemoryStorage()

	res, err := attachments.MigrateAttachments(ctx, st.DB, source, target, attachments.MigrationOptions{
		Target: attachments.BackendMarker{
			Backend: attachments.AttachmentBackendOSS, Bucket: "files", Prefix: "agent-taskboard/prod/",
		},
	})
	if err == nil {
		t.Fatal("源端缺失时应返回错误")
	}
	if res.Missing != 1 || len(res.MissingIDs) != 1 || res.MissingIDs[0] != 2 {
		t.Fatalf("缺失结果 = %+v, 想列出 2", res)
	}
	marker, exists, err := attachments.ReadBackendMarker(ctx, st.DB)
	if err != nil || !exists || marker.Backend != attachments.AttachmentBackendLocal {
		t.Fatalf("缺失后标记 = (%+v, %v, %v), 想保持 local", marker, exists, err)
	}

	target = attachments.NewMemoryStorage()
	target.PutErr = errors.New("模拟目标端故障")
	res, err = attachments.MigrateAttachments(ctx, st.DB, source, target, attachments.MigrationOptions{
		Target: attachments.BackendMarker{
			Backend: attachments.AttachmentBackendOSS, Bucket: "files", Prefix: "agent-taskboard/prod/",
		},
		AcceptMissing: true,
	})
	if err == nil {
		t.Fatal("复制失败时即使接受缺失也应返回错误")
	}
	if res.Failed != 1 || len(res.FailedIDs) != 1 || res.FailedIDs[0] != 1 {
		t.Fatalf("失败结果 = %+v, 想列出 1", res)
	}
	marker, exists, err = attachments.ReadBackendMarker(ctx, st.DB)
	if err != nil || !exists || marker.Backend != attachments.AttachmentBackendLocal {
		t.Fatalf("失败后标记 = (%+v, %v, %v), 想保持 local", marker, exists, err)
	}
}
