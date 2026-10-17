package server_test

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// downloadAttachment 用给定客户端取附件地址，返回原始响应。
func downloadAttachment(t testing.TB, s *tbtest.Server, c *http.Client, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, s.URL()+path, nil)
	if err != nil {
		t.Fatalf("构造请求失败：%v", err)
	}
	client := c
	if client == nil {
		client = s.NewClient() // 无凭证（模拟 <img src> 直接取签名地址）
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("下载附件请求失败：%v", err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

// 下载的 Content-Disposition 与 nosniff（验收点）：图片、PDF、.txt、.log 以
// inline 返回；其余（含 HTML、SVG）一律 attachment；所有响应都带
// X-Content-Type-Options: nosniff。内容逐字节一致，Content-Type 按扩展名。
func Test附件下载的安全响应头(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "附件安全测试")

	files := map[string][]byte{
		"报告.html": []byte("<html><script>alert(1)</script></html>"),
		"图形.svg":  []byte("<svg xmlns='http://www.w3.org/2000/svg'></svg>"),
		"截图.png":  []byte("PNGDATA"),
		"文档.pdf":  []byte("%PDF-1.4"),
		"说明.txt":  []byte("文本内容"),
		"日志.log":  []byte("2026-10-16 09:00 INFO 启动"),
		"数据.zip":  []byte("PK\x03\x04zip"),
	}
	refs := map[string]string{}
	for name, content := range files {
		refs[name] = mustUploadAttachment(t, s, web, number, name, content).ID
	}
	// 分两条回复挂上（每帖最多 5 个附件）：前四个、后三个。
	first := []string{"报告.html", "图形.svg", "截图.png", "文档.pdf"}
	second := []string{"说明.txt", "日志.log", "数据.zip"}
	for _, group := range [][]string{first, second} {
		ids := make([]string, 0, len(group))
		for _, name := range group {
			ids = append(ids, refs[name])
		}
		if res := claimByReply(t, s, web, number, map[string]any{
			"body_md":        "见附件。",
			"attachment_ids": ids,
		}); res.StatusCode != http.StatusCreated {
			t.Fatalf("挂附件状态码 = %d, 想 201", res.StatusCode)
		}
	}

	var view attachmentsTicketBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	urls := map[string]string{}
	for _, att := range view.Attachments {
		urls[att.Filename] = att.URL
	}

	for name, wantDisposition := range map[string]string{
		"报告.html": "attachment", // HTML 只能下载
		"图形.svg":  "attachment", // SVG 只能下载
		"截图.png":  "inline",
		"文档.pdf":  "inline",
		"说明.txt":  "inline",
		"日志.log":  "inline",
		"数据.zip":  "attachment",
	} {
		res := downloadAttachment(t, s, web, urls[name])
		if res.StatusCode != http.StatusOK {
			t.Errorf("[%s] 状态码 = %d, 想 200", name, res.StatusCode)
			continue
		}
		if got := res.Header.Get("Content-Disposition"); !strings.HasPrefix(got, wantDisposition) {
			t.Errorf("[%s] Content-Disposition = %q, 想以 %q 开头", name, got, wantDisposition)
		}
		if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("[%s] X-Content-Type-Options = %q, 想 nosniff", name, got)
		}
		if got := res.Header.Get("Content-Type"); got == "" {
			t.Errorf("[%s] 缺 Content-Type", name)
		}
		body := readAll(t, res)
		if string(body) != string(files[name]) {
			t.Errorf("[%s] 下载的内容与上传的不一致", name)
		}
	}

	// 文件名带非 ASCII 时 Content-Disposition 用 RFC 5987 编码，仍能下载。
	res := downloadAttachment(t, s, web, urls["截图.png"])
	if !strings.Contains(res.Header.Get("Content-Disposition"), "filename") {
		t.Errorf("Content-Disposition 应带 filename：%q", res.Header.Get("Content-Disposition"))
	}

	// 地址里的文件名不对：404（地址从读取接口拿，防止张冠李戴）。
	bad := strings.Replace(urls["截图.png"], url.PathEscape("截图.png"), "别的名字.png", 1)
	if res := downloadAttachment(t, s, web, bad); res.StatusCode != http.StatusNotFound {
		t.Errorf("文件名不符状态码 = %d, 想 404", res.StatusCode)
	}
	// 待用（未引用）附件不能下载。
	pending := mustUploadAttachment(t, s, web, number, "待用.txt", []byte("x"))
	pendingURL := "/api/v1/attachments/" + pending.ID + "/" + url.PathEscape("待用.txt")
	if res := downloadAttachment(t, s, web, pendingURL); res.StatusCode != http.StatusNotFound {
		t.Errorf("待用附件下载状态码 = %d, 想 404", res.StatusCode)
	}
	// 不存在的附件：404；匿名且无签名：401。
	if res := downloadAttachment(t, s, web, "/api/v1/attachments/att_999/x.png"); res.StatusCode != http.StatusNotFound {
		t.Errorf("不存在的附件状态码 = %d, 想 404", res.StatusCode)
	}
	if res := downloadAttachment(t, s, nil, urls["截图.png"]); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("匿名下载状态码 = %d, 想 401", res.StatusCode)
	}
}

