package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type ticketChangedBody struct {
	Error struct {
		Code       string `json:"code"`
		NewEvents  int64  `json:"new_events"`
		ThroughSeq int64  `json:"through_seq"`
	} `json:"error"`
}

func Test回复confirm_through_seq过旧时返回409(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	li := s.Login("李工", "secret-pass")
	number := mustCreateTicket(t, s, web, "固件升级")
	_, token := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(token)
	s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+number+"/assign",
		map[string]any{"assignee": "李工"}, nil)
	s.Do(t, li, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "预览之后的新变化。"}, nil)

	var conflict ticketChangedBody
	res := s.Do(t, agent, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "Agent 回复。", "confirm_through_seq": 1}, &conflict)
	if res.StatusCode != http.StatusConflict || conflict.Error.Code != "ticket_changed" {
		t.Fatalf("过旧 confirm_through_seq 回复 = %d/%+v, 想 409 ticket_changed", res.StatusCode, conflict)
	}
	if conflict.Error.NewEvents != 2 || conflict.Error.ThroughSeq != 3 {
		t.Fatalf("409 错误体 new_events/through_seq = %d/%d, 想 2/3",
			conflict.Error.NewEvents, conflict.Error.ThroughSeq)
	}

	var sent replyBody
	res = s.Do(t, agent, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "Agent 回复。", "confirm_through_seq": conflict.Error.ThroughSeq}, &sent)
	if res.StatusCode != http.StatusCreated || sent.ReplyNo != 2 || sent.TimelineSeq != 4 {
		t.Fatalf("带最新序号回复 = %d/%+v, 想 201/第 2 条/seq 4", res.StatusCode, sent)
	}

	res = s.Do(t, agent, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "再次回复。"}, &sent)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("不带 confirm_through_seq 回复 = %d, 想 201（行为不变）", res.StatusCode)
	}
}

func Test修订说明confirm_through_seq过旧时返回409(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	li := s.Login("李工", "secret-pass")
	number := mustCreateTicket(t, s, web, "固件升级")
	_, token := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(token)
	s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+number+"/assign",
		map[string]any{"assignee": "李工"}, nil)
	s.Do(t, li, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "预览之后的新变化。"}, nil)

	var conflict ticketChangedBody
	res := s.Do(t, agent, http.MethodPatch, "/api/v1/tickets/"+number,
		map[string]any{"brief_md": "Agent 修订后的说明。", "base_revision": 1, "confirm_through_seq": 1}, &conflict)
	if res.StatusCode != http.StatusConflict || conflict.Error.Code != "ticket_changed" {
		t.Fatalf("过旧 confirm_through_seq 修订 = %d/%+v, 想 409 ticket_changed", res.StatusCode, conflict)
	}
	if conflict.Error.NewEvents != 2 || conflict.Error.ThroughSeq != 3 {
		t.Fatalf("409 错误体 new_events/through_seq = %d/%d, 想 2/3",
			conflict.Error.NewEvents, conflict.Error.ThroughSeq)
	}

	var revised struct {
		Revision int64 `json:"revision"`
	}
	res = s.Do(t, agent, http.MethodPatch, "/api/v1/tickets/"+number,
		map[string]any{"brief_md": "Agent 修订后的说明。", "base_revision": 1,
			"confirm_through_seq": conflict.Error.ThroughSeq}, &revised)
	if res.StatusCode != http.StatusOK || revised.Revision != 2 {
		t.Fatalf("带最新序号修订 = %d/%+v, 想 200/第 2 版", res.StatusCode, revised)
	}

	res = s.Do(t, agent, http.MethodPatch, "/api/v1/tickets/"+number,
		map[string]any{"brief_md": "又一次修订。", "base_revision": 2}, &revised)
	if res.StatusCode != http.StatusOK || revised.Revision != 3 {
		t.Fatalf("不带 confirm_through_seq 修订 = %d/%+v, 想 200/第 3 版（行为不变）", res.StatusCode, revised)
	}
}

func Test直接回复署名不带经字样(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "固件升级")
	_, token := s.IssueAgentToken(t, web, "笔记本")

	var got replyBody
	res := s.Do(t, s.AgentClient(token), http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "直接提交的回复。"}, &got)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("直接回复状态码 = %d, 想 201", res.StatusCode)
	}
	if got.Author != "陈工 的 Agent · 笔记本" {
		t.Fatalf("直接回复署名 = %q, 想 陈工 的 Agent · 笔记本", got.Author)
	}
	if strings.Contains(got.Author, "经") {
		t.Fatalf("直接回复署名不应带「经」字样：%q", got.Author)
	}
}
