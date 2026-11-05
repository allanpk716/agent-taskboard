package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// noRedirect 返回一个复用原客户端凭证但不跟随 302 的客户端。
func noRedirect(t testing.TB, c *http.Client) *http.Client {
	t.Helper()
	out := *c
	out.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &out
}

// 外链存储后端下，所有入口只做鉴权和访问记录，然后用 no-store 的 302 跳转；
// 被拒绝的访问不会拿到签名地址，但仍留下 denied 记录。
func Test外链存储下附件下载返回302(t *testing.T) {
	mem := attachments.NewMemoryStorage()
	mem.DownloadURLPrefix = "https://oss.example.test/signed/"
	s := tbtest.Start(t, tbtest.WithAttachmentStorage(mem))
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	mine := mustCreateTicket(t, s, web, "外链附件工单")

	uploaded := mustUploadAttachment(t, s, web, mine, "现场.png", []byte("png"))
	if res := claimByReply(t, s, web, mine, map[string]any{"attachment_ids": []string{uploaded.ID}}); res.StatusCode != http.StatusCreated {
		t.Fatalf("挂附件状态码 = %d, 想 201", res.StatusCode)
	}
	path := "/api/v1/attachments/" + uploaded.ID + "/" + url.PathEscape("现场.png")

	_, token := s.IssueAgentToken(t, web, "笔记本")
	guest := s.GuestClient(guestKeyOf(t, mustCreateGuestLink(t, s, web, mine, map[string]any{"note": "检验科-张三"})))
	for name, client := range map[string]*http.Client{
		"成员会话":    noRedirect(t, web),
		"成员Agent": noRedirect(t, s.AgentClient(token)),
		"访客":      noRedirect(t, guest),
	} {
		res := downloadAttachment(t, s, client, path)
		if res.StatusCode != http.StatusFound {
			t.Errorf("[%s] 状态码 = %d, 想 302", name, res.StatusCode)
			continue
		}
		if got := res.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("[%s] Cache-Control = %q, 想 no-store", name, got)
		}
		location := res.Header.Get("Location")
		if !strings.HasPrefix(location, mem.DownloadURLPrefix+"attachments/") {
			t.Errorf("[%s] Location = %q, 想以后端签名地址开头", name, location)
		}
		parsed, err := url.Parse(location)
		if err != nil {
			t.Errorf("[%s] 解析 Location 失败：%v", name, err)
		} else if disposition := parsed.Query().Get("disposition"); !strings.HasPrefix(disposition, "inline") ||
			!strings.Contains(disposition, "%E7%8E%B0%E5%9C%BA.png") {
			t.Errorf("[%s] 签名地址里的 disposition = %q, 想带 inline 和 RFC 5987 文件名", name, disposition)
		}
	}

	req, err := http.NewRequest(http.MethodHead, s.URL()+path, nil)
	if err != nil {
		t.Fatalf("构造 HEAD 请求失败：%v", err)
	}
	headClient := noRedirect(t, web)
	res, err := headClient.Do(req)
	if err != nil {
		t.Fatalf("HEAD 下载失败：%v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusFound || res.Header.Get("Cache-Control") != "no-store" || res.Header.Get("Location") == "" {
		t.Fatalf("HEAD 响应 = %d %q %q, 想 302 no-store 且带 Location",
			res.StatusCode, res.Header.Get("Cache-Control"), res.Header.Get("Location"))
	}

	s.CreateMember("李工", "secret-pass")
	stranger := s.Login("李工", "secret-pass")
	denied := downloadAttachment(t, s, noRedirect(t, stranger), path)
	if denied.StatusCode != http.StatusNotFound || denied.Header.Get("Location") != "" {
		t.Fatalf("无权限响应 = %d Location=%q body=%s, 想 404 且不跳转",
			denied.StatusCode, denied.Header.Get("Location"), readAll(t, denied))
	}

	var downloads, deniedCount int
	if err := s.Store().DB.QueryRow(`SELECT
		COALESCE(SUM(action='download'), 0), COALESCE(SUM(action='denied'), 0)
		FROM access_records WHERE attachment_ref=?`, uploaded.ID).Scan(&downloads, &deniedCount); err != nil {
		t.Fatalf("查访问记录失败：%v", err)
	}
	if downloads != 4 || deniedCount != 1 {
		t.Fatalf("访问记录 downloads=%d denied=%d, 想 4 和 1（含 HEAD）", downloads, deniedCount)
	}
}