// 附件地址使用与工单相同的凭证：成员会话、成员 Agent、访客链接密钥都能下载；
// 访客只能下载自己链接指向的那张工单的附件（他单 404，防枚举）。
func Test附件下载使用与工单相同的凭证(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	mine := mustCreateTicket(t, s, web, "有附件的工单")
	other := mustCreateTicket(t, s, web, "另一张工单")

	att := mustUploadAttachment(t, s, web, mine, "现场.png", []byte("png"))
	if res := claimByReply(t, s, web, mine, map[string]any{"attachment_ids": []string{att.ID}}); res.StatusCode != http.StatusCreated {
		t.Fatalf("挂附件状态码 = %d, 想 201", res.StatusCode)
	}
	path := "/api/v1/attachments/" + att.ID + "/" + url.PathEscape("现场.png")

	// 成员本人（会话 cookie）、成员 Agent（Bearer）都可以。
	if res := downloadAttachment(t, s, web, path); res.StatusCode != http.StatusOK {
		t.Errorf("成员会话下载状态码 = %d, 想 200", res.StatusCode)
	}
	_, token := s.IssueAgentToken(t, web, "笔记本")
	if res := downloadAttachment(t, s, s.AgentClient(token), path); res.StatusCode != http.StatusOK {
		t.Errorf("成员 Agent 下载状态码 = %d, 想 200", res.StatusCode)
	}

	// 指向这张工单的访客链接可以；指向另一张工单的链接取这张的附件：404。
	good := s.GuestClient(guestKeyOf(t, mustCreateGuestLink(t, s, web, mine, map[string]any{"note": "检验科-张三"})))
	if res := downloadAttachment(t, s, good, path); res.StatusCode != http.StatusOK {
		t.Errorf("指向本单的访客链接下载状态码 = %d, 想 200", res.StatusCode)
	}
	badLink := s.GuestClient(guestKeyOf(t, mustCreateGuestLink(t, s, web, other, map[string]any{"note": "别单"})))
	res := downloadAttachment(t, s, badLink, path)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("指向他单的访客链接下载状态码 = %d, 想 404", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "ticket_not_found" {
		t.Errorf("错误码 = %q, 想 ticket_not_found（防枚举）", got.Error.Code)
	}

	// 未知 Bearer：401 invalid_token。
	if res := downloadAttachment(t, s, s.GuestClient("tbg_nosuch"), path); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("未知密钥状态码 = %d, 想 401", res.StatusCode)
	}
}

