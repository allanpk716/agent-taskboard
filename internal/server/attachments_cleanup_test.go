package server_test

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// uploadAttachmentWithHeaders 同 uploadAttachment，但可附加请求头（如 Idempotency-Key）。
func uploadAttachmentWithHeaders(t testing.TB, s *tbtest.Server, c *http.Client, ref, filename string, content []byte, headers map[string]string) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("构造 multipart 失败：%v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("写 multipart 失败：%v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("关闭 multipart 失败：%v", err)
	}
	req, err := http.NewRequest(http.MethodPost, s.URL()+"/api/v1/tickets/"+ref+"/attachments", &buf)
	if err != nil {
		t.Fatalf("构造请求失败：%v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("上传附件请求失败：%v", err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

// 未被引用的附件 24 小时后被清除（验收点）：注入时钟推进后跑清除任务；
// 已挂到回复/说明上的附件保留。清除同时删元数据行与磁盘文件。
func Test未被引用的附件二十四小时后被清除(t *testing.T) {
	s := tbtest.Start(t) // 2026-10-16 09:00
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "整理 Q3 质控规则说明")

	claimed := mustUploadAttachment(t, s, web, number, "已引用.png", []byte("keep"))
	pending := mustUploadAttachment(t, s, web, number, "待清除.zip", []byte("drop"))
	if res := claimByReply(t, s, web, number, map[string]any{"attachment_ids": []string{claimed.ID}}); res.StatusCode != http.StatusCreated {
		t.Fatalf("挂附件状态码 = %d, 想 201", res.StatusCode)
	}

	// 23 小时：都还在（用量含两个）。
	s.Clock().Advance(23 * time.Hour)
	if n, err := s.Attachments().CleanupUnreferenced(context.Background()); err != nil || n != 0 {
		t.Fatalf("23 小时后清除任务删了 %d 个（err=%v），想 0", n, err)
	}
	var view attachmentsTicketBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	if view.AttachmentUsage.UsedBytes != int64(len("keep")+len("drop")) {
		t.Errorf("23 小时后 used_bytes = %d, 想两个都还在", view.AttachmentUsage.UsedBytes)
	}

	// 再过 2 小时（合计 25h > 24h）：待用的被清除，已引用的保留。
	s.Clock().Advance(2 * time.Hour)
	removed, err := s.Attachments().CleanupUnreferenced(context.Background())
	if err != nil {
		t.Fatalf("清除任务失败：%v", err)
	}
	if removed != 1 {
		t.Fatalf("清除了 %d 个, 想 1", removed)
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	if view.AttachmentUsage.UsedBytes != int64(len("keep")) {
		t.Errorf("清除后 used_bytes = %d, 想只剩已引用的", view.AttachmentUsage.UsedBytes)
	}
	if len(view.Attachments) != 1 || view.Attachments[0].Filename != "已引用.png" {
		t.Errorf("附件列表不对：%+v", view.Attachments)
	}
	// 磁盘：待用的文件删掉，已引用的还在。
	if _, err := os.Stat(filepath.Join(s.DataDir(), "attachments", "1")); err != nil {
		t.Errorf("已引用的附件文件应保留：%v", err)
	}
	if _, err := os.Stat(filepath.Join(s.DataDir(), "attachments", "2")); !os.IsNotExist(err) {
		t.Errorf("待用的附件文件应被删除（err=%v）", err)
	}
	// 再跑一次：幂等，没有可清的了。
	if n, err := s.Attachments().CleanupUnreferenced(context.Background()); err != nil || n != 0 {
		t.Errorf("重复清除删了 %d 个（err=%v），想 0", n, err)
	}
	_ = pending.ID
}

// 上传支持 Idempotency-Key：重试拿到首次结果（同一个附件 id），不占双份配额。
func Test上传附件的幂等键(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "整理 Q3 质控规则说明")

	headers := map[string]string{"Idempotency-Key": "upload-001"}
	first := uploadAttachmentWithHeaders(t, s, web, number, "网络闪断.png", []byte("data"), headers)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("首次上传状态码 = %d, 想 201", first.StatusCode)
	}
	firstBody := readAll(t, first)
	second := uploadAttachmentWithHeaders(t, s, web, number, "网络闪断.png", []byte("data"), headers)
	if second.StatusCode != http.StatusCreated || string(readAll(t, second)) != string(firstBody) {
		t.Errorf("重试应返回首次结果：%d %s", second.StatusCode, readAll(t, second))
	}

	var view attachmentsTicketBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	if view.AttachmentUsage.UsedBytes != int64(len("data")) {
		t.Errorf("used_bytes = %d, 想 %d（重试不占双份配额）", view.AttachmentUsage.UsedBytes, int64(len("data")))
	}
	// 换一个 key 是新的上传。
	if res := uploadAttachmentWithHeaders(t, s, web, number, "另一个.png", []byte("data"), map[string]string{"Idempotency-Key": "upload-002"}); res.StatusCode != http.StatusCreated {
		t.Errorf("新 key 上传状态码 = %d, 想 201", res.StatusCode)
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	if view.AttachmentUsage.UsedBytes != 2*int64(len("data")) {
		t.Errorf("used_bytes = %d, 想翻倍", view.AttachmentUsage.UsedBytes)
	}
}

// 工单关闭后：成员仍可上传并引用（决策 2：不重开工单），访客链接只读
// （上传 409 ticket_closed）。
func Test关闭后的上传按凭证区分(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "整理 Q3 质控规则说明")
	closeTicket(t, s, web, number, closeBody{Reason: "done"})
	key := guestKeyOf(t, mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三"}))

	// 访客上传：409 ticket_closed（链接只读）。
	res := uploadAttachment(t, s, s.GuestClient(key), number, "关闭后.png", []byte("x"))
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("关闭后访客上传状态码 = %d, 想 409", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "ticket_closed" {
		t.Errorf("错误码 = %q, 想 ticket_closed", got.Error.Code)
	}

	// 成员上传并挂到回复上：可以，但工单仍是关闭的。
	att := mustUploadAttachment(t, s, web, number, "补充材料.zip", []byte("more"))
	if res := claimByReply(t, s, web, number, map[string]any{
		"body_md": "关闭后补充材料。", "attachment_ids": []string{att.ID},
	}); res.StatusCode != http.StatusCreated {
		t.Errorf("关闭后成员带附件回复状态码 = %d, 想 201", res.StatusCode)
	}
	var view attachmentsTicketBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	if len(view.Attachments) != 1 || view.Attachments[0].AttachedTo != "回复 1" {
		t.Errorf("附件应挂在回复上：%+v", view.Attachments)
	}
	if !strings.HasSuffix(view.Attachments[0].URL, ".zip") && !strings.Contains(view.Attachments[0].URL, ".zip") {
		t.Errorf("下载地址不对：%q", view.Attachments[0].URL)
	}
}
