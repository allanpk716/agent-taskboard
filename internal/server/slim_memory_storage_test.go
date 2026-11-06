package server_test

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 假存储下完成瘦身的生成、移出与恢复：附件对象从存储读出、删除并写回。
func Test内存假存储下瘦身生成移出恢复走存储接口(t *testing.T) {
	mem := attachments.NewMemoryStorage()
	s := tbtest.Start(t, tbtest.WithAttachmentStorage(mem))
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, admin, "假存储瘦身工单")
	content := []byte("memory slim attachment")
	uploaded := mustUploadAttachment(t, s, admin, number, "瘦身.log", content)
	if res := revise(t, s, admin, number, map[string]any{
		"brief_md":       "见附件。",
		"attachment_ids": []string{uploaded.ID},
		"base_revision":  1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂说明附件状态码 = %d, 想 200", res.StatusCode)
	}
	if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d, 想 200", res.StatusCode)
	}
	s.Clock().Set(time.Date(2026, time.October, 30, 10, 0, 0, 0, clock.Beijing))
	admin = s.Login("陈工", "secret-pass")

	var pkg slimPackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/slim/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成瘦身包状态码 = %d, 想 201", res.StatusCode)
	}
	if _, exists := mem.ObjectByKey("archive-packages/" + pkg.Name); exists {
		t.Fatal("本地后端生成瘦身包不应上传 OSS 对象")
	}
	pkgRes := s.Do(t, admin, http.MethodGet, pkg.DownloadURL, nil, nil)
	if pkgRes.StatusCode != http.StatusOK {
		t.Fatalf("下载瘦身包状态码 = %d, 想 200", pkgRes.StatusCode)
	}
	zipBytes, err := io.ReadAll(pkgRes.Body)
	if err != nil {
		t.Fatal(err)
	}
	files := zipFiles(t, zipBytes)
	attachmentPath := "tickets/" + number + "/attachments/" + uploaded.ID + "/瘦身.log"
	if string(files[attachmentPath]) != string(content) {
		t.Fatalf("瘦身包里的附件内容不符：%q", files[attachmentPath])
	}

	if _, err := s.Backups().BackupNow(context.Background(), ops.KindDaily); err != nil {
		t.Fatalf("生成附件镜像失败：%v", err)
	}
	var removed slimRemoveBody
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/slim/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, &removed); res.StatusCode != http.StatusOK {
		t.Fatalf("移出附件状态码 = %d, 想 200", res.StatusCode)
	}
	id, err := attachments.ParseRef(uploaded.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := mem.Object(id); exists {
		t.Fatalf("瘦身移出后附件 %s 应从存储删除", uploaded.ID)
	}

	res, restored := restoreSlim(t, s, admin, zipBytes, number)
	if res.StatusCode != http.StatusOK || len(restored.Restored) != 1 || restored.Restored[0].Number != number {
		t.Fatalf("恢复瘦身 = %d/%+v, 想 200 且恢复 %s", res.StatusCode, restored, number)
	}
	obj, exists := mem.Object(id)
	if !exists {
		t.Fatalf("恢复瘦身后附件 %s 应写回存储", uploaded.ID)
	}
	if string(obj.Content) != string(content) || obj.Size != int64(len(content)) {
		t.Fatalf("恢复后的附件对象不对：size=%d content=%q", obj.Size, obj.Content)
	}
	if obj.ContentType != "text/plain; charset=utf-8" {
		t.Fatalf("恢复后的 Content-Type = %q, 想按文件名推断", obj.ContentType)
	}
}
