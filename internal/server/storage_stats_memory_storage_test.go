package server_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/config"
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

func TestOSS后端存储统计用数据库累加并带后端标识(t *testing.T) {
	mem := attachments.NewMemoryStorage()
	s := tbtest.Start(t,
		tbtest.WithAttachmentStorage(mem),
		tbtest.WithAttachmentConfig(config.Attachments{
			Backend: attachments.AttachmentBackendOSS,
			OSS: config.OSS{
				Region: "cn-hangzhou", Bucket: "files", Prefix: "agent-taskboard/prod/",
				InternalEndpoint: "oss-cn-hangzhou-internal.aliyuncs.com",
			},
		}),
	)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, admin, "OSS 存储统计")
	uploaded := mustUploadAttachment(t, s, admin, number, "统计.txt", bytesOf(1234))
	id, err := attachments.ParseRef(uploaded.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 故意让假存储对象大小与数据库不一致：OSS 后端不应 List 桶。
	if err := mem.Put(context.Background(), id, bytes.NewReader(bytesOf(999)), 999,
		attachments.PutMeta{ContentType: "text/plain"}); err != nil {
		t.Fatal(err)
	}

	var stats storageStatsBody
	if res := s.Do(t, admin, http.MethodGet, "/api/v1/admin/storage", nil, &stats); res.StatusCode != http.StatusOK {
		t.Fatalf("存储统计状态码 = %d, 想 200", res.StatusCode)
	}
	if stats.AttachmentsBytes != 1234 {
		t.Fatalf("OSS 后端 attachments_bytes = %d, 想数据库里的 1234", stats.AttachmentsBytes)
	}
	if stats.AttachmentsBackend != "oss" {
		t.Fatalf("attachments_backend = %q, 想 oss", stats.AttachmentsBackend)
	}
}
