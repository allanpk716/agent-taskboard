package server_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test访客与Markdown不出现交接字段(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("王工", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	web := s.Login("王工", "secret-pass")
	_, token := s.IssueAgentToken(t, web, "笔记本")

	var created createAssignedResponse
	res := s.Do(t, web, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "已指派", "brief_md": "……", "labels": []string{"机型:UD-2300"}, "assignee": "李工",
	}, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d, 想 201", res.StatusCode)
	}
	link := s.CreateGuestLink(t, web, created.Number, map[string]any{"note": "检验科-张三"})
	key := link.Key(t)

	for name, text := range map[string]string{"成员 token": token, "访客密钥": key} {
		if strings.Contains(*created.ShareText, text) {
			t.Errorf("%s 泄露进 share_text", name)
		}
	}

	var assigned ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &assigned)
	if assigned.HandoffShareText == nil {
		t.Fatal("有指派人时 handoff_share_text 不应为 null")
	}

	var unassigned ticketResponseBody
	res = s.Do(t, web, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "未指派", "brief_md": "……", "labels": []string{"机型:UD-2300"},
	}, &unassigned)
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+unassigned.Number, nil, &unassigned)
	if unassigned.HandoffText == "" || unassigned.HandoffShareText != nil {
		t.Fatalf("未指派 handoff_text/handoff_share_text = %q/%v, 想 Agent 段/null",
			unassigned.HandoffText, unassigned.HandoffShareText)
	}

	var guest map[string]any
	s.Do(t, s.GuestClient(key), http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &guest)
	for _, field := range []string{"handoff_text", "handoff_share_text"} {
		if _, ok := guest[field]; ok {
			t.Errorf("访客 JSON 不应出现 %s 字段", field)
		}
	}

	res = s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number+"?format=markdown", nil, nil)
	md, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(md), "请处理 agent-taskboard 上的工单") {
		t.Errorf("Markdown 视图不应加入交接文案：\n%s", string(md))
	}
}
