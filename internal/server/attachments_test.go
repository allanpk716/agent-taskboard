package server_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// attachmentUploadBody 是 POST …/attachments 成功（201）的响应。
type attachmentUploadBody struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	State    string `json:"state"`
	Markdown string `json:"markdown"`
}

// attachmentViewBody 是工单 GET 里 attachments 列表项的结构。
type attachmentViewBody struct {
	ID         string `json:"id"`
	Filename   string `json:"filename"`
	Size       int64  `json:"size"`
	URL        string `json:"url"`
	AttachedTo string `json:"attached_to"`
}

// attachmentUsageBody 是工单 GET 里 attachment_usage 的结构。
type attachmentUsageBody struct {
	UsedBytes        int64   `json:"used_bytes"`
	UsedMB           float64 `json:"used_mb"`
	TicketLimitMB    int     `json:"ticket_limit_mb"`
	FileLimitMB      int     `json:"file_limit_mb"`
	MaxFilesPerPost  int     `json:"max_files_per_post"`
	MaxImagesPerPost int     `json:"max_images_per_post"`
}

// attachmentsTicketBody 是工单 GET 响应里附件相关的部分。
type attachmentsTicketBody struct {
	Attachments     []attachmentViewBody `json:"attachments"`
	AttachmentUsage attachmentUsageBody  `json:"attachment_usage"`
}

// uploadAttachment 用 multipart 上传一个文件（两步上传的第一步），返回原始响应。
func uploadAttachment(t testing.TB, s *tbtest.Server, c *http.Client, ref, filename string, content []byte) *http.Response {
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
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("上传附件请求失败：%v", err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

// mustUploadAttachment 上传并断言 201，返回解码后的响应。
func mustUploadAttachment(t testing.TB, s *tbtest.Server, c *http.Client, ref, filename string, content []byte) attachmentUploadBody {
	t.Helper()
	res := uploadAttachment(t, s, c, ref, filename, content)
	if res.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("上传 %q 状态码 = %d, 想 201（%s）", filename, res.StatusCode, body)
	}
	var got attachmentUploadBody
	tbtest.DecodeJSON(t, res, &got)
	return got
}

// claimByReply 用回复引用附件（两步上传的第二步），返回原始响应。
func claimByReply(t testing.TB, s *tbtest.Server, c *http.Client, ref string, body map[string]any) *http.Response {
	t.Helper()
	return s.Do(t, c, http.MethodPost, "/api/v1/tickets/"+ref+"/replies", body, nil)
}

// 两步上传（spec 用户故事 52）：先上传拿到 att: 引用和现成的 Markdown 片段，
// 之后在回复或说明中引用。待用（pending）附件计入配额但不进附件列表；
// 文件本体放在 data_dir 下，库里只有元数据。
func Test两步上传返回引用与Markdown片段(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "BH-7600CS 质控结果偏高")

	png := make([]byte, 128*1024) // 128KB，足够让 used_mb 进位
	for i := range png {
		png[i] = byte(i)
	}
	uploaded := mustUploadAttachment(t, s, web, number, "质控图.png", png)
	if uploaded.ID != "att_1" {
		t.Errorf("id = %q, 想 att_1", uploaded.ID)
	}
	if uploaded.Filename != "质控图.png" || uploaded.Size != int64(len(png)) {
		t.Errorf("filename/size 不对：%+v", uploaded)
	}
	if uploaded.State != "pending" {
		t.Errorf("state = %q, 想 pending（待用）", uploaded.State)
	}
	// 图片的现成片段是插图写法；非图片是普通链接。
	if want := "![质控图.png](att:att_1)"; uploaded.Markdown != want {
		t.Errorf("图片的 markdown = %q, 想 %q", uploaded.Markdown, want)
	}
	zip := append([]byte("PK\x03\x04"), make([]byte, 2048)...)
	file := mustUploadAttachment(t, s, web, number, "历史数据.zip", zip)
	if want := "[历史数据.zip](att:att_2)"; file.Markdown != want {
		t.Errorf("文件的 markdown = %q, 想 %q", file.Markdown, want)
	}

	// 单工单 GET：附件列表只有已挂上的（pending 不列），用量把待用的也算进去。
	var view attachmentsTicketBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	if len(view.Attachments) != 0 {
		t.Errorf("待用附件不应出现在列表里：%+v", view.Attachments)
	}
	if want := int64(len(png) + len(zip)); view.AttachmentUsage.UsedBytes != want {
		t.Errorf("used_bytes = %d, 想 %d（待用附件计入配额）", view.AttachmentUsage.UsedBytes, want)
	}
	if view.AttachmentUsage.UsedMB < 0.1 {
		t.Errorf("used_mb = %v, 想按 MB 保留一位小数", view.AttachmentUsage.UsedMB)
	}
	if view.AttachmentUsage.TicketLimitMB != 50 || view.AttachmentUsage.FileLimitMB != 5 ||
		view.AttachmentUsage.MaxFilesPerPost != 5 || view.AttachmentUsage.MaxImagesPerPost != 10 {
		t.Errorf("上限回显不对（2026-10-16 新值 5MB/5 个/10 图/50MB）：%+v", view.AttachmentUsage)
	}

	// 文件本体在 data_dir\attachments 下，内容逐字节一致（库里只存元数据）。
	got, err := os.ReadFile(filepath.Join(s.DataDir(), "attachments", "1"))
	if err != nil {
		t.Fatalf("读附件文件失败：%v", err)
	}
	if !bytes.Equal(got, png) {
		t.Error("磁盘上的附件内容与上传的不一致")
	}

	// 上传不进时间线（引用它的回复才进）；不改变工单状态。
	var full ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &full)
	if len(full.Timeline) != 1 || full.Timeline[0].Type != "created" {
		t.Errorf("上传附件不应写时间线事件：%+v", full.Timeline)
	}
}

