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

	var assigned map[string]any
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &assigned)
	if _, ok := assigned["handoff_share_text"]; !ok {
		t.Fatal("成员 JSON 应出现 handoff_share_text 键")
	}
	if assigned["handoff_share_text"] == nil {
		t.Fatal("有指派人 handoff_share_text 不应为 null")
	}

	var unassignedCreated createAssignedResponse
	res = s.Do(t, web, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "未指派", "brief_md": "……", "labels": []string{"机型:UD-2300"},
	}, &unassignedCreated)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("未指派建单状态码 = %d, 想 201", res.StatusCode)
	}
	var unassigned map[string]any
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+unassignedCreated.Number, nil, &unassigned)
	if unassigned["handoff_text"] == "" || unassigned["handoff_share_text"] == nil {
		t.Fatalf("未指派 handoff_text/handoff_share_text = %v/%v, 想 Agent 段/中性交接文案",
			unassigned["handoff_text"], unassigned["handoff_share_text"])
	}
	if _, ok := unassigned["handoff_share_text"]; !ok {
		t.Fatal("未指派成员 JSON 也应出现 handoff_share_text 键")
	}
	if share, ok := unassigned["handoff_share_text"].(string); !ok ||
		!strings.HasPrefix(share, "请让你的 Agent 处理工单 "+unassignedCreated.Number+"「未指派」") {
		t.Fatalf("未指派 handoff_share_text = %v, 想不点名的中性交接文案", unassigned["handoff_share_text"])
	}

	var guest map[string]any
	s.Do(t, s.GuestClient(key), http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &guest)
	for _, field := range []string{"handoff_text"} {
		if _, ok := guest[field]; ok {
			t.Errorf("访客 JSON 不应出现 %s 字段", field)
		}
	}
	for _, field := range []string{"handoff_share_text", "guest_agent_instruction"} {
		if _, ok := guest[field]; !ok {
			t.Errorf("访客 JSON 应出现 %s 字段", field)
		}
	}
	guestShare, _ := guest["handoff_share_text"].(string)
	guestInstruction, _ := guest["guest_agent_instruction"].(string)
	if !strings.HasPrefix(guestShare, "请让你的 Agent 处理工单 "+created.Number+"「已指派」") {
		t.Fatalf("访客 handoff_share_text = %q, 想与成员一致的中性交接文案", guestShare)
	}
	if !strings.Contains(guestInstruction, "{GUEST_LINK_URL}") ||
		!strings.Contains(guestInstruction, "访问码：<请填入你的 6 位访问码>") ||
		!strings.Contains(guestInstruction, "Authorization: Bearer tbg_<链接密钥>:<访问码>") {
		t.Fatalf("访客 guest_agent_instruction 缺少链接占位、访问码占位或鉴权说明：\n%q", guestInstruction)
	}
	for name, secret := range map[string]string{"成员 token": token, "访客密钥": key} {
		if strings.Contains(guestShare, secret) || strings.Contains(guestInstruction, secret) {
			t.Errorf("%s 泄露进访客交接文案", name)
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
