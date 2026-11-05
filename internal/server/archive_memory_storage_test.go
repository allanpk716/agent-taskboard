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

// 假存储下完成归档的生成、移除与导入：附件对象始终经存储接口进出，
// 包内容和导入后的对象保持一致。
func Test内存假存储下归档生成移除导入走存储接口(t *testing.T) {
	mem := attachments.NewMemoryStorage()
	s := tbtest.Start(t, tbtest.WithAttachmentStorage(mem))
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, admin, "假存储归档工单")
	content := []byte("memory archive attachment")
	uploaded := mustUploadAttachment(t, s, admin, number, "归档.txt", content)
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

	var pkg archivePackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成归档包状态码 = %d, 想 201", res.StatusCode)
	}
	pkgRes := s.Do(t, admin, http.MethodGet, pkg.DownloadURL, nil, nil)
	if pkgRes.StatusCode != http.StatusOK {
		t.Fatalf("下载归档包状态码 = %d, 想 200", pkgRes.StatusCode)
	}
	zipBytes, err := io.ReadAll(pkgRes.Body)
	if err != nil {
		t.Fatal(err)
	}
	files := zipFiles(t, zipBytes)
	attachmentPath := "tickets/" + number + "/attachments/" + uploaded.ID + "/归档.txt"
	if string(files[attachmentPath]) != string(content) {
		t.Fatalf("归档包里的附件内容不符：%q", files[attachmentPath])
	}

	if _, err := s.Backups().BackupNow(context.Background(), ops.KindDaily); err != nil {
		t.Fatalf("生成附件镜像失败：%v", err)
	}
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/archive/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("移除归档状态码 = %d, 想 200", res.StatusCode)
	}
	id, err := attachments.ParseRef(uploaded.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := mem.Object(id); exists {
		t.Fatalf("归档移除后附件 %s 应从存储删除", uploaded.ID)
	}

	res, imported := importArchive(t, s, admin, zipBytes, number)
	if res.StatusCode != http.StatusOK || len(imported.Imported) != 1 || imported.Imported[0].Number != number {
		t.Fatalf("导入归档 = %d/%+v, 想 200 且导入 %s", res.StatusCode, imported, number)
	}
	obj, exists := mem.Object(id)
	if !exists {
		t.Fatalf("导入归档后附件 %s 应写回存储", uploaded.ID)
	}
	if string(obj.Content) != string(content) || obj.Size != int64(len(content)) {
		t.Fatalf("导入后的附件对象不对：size=%d content=%q", obj.Size, obj.Content)
	}
	if obj.ContentType != "text/plain; charset=utf-8" {
		t.Fatalf("导入后的 Content-Type = %q, 想按文件名推断", obj.ContentType)
	}
}
