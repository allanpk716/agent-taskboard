package server_test

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func startMemoryStorageServer(t *testing.T, mem *attachments.MemoryStorage) *tbtest.Server {
	t.Helper()
	s := tbtest.Start(t, tbtest.WithAttachmentStorage(mem))
	s.CreateAdmin("陈工", "secret-pass")
	return s
}

// 假存储下上传照旧：对象进内存后端并带元数据，本地目录不再落文件。
func Test内存假存储下上传附件写入对象与元数据(t *testing.T) {
	mem := attachments.NewMemoryStorage()
	s := startMemoryStorageServer(t, mem)
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "带附件的工单")
	content := []byte("假存储里的附件内容")

	got := mustUploadAttachment(t, s, web, number, "报告.txt", content)
	id, err := attachments.ParseRef(got.ID)
	if err != nil {
		t.Fatalf("解析附件引用 %q 失败：%v", got.ID, err)
	}
	obj, ok := mem.Object(id)
	if !ok {
		t.Fatalf("附件 %s 应写进注入的存储", got.ID)
	}
	if obj.Size != int64(len(content)) || string(obj.Content) != string(content) {
		t.Fatalf("存储里的对象不对：size=%d content=%q", obj.Size, obj.Content)
	}
	if obj.ContentType != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q, 想按扩展名写入", obj.ContentType)
	}
	if _, err := os.Stat(filepath.Join(s.DataDir(), "attachments", got.ID)); !os.IsNotExist(err) {
		t.Fatalf("假存储下不应在本地落文件：%v", err)
	}
}

// 假存储下完成上传和下载：下载也从存储接口流式读取，响应头保持不变。
func Test内存假存储下下载附件走存储接口(t *testing.T) {
	mem := attachments.NewMemoryStorage()
	s := startMemoryStorageServer(t, mem)
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "假存储下载工单")
	content := []byte("从内存后端读回的内容")

	uploaded := mustUploadAttachment(t, s, web, number, "下载.txt", content)
	if res := claimByReply(t, s, web, number, map[string]any{
		"body_md":        "见附件。",
		"attachment_ids": []string{uploaded.ID},
	}); res.StatusCode != http.StatusCreated {
		t.Fatalf("挂附件状态码 = %d, 想 201", res.StatusCode)
	}
	var view attachmentsTicketBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	if len(view.Attachments) != 1 {
		t.Fatalf("附件数 = %d, 想 1", len(view.Attachments))
	}
	res := downloadAttachment(t, s, web, view.Attachments[0].URL)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("下载状态码 = %d, 想 200", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, 想 nosniff", got)
	}
	if got := res.Header.Get("Content-Disposition"); !strings.HasPrefix(got, "inline") {
		t.Errorf("Content-Disposition = %q, 想以 inline 开头", got)
	}
	if want := strconv.Itoa(len(content)); res.Header.Get("Content-Length") != want {
		t.Errorf("Content-Length = %q, 想 %s", res.Header.Get("Content-Length"), want)
	}
	if body := readAll(t, res); string(body) != string(content) {
		t.Errorf("下载内容 = %q, 想 %q", body, content)
	}
}

// Put 失败：接口返回 500，数据库不留行，存储不留对象。
func Test内存假存储下Put失败不留附件与行(t *testing.T) {
	mem := attachments.NewMemoryStorage()
	mem.PutErr = errors.New("模拟存储故障")
	s := startMemoryStorageServer(t, mem)
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "上传失败工单")

	res := uploadAttachment(t, s, web, number, "失败.txt", []byte("写不进去"))
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d, 想 500", res.StatusCode)
	}
	if ids := mem.IDs(); len(ids) != 0 {
		t.Fatalf("失败的 Put 不应留对象：%v", ids)
	}
	var count int
	if err := s.Store().DB.QueryRow("SELECT COUNT(*) FROM attachments").Scan(&count); err != nil {
		t.Fatalf("查附件行失败：%v", err)
	}
	if count != 0 {
		t.Fatalf("Put 失败后数据库不应留行：count = %d", count)
	}
}
