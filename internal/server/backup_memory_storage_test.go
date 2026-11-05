package server_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 假存储下备份镜像从 Storage 列出并读出附件，落到备份目标的 attachments 下。
func Test内存假存储下备份镜像走存储接口(t *testing.T) {
	mem := attachments.NewMemoryStorage()
	s := tbtest.Start(t, tbtest.WithAttachmentStorage(mem))
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, admin, "假存储备份工单")
	content := []byte("memory backup attachment")
	uploaded := mustUploadAttachment(t, s, admin, number, "备份.txt", content)

	record, err := s.Backups().BackupNow(context.Background(), ops.KindDaily)
	if err != nil {
		t.Fatalf("备份失败：%v", err)
	}
	if !record.IntegrityOK || record.FilesAdded != 1 {
		t.Fatalf("备份记录 = %+v, 想成功并镜像 1 个附件", record)
	}
	id, err := attachments.ParseRef(uploaded.ID)
	if err != nil {
		t.Fatal(err)
	}
	mirror, err := os.ReadFile(filepath.Join(s.Backups().TargetDir(), "attachments", strconv.FormatInt(id, 10)))
	if err != nil {
		t.Fatalf("读附件镜像失败：%v", err)
	}
	if string(mirror) != string(content) {
		t.Fatalf("附件镜像内容 = %q, 想 %q", mirror, content)
	}
}
