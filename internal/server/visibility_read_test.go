package server_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func responseBody(t testing.TB, res *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("读响应体失败：%v", err)
	}
	return string(body)
}

func Test可见范围外成员与Agent读取工单得到一致的404并留下被拒记录(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("管理员", "secret-pass")
	s.CreateMember("创建者", "secret-pass")
	s.CreateMember("旁观者", "secret-pass")
	admin := s.Login("管理员", "secret-pass")
	creator := s.Login("创建者", "secret-pass")
	outsider := s.Login("旁观者", "secret-pass")
	_, token := s.IssueAgentToken(t, outsider, "旁观的 Agent")
	agent := s.AgentClient(token)

	var created createTicketResponse
	res := s.Do(t, creator, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "商业敏感工单", "labels": []string{"机型:UD-2300"}, "assignee": "创建者",
	}, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d", res.StatusCode)
	}
	number := created.Number
	att := mustUploadAttachment(t, s, creator, number, "内部资料.txt", []byte("secret"))
	if res := claimByReply(t, s, creator, number, map[string]any{"attachment_ids": []string{att.ID}}); res.StatusCode != http.StatusCreated {
		t.Fatalf("挂附件状态码 = %d", res.StatusCode)
	}
	attPath := "/api/v1/attachments/" + att.ID + "/" + url.PathEscape("内部资料.txt")

	assertSame404 := func(name, hiddenPath, missingPath string, clients map[string]*http.Client) {
		t.Helper()
		for credential, client := range clients {
			hidden := s.Do(t, client, http.MethodGet, hiddenPath, nil, nil)
			if hidden.StatusCode != http.StatusNotFound {
				t.Fatalf("%s %s 状态码 = %d, 想 404", credential, name, hidden.StatusCode)
			}
			missing := s.Do(t, client, http.MethodGet, missingPath, nil, nil)
			if missing.StatusCode != http.StatusNotFound {
				t.Fatalf("%s 不存在 %s 状态码 = %d, 想 404", credential, name, missing.StatusCode)
			}
			if got, want := responseBody(t, hidden), responseBody(t, missing); got != want {
				t.Fatalf("%s %s 的 404 响应不一致：\n%q\n%q", credential, name, got, want)
			}
		}
	}
	clients := map[string]*http.Client{"成员": outsider, "成员Agent": agent}
	assertSame404("详情", "/api/v1/tickets/"+number, "/api/v1/tickets/20990101-001", clients)
	assertSame404("时间线", "/api/v1/tickets/"+number+"/timeline", "/api/v1/tickets/20990101-001/timeline", clients)
	assertSame404("访问记录", "/api/v1/tickets/"+number+"/access-records", "/api/v1/tickets/20990101-001/access-records", clients)
	assertSame404("附件", attPath, "/api/v1/attachments/att_999999/%E5%86%85%E9%83%A8%E8%B5%84%E6%96%99.txt", clients)

	got := readAccessRecords(t, s, admin, number)
	var denied []accessRecordBody
	for _, r := range got.Records {
		if r.Action == "denied" {
			denied = append(denied, r)
		}
	}
	if len(denied) != 8 {
		t.Fatalf("被拒记录有 %d 条, 想 8：%+v", len(denied), denied)
	}
	for _, r := range denied {
		if r.Actor.Kind != "member" && r.Actor.Kind != "agent" {
			t.Errorf("被拒记录访问者类型 = %q, 想 member/agent", r.Actor.Kind)
		}
		if !strings.Contains(r.Actor.Name, "旁观者") || r.Count != 1 {
			t.Errorf("被拒记录署名/次数不对：%+v", r)
		}
	}
}

func Test管理员能读全部工单且查看照常记录(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("管理员", "secret-pass")
	s.CreateMember("创建者", "secret-pass")
	admin := s.Login("管理员", "secret-pass")
	creator := s.Login("创建者", "secret-pass")
	number := mustCreateTicket(t, s, creator, "管理员兜底")

	var got ticketResponseBody
	if res := s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number, nil, &got); res.StatusCode != http.StatusOK {
		t.Fatalf("管理员读工单状态码 = %d", res.StatusCode)
	}
	if got.Title != "管理员兜底" {
		t.Fatalf("管理员读到 %q", got.Title)
	}
	records := readAccessRecords(t, s, creator, number)
	if len(records.Records) != 1 {
		t.Fatalf("管理员查看记录 = %+v, 想 1 条", records.Records)
	}
	r := records.Records[0]
	if r.Action != "view" || r.Actor.Kind != "admin" || !r.Actor.IsAdmin || r.Actor.Name != "管理员" {
		t.Fatalf("管理员查看记录不对：%+v", r)
	}
}

