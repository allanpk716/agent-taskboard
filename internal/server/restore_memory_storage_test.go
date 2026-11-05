package server_test

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 假存储下恢复备份时，从备份镜像读附件并写入目标存储后端。
func Test内存假存储下恢复备份补附件走存储接口(t *testing.T) {
	sourceStorage := attachments.NewMemoryStorage()
	target := t.TempDir()
	s := tbtest.Start(t,
		tbtest.WithAttachmentStorage(sourceStorage),
		tbtest.WithBackup(config.Backup{Target: target, DailyAt: "03:00"}))
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, admin, "假存储恢复备份工单")
	content := []byte("memory restore attachment")
	uploaded := mustUploadAttachment(t, s, admin, number, "恢复.txt", content)
	if res := revise(t, s, admin, number, map[string]any{
		"brief_md":       "见附件。",
		"attachment_ids": []string{uploaded.ID},
		"base_revision":  1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂说明附件状态码 = %d, 想 200", res.StatusCode)
	}
	record, err := s.Backups().BackupNow(context.Background(), ops.KindDaily)
	if err != nil {
		t.Fatalf("生成备份失败：%v", err)
	}
	id, err := attachments.ParseRef(uploaded.ID)
	if err != nil {
		t.Fatal(err)
	}

	destinationStorage := attachments.NewMemoryStorage()
	result, err := ops.RestoreWithStorage(t.TempDir(), 0,
		filepath.Join(target, record.Filename),
		time.Date(2026, time.October, 16, 10, 0, 0, 0, time.Local), destinationStorage)
	if err != nil {
		t.Fatalf("恢复备份失败：%v", err)
	}
	if result.AttachmentsRestored != 1 || len(result.MissingAttachments) != 0 {
		t.Fatalf("恢复结果 = %+v, 想从镜像补 1 个附件且无缺失", result)
	}
	obj, exists := destinationStorage.Object(id)
	if !exists {
		t.Fatalf("恢复后附件 %s 应写进目标存储", uploaded.ID)
	}
	if string(obj.Content) != string(content) || obj.Size != int64(len(content)) {
		t.Fatalf("恢复后的附件对象不对：size=%d content=%q", obj.Size, obj.Content)
	}
	if obj.ContentType != "text/plain; charset=utf-8" {
		t.Fatalf("恢复后的 Content-Type = %q, 想按文件名推断", obj.ContentType)
	}
}
