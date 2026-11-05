package server_test

import (
	"net/http"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 假存储下存储统计通过 List 汇总附件对象占用。
func Test内存假存储下存储统计附件占用走存储接口(t *testing.T) {
	mem := attachments.NewMemoryStorage()
	s := tbtest.Start(t, tbtest.WithAttachmentStorage(mem))
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, admin, "假存储统计工单")
	mustUploadAttachment(t, s, admin, number, "统计.txt", bytesOf(1234))

	var stats storageStatsBody
	if res := s.Do(t, admin, http.MethodGet, "/api/v1/admin/storage", nil, &stats); res.StatusCode != http.StatusOK {
		t.Fatalf("存储统计状态码 = %d, 想 200", res.StatusCode)
	}
	if stats.AttachmentsBytes != 1234 {
		t.Fatalf("attachments_bytes = %d, 想 1234", stats.AttachmentsBytes)
	}

	mustUploadAttachment(t, s, admin, number, "补充.txt", bytesOf(100))
	stats = storageStatsBody{}
	s.Do(t, admin, http.MethodGet, "/api/v1/admin/storage", nil, &stats)
	if stats.AttachmentsBytes != 1334 {
		t.Fatalf("再上传后 attachments_bytes = %d, 想 1334", stats.AttachmentsBytes)
	}
}
