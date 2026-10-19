package server_test

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type guestCookieTransport struct{ token string }

func (t guestCookieTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Cookie", "tb_guest_device="+t.token)
	return http.DefaultTransport.RoundTrip(clone)
}

func guestDeviceCookie(t testing.TB, res *http.Response) string {
	t.Helper()
	for _, c := range res.Cookies() {
		if c.Name == "tb_guest_device" && c.Value != "" {
			return c.Value
		}
	}
	t.Fatal("响应没有下发访客设备 cookie")
	return ""
}

func Test浏览器认领后仅凭访客设备cookie下载附件(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "浏览器访客下载附件")
	att := mustUploadAttachment(t, s, web, number, "资料.txt", []byte("guest only"))
	if res := claimByReply(t, s, web, number, map[string]any{"attachment_ids": []string{att.ID}}); res.StatusCode != http.StatusCreated {
		t.Fatalf("挂附件状态码 = %d", res.StatusCode)
	}
	link := s.CreateGuestLink(t, web, number, map[string]any{"note": "检验科-张三"})
	res := s.Do(t, s.AgentClient(link.Key(t)), http.MethodPost, "/api/v1/guest-links/claim",
		map[string]string{"author_name": "检验科 张三"}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("认领状态码 = %d", res.StatusCode)
	}
	cookieClient := &http.Client{Transport: guestCookieTransport{token: guestDeviceCookie(t, res)}}
	attPath := "/api/v1/attachments/" + att.ID + "/" + url.PathEscape(att.Filename)
	if got := downloadAttachment(t, s, cookieClient, attPath); got.StatusCode != http.StatusOK {
		t.Fatalf("仅凭 cookie 下载附件状态码 = %d, 想 200", got.StatusCode)
	}
}

func Test入口提交响应下发访客设备cookie(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	createIntake(t, s, web, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})
	res := s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/intakes/bug/tickets",
		map[string]any{"title": "浏览器提交反馈", "fields": bugFields(), "author_name": "赵六"}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("入口提交状态码 = %d, 想 201", res.StatusCode)
	}
	cookieClient := &http.Client{Transport: guestCookieTransport{token: guestDeviceCookie(t, res)}}
	var view ticketResponseBody
	got := s.Do(t, cookieClient, http.MethodGet, "/api/v1/tickets/20261016-001", nil, &view)
	if got.StatusCode != http.StatusOK {
		t.Fatalf("仅凭 cookie 读反馈状态码 = %d, 想 200", got.StatusCode)
	}
}

func Test访客设备cookie在链接撤销后得到401并留下被拒记录(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "撤销后的访客 cookie")
	link := s.CreateGuestLink(t, web, number, map[string]any{"note": "检验科-张三"})
	res := s.Do(t, s.AgentClient(link.Key(t)), http.MethodPost, "/api/v1/guest-links/claim",
		map[string]string{"author_name": "检验科 张三"}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("认领状态码 = %d", res.StatusCode)
	}
	if got := s.Do(t, web, http.MethodDelete,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(link.ID, 10), nil, nil); got.StatusCode != http.StatusNoContent {
		t.Fatalf("撤销链接状态码 = %d", got.StatusCode)
	}
	cookieClient := &http.Client{Transport: guestCookieTransport{token: guestDeviceCookie(t, res)}}
	got := s.Do(t, cookieClient, http.MethodGet, "/api/v1/tickets/"+number, nil, nil)
	var err errorBody
	tbtest.DecodeJSON(t, got, &err)
	if got.StatusCode != http.StatusUnauthorized || err.Error.Code != "link_revoked" {
		t.Fatalf("撤销后的 cookie = %d/%s, 想 401/link_revoked", got.StatusCode, err.Error.Code)
	}
	records := readAccessRecords(t, s, web, number)
	found := false
	for _, record := range records.Records {
		if record.Action == "denied" && record.Actor.Kind == "guest" {
			found = true
		}
	}
	if !found {
		t.Fatalf("撤销后的 cookie 应留下访客被拒记录：%+v", records.Records)
	}
}
