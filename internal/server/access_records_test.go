package server_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type accessRecordBody struct {
	Actor struct {
		Name        string `json:"name"`
		Kind        string `json:"kind"`
		IsAdmin     bool   `json:"is_admin"`
		LinkNote    string `json:"link_note"`
		EntryMethod string `json:"entry_method"`
	} `json:"actor"`
	Action     string `json:"action"`
	Attachment struct {
		ID       string `json:"id"`
		Filename string `json:"filename"`
	} `json:"attachment"`
	FirstAt string `json:"first_at"`
	LastAt  string `json:"last_at"`
	Count   int    `json:"count"`
}

type accessRecordsBody struct {
	Records []accessRecordBody `json:"records"`
	Since   string             `json:"since"`
}

func readAccessRecords(t testing.TB, s *tbtest.Server, c *http.Client, ref string) accessRecordsBody {
	t.Helper()
	var got accessRecordsBody
	res := s.Do(t, c, http.MethodGet, "/api/v1/tickets/"+ref+"/access-records", nil, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("读取访问记录状态码 = %d, 想 200", res.StatusCode)
	}
	return got
}

func Test成员打开详情一小时内的查看合并为一条访问记录(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	web := s.Login("王工", "member-pass")
	number := mustCreateTicket(t, s, web, "访问记录测试")

	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, nil)
	s.Clock().Advance(20 * time.Minute)
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number+"/timeline", nil, nil)
	s.Clock().Advance(20 * time.Minute)
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, nil)

	got := readAccessRecords(t, s, web, number)
	if len(got.Records) != 1 {
		t.Fatalf("访问记录有 %d 条, 想 1： %+v", len(got.Records), got.Records)
	}
	r := got.Records[0]
	if r.Actor.Name != "王工" || r.Actor.Kind != "member" || r.Actor.IsAdmin {
		t.Errorf("成员署名不对：%+v", r.Actor)
	}
	if r.Action != "view" || r.Count != 3 {
		t.Errorf("动作/次数 = %q/%d, 想 view/3", r.Action, r.Count)
	}
	if r.FirstAt == "" || r.LastAt == "" || r.FirstAt == r.LastAt {
		t.Errorf("首次/末次时间不对：%q %q", r.FirstAt, r.LastAt)
	}

	s.Clock().Advance(61 * time.Minute)
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, nil)
	got = readAccessRecords(t, s, web, number)
	if len(got.Records) != 2 || got.Records[1].Count != 1 {
		t.Fatalf("超过一小时应另起一条： %+v", got.Records)
	}
}