// 访客链接拿到的附件地址与该链接绑定：工单 JSON / Markdown 里给访客的 url 是
// 短时效签名地址，<img src> 直接可用（无 Bearer 头）；链接撤销后同一地址 401
// link_revoked、过期后 401 link_expired（验收点：撤销后 401）。
func Test访客链接绑定的附件地址(t *testing.T) {
	s := tbtest.Start(t) // 2026-10-16 09:00
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "BH-7600CS 质控结果偏高")

	link := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三", "valid_days": 7})
	guest := s.GuestClient(guestKeyOf(t, link))

	att := mustUploadAttachment(t, s, guest, number, "现场照片.png", []byte("photo"))
	if res := claimByReply(t, s, guest, number, map[string]any{
		"attachment_ids": []string{att.ID}, "author_name": "张三",
	}); res.StatusCode != http.StatusCreated {
		t.Fatalf("访客挂附件状态码 = %d, 想 201", res.StatusCode)
	}

	// 访客读工单：url 是带签名的完整地址（link/exp/sig 参数）。
	var view attachmentsTicketBody
	s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	if len(view.Attachments) != 1 {
		t.Fatalf("附件列表 = %+v", view.Attachments)
	}
	signed := view.Attachments[0].URL
	if !strings.Contains(signed, "sig=") || !strings.Contains(signed, "link=") || !strings.Contains(signed, "exp=") {
		t.Fatalf("访客的附件地址应是签名地址：%q", signed)
	}
	// 成员拿到的地址不带签名（成员用凭证即可）。
	var memberView attachmentsTicketBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &memberView)
	if strings.Contains(memberView.Attachments[0].URL, "sig=") {
		t.Errorf("成员的地址不应带签名：%q", memberView.Attachments[0].URL)
	}

	// 无任何凭证直接取签名地址（模拟 <img src>）：200，内容一致，头同样安全。
	res := downloadAttachment(t, s, nil, signed)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("签名地址状态码 = %d, 想 200（body=%s）", res.StatusCode, readAll(t, res))
	}
	if string(readAll(t, res)) != "photo" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("签名地址的响应不对：headers=%v", res.Header)
	}

	// Markdown 里访客拿到的也是签名地址。
	md := string(readAll(t, s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+number+"?format=markdown", nil, nil)))
	if !strings.Contains(md, "sig=") {
		t.Errorf("访客 Markdown 的附件链接应是签名地址：\n%s", md)
	}

	// 篡改签名 / 换附件：401 / 404。
	forged := strings.Replace(signed, "sig=", "sig=deadbeef", 1)
	if res := downloadAttachment(t, s, nil, forged); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("篡改签名状态码 = %d, 想 401", res.StatusCode)
	}
	// 签名拿到别的附件上用：链接指向的工单不符，按不存在处理（防枚举）。
	otherAtt := mustUploadAttachment(t, s, web, number, "成员的图.png", []byte("m"))
	if res := claimByReply(t, s, web, number, map[string]any{"attachment_ids": []string{otherAtt.ID}}); res.StatusCode != http.StatusCreated {
		t.Fatalf("成员挂附件失败：%d", res.StatusCode)
	}
	reused := fmt.Sprintf("/api/v1/attachments/%s/%s?%s", otherAtt.ID, url.PathEscape("成员的图.png"), signed[strings.Index(signed, "?")+1:])
	if res := downloadAttachment(t, s, nil, reused); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("签名挪用到别的附件状态码 = %d, 想 401（签名绑定附件）", res.StatusCode)
	}

	// 撤销链接：同一签名地址 401 link_revoked（验收点）。
	if res := s.Do(t, web, http.MethodDelete,
		"/api/v1/tickets/"+number+"/guest-links/"+fmt.Sprint(link.ID), nil, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("撤销链接状态码 = %d, 想 204", res.StatusCode)
	}
	res = downloadAttachment(t, s, nil, signed)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("撤销后签名地址状态码 = %d, 想 401", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "link_revoked" {
		t.Errorf("撤销后错误码 = %q, 想 link_revoked", got.Error.Code)
	}
	// 访客链接密钥作 Bearer 下载也一并失效。
	if res := downloadAttachment(t, s, guest, signed); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("撤销后密钥下载状态码 = %d, 想 401", res.StatusCode)
	}

	// 过期同样让地址失效：换一条 7 天的链接，把时钟拨过去。
	link2 := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-李四", "valid_days": 7})
	guest2 := s.GuestClient(guestKeyOf(t, link2))
	att2 := mustUploadAttachment(t, s, guest2, number, "第二张.png", []byte("p2"))
	if res := claimByReply(t, s, guest2, number, map[string]any{"attachment_ids": []string{att2.ID}}); res.StatusCode != http.StatusCreated {
		t.Fatalf("第二条链接挂附件状态码 = %d, 想 201", res.StatusCode)
	}
	var view2 attachmentsTicketBody
	s.Do(t, guest2, http.MethodGet, "/api/v1/tickets/"+number, nil, &view2)
	signed2 := view2.Attachments[len(view2.Attachments)-1].URL

	s.Clock().Advance(7 * 24 * time.Hour) // 到期时刻含边界即失效
	res = downloadAttachment(t, s, nil, signed2)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("链接过期后签名地址状态码 = %d, 想 401", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "link_expired" {
		t.Errorf("过期后错误码 = %q, 想 link_expired", got.Error.Code)
	}
	_ = io.Discard
}
