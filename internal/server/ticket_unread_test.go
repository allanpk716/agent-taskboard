package server_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test工单未读点跟随本人亲自操作Agent操作与详情读取(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("甲", "secret-pass")
	s.CreateMember("乙", "secret-pass")
	s.CreateMember("丙", "secret-pass")
	jia := s.Login("甲", "secret-pass")
	yi := s.Login("乙", "secret-pass")
	bing := s.Login("丙", "secret-pass")
	_, yiAgentToken := s.IssueAgentToken(t, yi, "乙的 Agent")
	yiAgent := s.AgentClient(yiAgentToken)

	number := createAssignedForUnread(t, s, jia, "未读点主流程", "乙")
	unread := func(c *http.Client, title string) bool {
		t.Helper()
		body := listByTitle(t, s, c, title)
		if len(body.Items) != 1 {
			t.Fatalf("%s 的列表结果 = %+v", title, body.Items)
		}
		return body.Items[0].Unread
	}
	read := func(c *http.Client) {
		t.Helper()
		if res := s.Do(t, c, http.MethodGet, "/api/v1/tickets/"+number, nil, nil); res.StatusCode != http.StatusOK {
			t.Fatalf("读取详情状态码 = %d", res.StatusCode)
		}
	}
	reply := func(c *http.Client, body string) {
		t.Helper()
		if res := s.Do(t, c, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
			map[string]string{"body_md": body}, nil); res.StatusCode != http.StatusCreated {
			t.Fatalf("回复状态码 = %d", res.StatusCode)
		}
	}

	// 建单后的指派事件是甲操作，乙先看到未读；打开详情即读穿到当前事件。
	if !unread(yi, "未读点主流程") {
		t.Fatal("别人指派后应显示未读")
	}
	read(yi)
	if unread(yi, "未读点主流程") {
		t.Fatal("打开详情后不应显示未读")
	}

	reply(jia, "甲补充了情况")
	if !unread(yi, "未读点主流程") {
		t.Fatal("别人回复后应显示未读")
	}
	var inbox inboxBody
	if res := s.Do(t, yi, http.MethodGet, "/api/v1/inbox", nil, &inbox); res.StatusCode != http.StatusOK {
		t.Fatalf("收件箱状态码 = %d", res.StatusCode)
	}
	if item := inbox.itemByNumber(number); item == nil || !item.Unread {
		t.Fatalf("收件箱未读标记不对：%+v", inbox.Items)
	}
	read(yi)

	reply(yi, "乙本人回复")
	if unread(yi, "未读点主流程") {
		t.Fatal("本人亲自回复不应让自己看到未读")
	}

	reply(yiAgent, "乙的 Agent 回复")
	if !unread(yi, "未读点主流程") {
		t.Fatal("本人名下 Agent 回复应让本人看到未读")
	}
	read(yiAgent)
	if !unread(yi, "未读点主流程") {
		t.Fatal("Agent 读详情不应替主人标已读")
	}
	read(yi)

	if status, _ := setLabels(t, s, jia, number, setLabelsBody{
		Add: []string{"紧急"}, AllowNew: true,
	}); status != http.StatusOK {
		t.Fatalf("标签变更状态码 = %d", status)
	}
	if unread(yi, "未读点主流程") {
		t.Fatal("标签变更不应产生未读")
	}

	// 可见范围外成员的列表只含自己的工单，不因未读计算泄露隐藏工单。
	own := createAssignedForUnread(t, s, bing, "丙自己的工单", "丙")
	hidden := listByTitle(t, s, bing, "未读点主流程")
	ownList := listByTitle(t, s, bing, "丙自己的工单")
	if len(hidden.Items) != 0 || len(ownList.Items) != 1 || ownList.Items[0].Number != own {
		t.Fatalf("可见范围过滤结果不对：%+v / %+v", hidden.Items, ownList.Items)
	}
}

func Test客户端凭证不能读详情也不改未读(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("甲", "secret-pass")
	s.CreateMember("乙", "secret-pass")
	jia := s.Login("甲", "secret-pass")
	yi := s.Login("乙", "secret-pass")
	number := createAssignedForUnread(t, s, jia, "客户端读取详情", "乙")
	if res := s.Do(t, yi, http.MethodGet, "/api/v1/tickets/"+number, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("网页打开详情状态码 = %d", res.StatusCode)
	}
	if res := s.Do(t, jia, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "等待客户端读取"}, nil); res.StatusCode != http.StatusCreated {
		t.Fatal("造未读回复失败")
	}
	issued := s.IssueClientCredential(t, yi, "WORKSTATION-01")
	client := s.ClientCredentialClient(issued.Token)
	// 客户端凭证不能读工单详情（ADR 0004），点通知是在浏览器里用网页会话打开。
	if res := s.Do(t, client, http.MethodGet, "/api/v1/tickets/"+number, nil, nil); res.StatusCode != http.StatusForbidden {
		t.Fatalf("客户端读取详情状态码 = %d，想 403", res.StatusCode)
	}
	if got := listByTitle(t, s, yi, "客户端读取详情").Items[0].Unread; !got {
		t.Fatal("客户端凭证被拒后不应改变未读状态")
	}
}

func Test上线基线之前的工单视为已读(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("甲", "secret-pass")
	s.CreateMember("乙", "secret-pass")
	jia := s.Login("甲", "secret-pass")
	yi := s.Login("乙", "secret-pass")
	number := createAssignedForUnread(t, s, jia, "上线前的旧工单", "乙")
	if _, err := s.Store().DB.Exec(
		`UPDATE ticket_read_baseline SET last_read_seq =
		  (SELECT COALESCE(MAX(change_seq), 0) FROM timeline_events)`); err != nil {
		t.Fatalf("模拟上线基线失败：%v", err)
	}
	if got := listByTitle(t, s, yi, "上线前的旧工单").Items[0].Unread; got {
		t.Fatal("基线之前的旧工单不应显示未读")
	}
	if res := s.Do(t, jia, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "上线后的新事件"}, nil); res.StatusCode != http.StatusCreated {
		t.Fatal("造上线后回复失败")
	}
	if got := listByTitle(t, s, yi, "上线前的旧工单").Items[0].Unread; !got {
		t.Fatal("基线之后的新回复应显示未读")
	}
}

func createAssignedForUnread(t testing.TB, s *tbtest.Server, c *http.Client, title, assignee string) string {
	t.Helper()
	var created createAssignedResponse
	res := s.Do(t, c, http.MethodPost, "/api/v1/tickets", map[string]any{
		"kind": "task", "title": title, "brief_md": "说明", "labels": []string{"机型:UD-2300"},
		"assignee": assignee,
	}, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单 %s 状态码 = %d", title, res.StatusCode)
	}
	return created.Number
}

func listByTitle(t testing.TB, s *tbtest.Server, c *http.Client, title string) listBody {
	t.Helper()
	var body listBody
	res := s.Do(t, c, http.MethodGet, "/api/v1/tickets?status=all&q="+url.QueryEscape(title), nil, &body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("列表 %s 状态码 = %d", title, res.StatusCode)
	}
	return body
}