func Test附件下载每次单独记录并写明附件与访问者(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	web := s.Login("王工", "member-pass")
	number := mustCreateTicket(t, s, web, "附件访问记录")
	first := mustUploadAttachment(t, s, web, number, "现场.png", []byte("a"))
	second := mustUploadAttachment(t, s, web, number, "日志.log", []byte("b"))
	for _, id := range []string{first.ID, second.ID} {
		if res := claimByReply(t, s, web, number, map[string]any{"attachment_ids": []string{id}}); res.StatusCode != http.StatusCreated {
			t.Fatalf("挂附件状态码 = %d", res.StatusCode)
		}
	}

	download := func(c *http.Client, id, name string) {
		t.Helper()
		path := "/api/v1/attachments/" + id + "/" + url.PathEscape(name)
		if res := downloadAttachment(t, s, c, path); res.StatusCode != http.StatusOK {
			t.Fatalf("下载 %s 状态码 = %d", name, res.StatusCode)
		}
	}
	download(web, first.ID, "现场.png")
	_, token := s.IssueAgentToken(t, web, "笔记本")
	download(s.AgentClient(token), first.ID, "现场.png")

	link := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三"})
	guest := s.GuestClient(guestKeyOf(t, link))
	var view attachmentsTicketBody
	s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	if len(view.Attachments) != 2 {
		t.Fatalf("附件列表 = %+v", view.Attachments)
	}
	signed := view.Attachments[1].URL
	if res := downloadAttachment(t, s, nil, signed); res.StatusCode != http.StatusOK {
		t.Fatalf("签名下载状态码 = %d", res.StatusCode)
	}
	if res := downloadAttachment(t, s, nil, signed); res.StatusCode != http.StatusOK {
		t.Fatalf("第二次签名下载状态码 = %d", res.StatusCode)
	}

	download(admin, first.ID, "现场.png")
	got := readAccessRecords(t, s, web, number)
	var downloads []accessRecordBody
	for _, r := range got.Records {
		if r.Action == "download" {
			downloads = append(downloads, r)
		}
	}
	if len(downloads) != 5 {
		t.Fatalf("下载记录有 %d 条, 想 5： %+v", len(downloads), downloads)
	}
	wantActors := []string{"member", "agent", "guest", "guest", "admin"}
	wantNames := []string{"王工", "王工 的 Agent · 笔记本", "检验科-张三", "检验科-张三", "陈工"}
	for i, r := range downloads {
		if r.Actor.Kind != wantActors[i] || r.Actor.Name != wantNames[i] {
			t.Errorf("第 %d 条署名 = %+v, 想 %s/%s", i, r.Actor, wantActors[i], wantNames[i])
		}
		if r.Count != 1 {
			t.Errorf("下载 %d 的次数 = %d, 想 1", i, r.Count)
		}
	}
	if downloads[0].Attachment.ID != first.ID || downloads[0].Attachment.Filename != "现场.png" {
		t.Errorf("第一条下载附件不对：%+v", downloads[0].Attachment)
	}
	if downloads[2].Attachment.ID != second.ID || downloads[2].Attachment.Filename != "日志.log" {
		t.Errorf("访客下载附件不对：%+v", downloads[2].Attachment)
	}
}

func Test列表搜索收件箱通知与读取访问记录本身不产生记录(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	web := s.Login("王工", "member-pass")
	var created createTicketResponse
	res := s.Do(t, admin, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "不产生访问记录的工单", "labels": []string{"机型:UD-2300"}, "assignee": "王工",
	}, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d", res.StatusCode)
	}
	number := created.Number

	for _, path := range []string{
		"/api/v1/tickets?q=" + url.QueryEscape("不产生访问记录"),
		"/api/v1/tickets/counts?group_by=status",
		"/api/v1/inbox",
		"/api/v1/notifications",
	} {
		if res := s.Do(t, web, http.MethodGet, path, nil, nil); res.StatusCode != http.StatusOK {
			t.Fatalf("%s 状态码 = %d, 想 200", path, res.StatusCode)
		}
	}
	got := readAccessRecords(t, s, web, number)
	if len(got.Records) != 0 {
		t.Fatalf("非详情访问产生了记录：%+v", got.Records)
	}
	got = readAccessRecords(t, s, web, number)
	if len(got.Records) != 0 {
		t.Fatalf("读取访问记录本身产生了记录：%+v", got.Records)
	}
	if got.Since == "" {
		t.Error("空记录时应返回上线日期 since")
	}

	link := mustCreateGuestLink(t, s, admin, number, map[string]any{"note": "检验科-张三"})
	guest := s.GuestClient(guestKeyOf(t, link))
	guestRes := s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+number+"/access-records", nil, nil)
	if guestRes.StatusCode != http.StatusForbidden {
		t.Fatalf("访客读取访问记录状态码 = %d, 想 403", guestRes.StatusCode)
	}
	_, token := s.IssueAgentToken(t, web, "笔记本")
	if agentRes := s.Do(t, s.AgentClient(token), http.MethodGet, "/api/v1/tickets/"+number+"/access-records", nil, nil); agentRes.StatusCode != http.StatusOK {
		t.Fatalf("成员 Agent 读取访问记录状态码 = %d, 想 200", agentRes.StatusCode)
	}
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodPut} {
		res := s.Do(t, web, method, "/api/v1/tickets/"+number+"/access-records", map[string]any{}, nil)
		if res.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s 访问记录状态码 = %d, 想 405", method, res.StatusCode)
		}
	}
}

