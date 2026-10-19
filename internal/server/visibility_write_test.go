package server_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test可见范围外成员与Agent写入工单得到一致的404无副作用并留下被拒记录(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("管理员", "secret-pass")
	s.CreateMember("创建者", "secret-pass")
	s.CreateMember("指派人", "secret-pass")
	s.CreateMember("旁观者", "secret-pass")
	admin := s.Login("管理员", "secret-pass")
	creator := s.Login("创建者", "secret-pass")
	outsider := s.Login("旁观者", "secret-pass")
	_, token := s.IssueAgentToken(t, outsider, "旁观的 Agent")
	agent := s.AgentClient(token)

	var created createTicketResponse
	res := s.Do(t, creator, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "成员写不入的商业敏感工单", "labels": []string{"机型:UD-2300"}, "assignee": "指派人",
	}, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d", res.StatusCode)
	}
	number := created.Number
	s.CreateGuestLink(t, creator, number, map[string]any{"note": "检验科-张三"})

	doUpload := func(c *http.Client, ref string) *http.Response {
		t.Helper()
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, err := mw.CreateFormFile("file", "内部资料.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = fw.Write([]byte("secret")); err != nil {
			t.Fatal(err)
		}
		if err = mw.Close(); err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, s.URL()+"/api/v1/tickets/"+ref+"/attachments", &body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", mw.FormDataContentType())
		got, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { got.Body.Close() })
		return got
	}

	routes := []struct {
		name   string
		method string
		path   func(ref string) string
		body   any
		upload bool
	}{
		{name: "回复", method: http.MethodPost, path: func(ref string) string { return "/api/v1/tickets/" + ref + "/replies" }, body: map[string]any{"body_md": "越权回复"}},
		{name: "修订说明", method: http.MethodPatch, path: func(ref string) string { return "/api/v1/tickets/" + ref }, body: map[string]any{"brief_md": "越权修订", "base_revision": int64(1)}},
		{name: "关闭", method: http.MethodPost, path: func(ref string) string { return "/api/v1/tickets/" + ref + "/close" }, body: map[string]any{"reason": "done"}},
		{name: "重开", method: http.MethodPost, path: func(ref string) string { return "/api/v1/tickets/" + ref + "/reopen" }},
		{name: "指派", method: http.MethodPost, path: func(ref string) string { return "/api/v1/tickets/" + ref + "/assign" }, body: map[string]any{"assignee": "旁观者"}},
		{name: "标签", method: http.MethodPost, path: func(ref string) string { return "/api/v1/tickets/" + ref + "/labels" }, body: map[string]any{"add": []string{"机型:UD-2300"}}},
		{name: "加入协作者", method: http.MethodPost, path: func(ref string) string { return "/api/v1/tickets/" + ref + "/collaborators" }, body: map[string]any{"member": "旁观者"}},
		{name: "移出协作者", method: http.MethodDelete, path: func(ref string) string { return "/api/v1/tickets/" + ref + "/collaborators/4" }},
		{name: "退出协作者", method: http.MethodPost, path: func(ref string) string { return "/api/v1/tickets/" + ref + "/collaborators/leave" }},
		{name: "创建访客链接", method: http.MethodPost, path: func(ref string) string { return "/api/v1/tickets/" + ref + "/guest-links" }, body: map[string]any{"note": "越权链接"}},
		{name: "列出访客链接", method: http.MethodGet, path: func(ref string) string { return "/api/v1/tickets/" + ref + "/guest-links" }},
		{name: "撤销访客链接", method: http.MethodDelete, path: func(ref string) string { return "/api/v1/tickets/" + ref + "/guest-links/1" }},
		{name: "延期访客链接", method: http.MethodPost, path: func(ref string) string { return "/api/v1/tickets/" + ref + "/guest-links/1/extend" }, body: map[string]any{"valid_days": 90}},
		{name: "重置访问码", method: http.MethodPost, path: func(ref string) string { return "/api/v1/tickets/" + ref + "/guest-links/1/reset-access-code" }},
		{name: "上传附件", method: http.MethodPost, path: func(ref string) string { return "/api/v1/tickets/" + ref + "/attachments" }, upload: true},
	}

	for _, route := range routes {
		for credential, client := range map[string]*http.Client{"成员": outsider, "成员Agent": agent} {
			var hidden, missing *http.Response
			if route.upload {
				hidden = doUpload(client, number)
				missing = doUpload(client, "20990101-001")
			} else {
				hidden = s.Do(t, client, route.method, route.path(number), route.body, nil)
				missing = s.Do(t, client, route.method, route.path("20990101-001"), route.body, nil)
			}
			if hidden.StatusCode != http.StatusNotFound {
				t.Fatalf("%s %s %s 状态码 = %d, 想 404", credential, route.name, route.method, hidden.StatusCode)
			}
			if got, want := responseBody(t, hidden), responseBody(t, missing); got != want {
				t.Fatalf("%s %s 的 404 响应不一致：\n%q\n%q", credential, route.name, got, want)
			}
		}
	}

	records := readAccessRecords(t, s, admin, number)
	denied := 0
	for _, record := range records.Records {
		if record.Action != "denied" {
			continue
		}
		denied++
		if record.Count != 1 {
			t.Fatalf("被拒记录次数 = %+v, 想 1", record)
		}
	}
	if denied != len(routes)*2 {
		t.Fatalf("被拒记录有 %d 条, 想 %d", denied, len(routes)*2)
	}
}

func Test离开可见范围成员只收到最后一条不带摘要的通知(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("管理员", "secret-pass")
	for _, name := range []string{"创建者", "旧指派人", "新指派人"} {
		s.CreateMember(name, "secret-pass")
	}
	creator := s.Login("创建者", "secret-pass")
	old := s.Login("旧指派人", "secret-pass")
	number := mustCreateTicket(t, s, creator, "改派后的旧指派人")
	if res := s.Do(t, creator, http.MethodPost, "/api/v1/tickets/"+number+"/assign",
		map[string]any{"assignee": "旧指派人"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("初次指派状态码 = %d", res.StatusCode)
	}
	if res := s.Do(t, creator, http.MethodPost, "/api/v1/tickets/"+number+"/assign",
		map[string]any{"assignee": "新指派人"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("改派状态码 = %d", res.StatusCode)
	}
	if res := s.Do(t, creator, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "旧指派人看不到的后续内容"}, nil); res.StatusCode != http.StatusCreated {
		t.Fatalf("后续回复状态码 = %d", res.StatusCode)
	}
	if res := s.Do(t, creator, http.MethodPost, "/api/v1/tickets/"+number+"/close",
		map[string]any{"reason": "done"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭状态码 = %d", res.StatusCode)
	}

	var notes notificationsBody
	if res := s.Do(t, old, http.MethodGet, "/api/v1/notifications", nil, &notes); res.StatusCode != http.StatusOK {
		t.Fatalf("通知状态码 = %d", res.StatusCode)
	}
	if len(notes.Items) != 2 {
		t.Fatalf("旧指派人通知 = %+v, 想 2 条", notes.Items)
	}
	last := notes.Items[0]
	if last.Reason != "unassigned" || last.Ticket.Title != "改派后的旧指派人" || last.Summary != "" {
		t.Fatalf("离开可见范围通知不对：%+v", last)
	}
	if !strings.Contains(last.Text, "改派后的旧指派人") {
		t.Fatalf("离开可见范围通知应带标题：%+v", last)
	}
	if res := s.Do(t, old, http.MethodGet, "/api/v1/tickets/"+number, nil, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("旧指派人读取状态码 = %d, 想 404", res.StatusCode)
	}
}