func Test列表搜索计数收件箱只包含可见工单(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("管理员", "secret-pass")
	for _, name := range []string{"创建者", "指派人", "协作者", "初筛人", "旁观者"} {
		s.CreateMember(name, "secret-pass")
	}
	admin := s.Login("管理员", "secret-pass")
	creator := s.Login("创建者", "secret-pass")
	assignee := s.Login("指派人", "secret-pass")
	collaborator := s.Login("协作者", "secret-pass")
	triager := s.Login("初筛人", "secret-pass")
	outsider := s.Login("旁观者", "secret-pass")
	mustSetTriager(t, s, admin, 5, true)

	hidden := mustCreateTicket(t, s, creator, "旁观者不该搜到")
	if res := addCollaborator(t, s, creator, hidden, "协作者"); res.StatusCode != http.StatusCreated {
		t.Fatalf("加入协作者状态码 = %d", res.StatusCode)
	}
	if res := s.Do(t, creator, http.MethodPost, "/api/v1/tickets/"+hidden+"/assign", map[string]string{"assignee": "指派人"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("指派状态码 = %d", res.StatusCode)
	}
	var ownCreated createTicketResponse
	if res := s.Do(t, outsider, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "旁观者自己的工单", "labels": []string{"机型:UD-2300"}, "assignee": "旁观者",
	}, &ownCreated); res.StatusCode != http.StatusCreated {
		t.Fatalf("创建自己的工单状态码 = %d", res.StatusCode)
	}
	own := ownCreated.Number

	checkHidden := func(name string, c *http.Client, path string) {
		t.Helper()
		var body struct {
			Items []struct {
				Number string `json:"number"`
			} `json:"items"`
			Total int64 `json:"total"`
		}
		if res := s.Do(t, c, http.MethodGet, path, nil, &body); res.StatusCode != http.StatusOK {
			t.Fatalf("%s 状态码 = %d", name, res.StatusCode)
		}
		if body.Total != 1 || len(body.Items) != 1 || body.Items[0].Number != own {
			t.Fatalf("%s = %+v, 只想包含 %s", name, body, own)
		}
	}
	checkHidden("列表", outsider, "/api/v1/tickets?status=all")
	checkHidden("搜索", outsider, "/api/v1/tickets?status=all&q="+url.QueryEscape("自己的工单"))

	var counts struct {
		Total int64 `json:"total"`
	}
	if res := s.Do(t, outsider, http.MethodGet, "/api/v1/tickets/counts?group_by=status&status=all", nil, &counts); res.StatusCode != http.StatusOK {
		t.Fatalf("计数状态码 = %d", res.StatusCode)
	}
	if counts.Total != 1 {
		t.Fatalf("计数 total = %d, 想 1", counts.Total)
	}

	var inbox inboxBody
	if res := s.Do(t, outsider, http.MethodGet, "/api/v1/inbox", nil, &inbox); res.StatusCode != http.StatusOK {
		t.Fatalf("收件箱状态码 = %d", res.StatusCode)
	}
	if inbox.Count != 1 || inbox.Items[0].Number != own {
		t.Fatalf("旁观者收件箱 = %+v, 只想包含 %s", inbox.Items, own)
	}

	for name, c := range map[string]*http.Client{"创建者": creator, "指派人": assignee, "协作者": collaborator, "管理员": admin} {
		var list struct {
			Items []struct {
				Number string `json:"number"`
			} `json:"items"`
		}
		if res := s.Do(t, c, http.MethodGet, "/api/v1/tickets?status=all", nil, &list); res.StatusCode != http.StatusOK {
			t.Fatalf("%s 列表状态码 = %d", name, res.StatusCode)
		}
		found := false
		for _, item := range list.Items {
			if item.Number == hidden {
				found = true
			}
		}
		if !found {
			t.Errorf("%s 应看到工单 %s", name, hidden)
		}
	}
	_ = triager
}

func Test初筛人只可见未指派工单且前任指派人离开可见范围(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("管理员", "secret-pass")
	s.CreateMember("创建者", "secret-pass")
	s.CreateMember("初筛人", "secret-pass")
	s.CreateMember("旧指派人", "secret-pass")
	s.CreateMember("新指派人", "secret-pass")
	admin := s.Login("管理员", "secret-pass")
	creator := s.Login("创建者", "secret-pass")
	triager := s.Login("初筛人", "secret-pass")
	old := s.Login("旧指派人", "secret-pass")
	mustSetTriager(t, s, admin, 3, true)

	unassigned := mustCreateTicket(t, s, creator, "等待初筛")
	if res := s.Do(t, triager, http.MethodGet, "/api/v1/tickets/"+unassigned, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("初筛人读未指派工单状态码 = %d", res.StatusCode)
	}
	assigned := mustCreateTicket(t, s, creator, "已离开可见范围")
	if res := s.Do(t, creator, http.MethodPost, "/api/v1/tickets/"+assigned+"/assign", map[string]string{"assignee": "旧指派人"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("初次指派状态码 = %d", res.StatusCode)
	}
	if res := s.Do(t, creator, http.MethodPost, "/api/v1/tickets/"+assigned+"/assign", map[string]string{"assignee": "新指派人"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("改派状态码 = %d", res.StatusCode)
	}
	for name, path := range map[string]string{
		"前任指派人":   "/api/v1/tickets/" + assigned,
		"指派后的初筛人": "/api/v1/tickets/" + unassigned,
	} {
		if name == "指派后的初筛人" {
			s.Do(t, creator, http.MethodPost, "/api/v1/tickets/"+unassigned+"/assign", map[string]string{"assignee": "新指派人"}, nil)
		}
		client := old
		if name == "指派后的初筛人" {
			client = triager
		}
		if res := s.Do(t, client, http.MethodGet, path, nil, nil); res.StatusCode != http.StatusNotFound {
			t.Fatalf("%s状态码 = %d, 想 404", name, res.StatusCode)
		}
	}
}