func Test访客链接已撤销或过期时记录尝试访问被拒(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, admin, "被拒访问记录")

	revoked := mustCreateGuestLink(t, s, admin, number, map[string]any{"note": "检验科-张三"})
	revokedKey := guestKeyOf(t, revoked)
	if res := s.Do(t, admin, http.MethodDelete,
		"/api/v1/tickets/"+number+"/guest-links/"+fmt.Sprint(revoked.ID), nil, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("撤销链接状态码 = %d", res.StatusCode)
	}
	if res := s.Do(t, s.GuestClient(revokedKey), http.MethodGet, "/api/v1/tickets/"+number, nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("撤销链接访问状态码 = %d, 想 401", res.StatusCode)
	}

	expired := mustCreateGuestLink(t, s, admin, number, map[string]any{"note": "检验科-李四", "valid_days": 7})
	expiredKey := guestKeyOf(t, expired)
	s.Clock().Advance(7 * 24 * time.Hour)
	admin = s.Login("陈工", "secret-pass") // 会话同样到期，重新登录后读取记录
	if res := s.Do(t, s.GuestClient(expiredKey), http.MethodGet, "/api/v1/tickets/"+number, nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("过期链接访问状态码 = %d, 想 401", res.StatusCode)
	}

	got := readAccessRecords(t, s, admin, number)
	if len(got.Records) != 2 {
		t.Fatalf("被拒记录有 %d 条, 想 2： %+v", len(got.Records), got.Records)
	}
	want := []struct{ note, reason string }{{"检验科-张三", "revoked"}, {"检验科-李四", "expired"}}
	for i, w := range want {
		r := got.Records[i]
		if r.Action != "denied" || r.Actor.Kind != "guest" || r.Actor.Name != w.note || r.Actor.EntryMethod != w.reason {
			t.Errorf("第 %d 条被拒记录不对：%+v, 想 %+v", i, r, w)
		}
		if r.Count != 1 {
			t.Errorf("被拒记录 %d 次数 = %d, 想 1", i, r.Count)
		}
	}
}

func Test撤销后的访客附件签名地址记录尝试访问被拒(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, admin, "签名地址被拒")
	att := mustUploadAttachment(t, s, admin, number, "现场.png", []byte("png"))
	if res := claimByReply(t, s, admin, number, map[string]any{"attachment_ids": []string{att.ID}}); res.StatusCode != http.StatusCreated {
		t.Fatalf("挂附件状态码 = %d", res.StatusCode)
	}

	link := mustCreateGuestLink(t, s, admin, number, map[string]any{"note": "检验科-张三"})
	guest := s.GuestClient(guestKeyOf(t, link))
	var view attachmentsTicketBody
	s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	signed := view.Attachments[0].URL
	if res := s.Do(t, admin, http.MethodDelete,
		"/api/v1/tickets/"+number+"/guest-links/"+fmt.Sprint(link.ID), nil, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("撤销链接状态码 = %d", res.StatusCode)
	}
	if res := downloadAttachment(t, s, nil, signed); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("撤销后签名地址状态码 = %d, 想 401", res.StatusCode)
	}

	got := readAccessRecords(t, s, admin, number)
	var denied []accessRecordBody
	for _, r := range got.Records {
		if r.Action == "denied" {
			denied = append(denied, r)
		}
	}
	if len(denied) != 1 || denied[0].Actor.Name != "检验科-张三" || denied[0].Actor.EntryMethod != "revoked" {
		t.Fatalf("签名地址被拒记录不对：%+v", denied)
	}
	if denied[0].Attachment.ID != att.ID || denied[0].Attachment.Filename != "现场.png" {
		t.Errorf("签名地址被拒记录未写明附件：%+v", denied[0].Attachment)
	}
}