// 上传的文件名缺失或带路径分隔符时拒绝（422）；工单不存在时 404；匿名 401。
func Test上传附件的文件名校验(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "整理 Q3 质控规则说明")

	// Go 的 multipart 会把带路径的文件名截成基本名（defense in depth：服务端
	// 仍拒绝路径分隔符，但正常情况下根本看不到它们）。
	for name, tc := range map[string]struct{ filename, stored string }{
		"带路径":  {"../../etc/passwd", "passwd"},
		"带反斜杠": {`C:\temp\x.png`, "x.png"},
	} {
		got := mustUploadAttachment(t, s, web, number, tc.filename, []byte("x"))
		if got.Filename != tc.stored {
			t.Errorf("[%s] 文件名应被截成基本名：got %q, want %q", name, got.Filename, tc.stored)
		}
	}

	// 非法文件名：纯空白、超长。
	for name, filename := range map[string]string{
		"纯空白":   "   ",
		"超长文件名": strings.Repeat("长", 200),
	} {
		res := uploadAttachment(t, s, web, number, filename, []byte("x"))
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("[%s] 状态码 = %d, 想 422", name, res.StatusCode)
			continue
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "invalid_filename" {
			t.Errorf("[%s] 错误码 = %q, 想 invalid_filename", name, got.Error.Code)
		}
	}
	// 空文件名在 multipart 层就当作没有文件（Go 按有无 filename 区分文件与字段）。
	if res := uploadAttachment(t, s, web, number, "", []byte("x")); res.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("空文件名状态码 = %d, 想 422", res.StatusCode)
	}

	// 缺少文件字段。
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("note", "没有文件")
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, s.URL()+"/api/v1/tickets/"+number+"/attachments", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := web.Do(req)
	if err != nil {
		t.Fatalf("请求失败：%v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("缺文件字段状态码 = %d, 想 422", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "file_required" {
		t.Errorf("错误码 = %q, 想 file_required", got.Error.Code)
	}

	// 不存在的工单：404；未登录：401。
	if res := uploadAttachment(t, s, web, "20990101-001", "x.png", []byte("x")); res.StatusCode != http.StatusNotFound {
		t.Errorf("不存在的工单上传状态码 = %d, 想 404", res.StatusCode)
	}
	if res := uploadAttachment(t, s, s.NewClient(), number, "x.png", []byte("x")); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("匿名上传状态码 = %d, 想 401", res.StatusCode)
	}
}
