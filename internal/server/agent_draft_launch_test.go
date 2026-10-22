package server_test

import (
	"net/http"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test不带draft_mode签发AgentToken默认必须走草稿(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("chen", "secret-pass")
	web := s.Login("chen", "secret-pass")

	var issued struct {
		DraftMode string `json:"draft_mode"`
	}
	res := s.Do(t, web, http.MethodPost, "/api/v1/me/agent-tokens",
		map[string]string{"name": "笔记本"}, &issued)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("签发状态码 = %d, 想 201", res.StatusCode)
	}
	if issued.DraftMode != "required" {
		t.Fatalf("默认 draft_mode = %q, 想 required", issued.DraftMode)
	}
}

func Test草稿上线提示只给有名下Agent的网页成员一次(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("chen", "secret-pass")
	s.CreateMember("li", "secret-pass")
	s.CreateMember("zhao", "secret-pass")
	webChen := s.Login("chen", "secret-pass")
	webLi := s.Login("li", "secret-pass")
	webZhao := s.Login("zhao", "secret-pass")
	s.IssueAgentToken(t, webChen, "笔记本")

	var withAgent struct {
		Notices []string `json:"notices"`
	}
	s.Do(t, webChen, http.MethodGet, "/api/v1/session", nil, &withAgent)
	if len(withAgent.Notices) != 1 || withAgent.Notices[0] != "drafts_intro" {
		t.Fatalf("有名下 Agent 的 session notices = %#v, 想 [drafts_intro]", withAgent.Notices)
	}

	var withoutAgent struct {
		Notices []string `json:"notices"`
	}
	s.Do(t, webLi, http.MethodGet, "/api/v1/session", nil, &withoutAgent)
	if len(withoutAgent.Notices) != 0 {
		t.Fatalf("没有名下 Agent 的 session notices = %#v, 想为空", withoutAgent.Notices)
	}

	if res := s.Do(t, webChen, http.MethodPost, "/api/v1/me/notices/drafts_intro/seen", nil, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("标记提示已读状态码 = %d, 想 204", res.StatusCode)
	}
	if res := s.Do(t, webChen, http.MethodPost, "/api/v1/me/notices/drafts_intro/seen", nil, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("重复标记提示已读状态码 = %d, 想 204", res.StatusCode)
	}
	var afterSeen struct {
		Notices []string `json:"notices"`
	}
	s.Do(t, webChen, http.MethodGet, "/api/v1/session", nil, &afterSeen)
	if len(afterSeen.Notices) != 0 {
		t.Fatalf("确认后 session notices = %#v, 想为空", afterSeen.Notices)
	}

	_, token := s.IssueAgentToken(t, webZhao, "工位")
	var agentSession struct {
		Notices []string `json:"notices"`
	}
	s.Do(t, s.AgentClient(token), http.MethodGet, "/api/v1/session", nil, &agentSession)
	if len(agentSession.Notices) != 0 {
		t.Fatalf("Agent session notices = %#v, 想为空", agentSession.Notices)
	}
	var zhao struct {
		Notices []string `json:"notices"`
	}
	s.Do(t, webZhao, http.MethodGet, "/api/v1/session", nil, &zhao)
	if len(zhao.Notices) != 1 || zhao.Notices[0] != "drafts_intro" {
		t.Fatalf("新签发 Agent 后 session notices = %#v, 想 [drafts_intro]", zhao.Notices)
	}
}
