package server_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// replyBody 是 POST …/replies 的响应结构。
type replyBody struct {
	ReplyNo     int64  `json:"reply_no"`
	TimelineSeq int64  `json:"timeline_seq"`
	Deliver     bool   `json:"deliver"`
	Author      string `json:"author"`
	WaitingOn   struct {
		Party  string  `json:"party"`
		Member *string `json:"member"`
		Text   string  `json:"text"`
		Reason string  `json:"reason"`
	} `json:"waiting_on"`
}

// reply 发一条回复，返回原始响应。
func reply(t testing.TB, s *tbtest.Server, c *http.Client, ref, bodyMD string) *http.Response {
	t.Helper()
	return s.Do(t, c, http.MethodPost, "/api/v1/tickets/"+ref+"/replies",
		map[string]string{"body_md": bodyMD}, nil)
}

// mustCreateTicket 建一张工单并返回其编号。
func mustCreateTicket(t testing.TB, s *tbtest.Server, c *http.Client, title string) string {
	t.Helper()
	res := createTicket(t, s, c, createTicketBody{Title: title, Labels: []string{"机型:UD-2300"}})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单 %q 状态码 = %d, 想 201", title, res.StatusCode)
	}
	var created createTicketResponse
	tbtest.DecodeJSON(t, res, &created)
	return created.Number
}

// 成员和成员 Agent 都能回复：响应带回复号、时间线序号、署名和等待方；
// 回复记入时间线，每条都带作者和时间，并推进全局变更序号。
func Test成员与Agent回复记入时间线(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	_, token := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(token)
	number := mustCreateTicket(t, s, web, "升级 UD-2300 固件")

	// 成员本人回复。
	res := reply(t, s, web, number, "收到，正在处理。")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("成员回复状态码 = %d, 想 %d", res.StatusCode, http.StatusCreated)
	}
	var first replyBody
	tbtest.DecodeJSON(t, res, &first)
	if first.ReplyNo != 1 || first.TimelineSeq != 2 {
		t.Errorf("第一条回复 reply_no/timeline_seq = %d/%d, 想 1/2", first.ReplyNo, first.TimelineSeq)
	}
	if first.Author != "陈工" {
		t.Errorf("author = %q, 想 陈工", first.Author)
	}
	if first.Deliver {
		t.Error("deliver 应为 false（交付标记在 #36 启用）")
	}
	// 任务单、创建者一方回复、无指派：轮到本部门。
	if first.WaitingOn.Party != "department" || first.WaitingOn.Text != "本部门（未指派）" {
		t.Errorf("waiting_on 不对：%+v", first.WaitingOn)
	}

	// 成员的具名 Agent 回复：署名按 ADR 0003。
	res = reply(t, s, agent, number, "已安排今晚升级。")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("Agent 回复状态码 = %d, 想 201", res.StatusCode)
	}
	var second replyBody
	tbtest.DecodeJSON(t, res, &second)
	if second.ReplyNo != 2 || second.TimelineSeq != 3 {
		t.Errorf("第二条回复 reply_no/timeline_seq = %d/%d, 想 2/3", second.ReplyNo, second.TimelineSeq)
	}
	if second.Author != "陈工 的 Agent · 笔记本" {
		t.Errorf("author = %q, 想 陈工 的 Agent · 笔记本", second.Author)
	}

	// 时间线：created + 两条 reply，每条带作者、时间和全局变更序号。
	var got ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &got)
	if len(got.Timeline) != 3 {
		t.Fatalf("时间线有 %d 条, 想 3", len(got.Timeline))
	}
	r1, r2 := got.Timeline[1], got.Timeline[2]
	for name, ev := range map[string]timelineEventBody{"成员回复": r1, "Agent回复": r2} {
		if ev.Type != "reply" {
			t.Errorf("[%s] type = %q, 想 reply", name, ev.Type)
		}
		if ev.At == "" {
			t.Errorf("[%s] 事件缺时间", name)
		}
		if ev.Actor.Member.Name != "陈工" {
			t.Errorf("[%s] 作者不对：%+v", name, ev.Actor)
		}
	}
	if r1.Actor.Agent != nil {
		t.Errorf("成员回复的作者不应带 Agent：%+v", r1.Actor)
	}
	if r2.Actor.Agent == nil || r2.Actor.Agent.Name != "笔记本" {
		t.Errorf("Agent 回复的作者应是具名 Agent：%+v", r2.Actor)
	}
	// 全局变更序号随事件推进：created=1、reply=2、3。
	if r1.Change != 2 || r2.Change != 3 {
		t.Errorf("变更序号不连续：%d、%d, 想 2、3", r1.Change, r2.Change)
	}

	// 事件里带正文：用时间线接口核对（增量读取另测）。
	var tl timelineBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number+"/timeline", nil, &tl)
	if len(tl.Events) != 3 || tl.Events[1].BodyMD != "收到，正在处理。" {
		t.Errorf("时间线事件正文不对：%+v", tl.Events)
	}
}

// 空白正文（且无附件）不能回复：422 empty_reply。
func Test空回复返回422(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "升级固件")

	for name, body := range map[string]string{"缺失": "", "纯空白": "  \n\t "} {
		t.Run(name, func(t *testing.T) {
			res := reply(t, s, web, number, body)
			if res.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("状态码 = %d, 想 422", res.StatusCode)
			}
			var got errorBody
			tbtest.DecodeJSON(t, res, &got)
			if got.Error.Code != "empty_reply" {
				t.Errorf("错误码 = %q, 想 empty_reply", got.Error.Code)
			}
		})
	}
}

// 未指派的工单没有指派人，带 deliver 一律 403 deliver_assignee_only
//（#36：交付标记只限指派人一方；结构先定好，避免悄悄丢掉交付意图）。
func Test回复带交付标记返回403(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "升级固件")

	res := s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "做完了", "deliver": true}, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("状态码 = %d, 想 403", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "deliver_assignee_only" {
		t.Errorf("错误码 = %q, 想 deliver_assignee_only", got.Error.Code)
	}
	// 带交付标记的回复被拒后不落地。
	var after ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &after)
	if len(after.Timeline) != 1 {
		t.Errorf("被拒的回复不应落地：%d 条事件", len(after.Timeline))
	}
}

// 写操作引用不存在的工单 → 404 ticket_not_found。
func Test写操作引用不存在的工单返回404(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	checks := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"回复", http.MethodPost, "/api/v1/tickets/20990101-001/replies", map[string]string{"body_md": "…"}},
		{"修订", http.MethodPatch, "/api/v1/tickets/20990101-001", map[string]any{"brief_md": "…", "base_revision": 1}},
		{"关闭", http.MethodPost, "/api/v1/tickets/20990101-001/close", closeBody{Reason: "done"}},
		{"重开", http.MethodPost, "/api/v1/tickets/20990101-001/reopen", nil},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			res := s.Do(t, web, check.method, check.path, check.body, nil)
			if res.StatusCode != http.StatusNotFound {
				t.Fatalf("状态码 = %d, 想 404", res.StatusCode)
			}
			var got errorBody
			tbtest.DecodeJSON(t, res, &got)
			if got.Error.Code != "ticket_not_found" {
				t.Errorf("错误码 = %q, 想 ticket_not_found", got.Error.Code)
			}
		})
	}
}

// 反馈单（无对手）成员回复后轮到访客：waiting_on 由时间线推导。
func Test反馈单成员回复后轮到访客(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	res := createTicket(t, s, web, createTicketBody{Kind: "report", Title: "LIS 导出报错", Labels: []string{"系统:LIS"}})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建反馈单状态码 = %d", res.StatusCode)
	}
	var created createTicketResponse
	tbtest.DecodeJSON(t, res, &created)

	var got replyBody
	res = s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+created.Number+"/replies",
		map[string]string{"body_md": "已定位到导出模块的编码问题。"}, &got)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("回复状态码 = %d", res.StatusCode)
	}
	if got.WaitingOn.Party != "guest" || got.WaitingOn.Text != "访客" {
		t.Errorf("反馈单成员回复后 waiting_on 不对：%+v", got.WaitingOn)
	}
}

// 工单关闭后成员（含成员 Agent）仍可回复，且不会悄悄重开（重新开启是显式动作）。
func Test关闭后成员仍可回复且不重开(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	_, token := s.IssueAgentToken(t, web, "工位")
	agent := s.AgentClient(token)
	number := mustCreateTicket(t, s, web, "整理机型对照表")

	closeTicket(t, s, web, number, closeBody{Reason: "done"})

	for name, c := range map[string]*http.Client{"成员": web, "成员Agent": agent} {
		t.Run(name, func(t *testing.T) {
			res := reply(t, s, c, number, "补一句结论。")
			if res.StatusCode != http.StatusCreated {
				t.Fatalf("状态码 = %d, 想 201（关闭后成员仍可回复）", res.StatusCode)
			}
			var got replyBody
			tbtest.DecodeJSON(t, res, &got)
			if got.WaitingOn.Party != "none" {
				t.Errorf("关闭工单上回复后 waiting_on = %q, 想 none", got.WaitingOn.Party)
			}
		})
	}

	// 状态仍是 closed，没有 reopened 事件。
	var after ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &after)
	if after.Status != "closed" {
		t.Errorf("回复不应重开工单：status = %q", after.Status)
	}
	for _, ev := range after.Timeline {
		if ev.Type == "reopened" {
			t.Error("成员回复不应产生 reopened 事件")
		}
	}
}

// timelineEventBody 是时间线事件的响应结构（含各类型的附加字段）。
type timelineEventBody struct {
	Seq         int64      `json:"seq"`
	Change      int64      `json:"change"`
	At          string     `json:"at"`
	Actor       authorBody `json:"actor"`
	Type        string     `json:"type"`
	ReplyNo     int64      `json:"reply_no"`
	BodyMD      string     `json:"body_md"`
	Deliver     bool       `json:"deliver"`
	Revision    int64      `json:"revision"`
	Reason      string     `json:"reason"`
	DuplicateOf string     `json:"duplicate_of"`
	Label       string     `json:"label"`      // label_added / label_removed（#31）
	Note        string     `json:"note"`       // guest_link_created / guest_link_revoked（#32）
	ExpiresAt   string     `json:"expires_at"` // guest_link_extended 的新到期时刻（#32）
	From        string     `json:"from"`       // assigned 事件的改派前指派人（空 = 创建时指派，#36）
	To          string     `json:"to"`         // assigned 事件的指派对象（#36）
}

// timelineBody 是 GET …/timeline?after=N 的响应结构。
type timelineBody struct {
	Events    []timelineEventBody `json:"events"`
	Cursor    int64               `json:"cursor"`
	WaitingOn struct {
		Party  string  `json:"party"`
		Member *string `json:"member"`
		Text   string  `json:"text"`
		Reason string  `json:"reason"`
	} `json:"waiting_on"`
	YourTurn bool   `json:"your_turn"`
	Status   string `json:"status"`
}

// closeBody 是 POST …/close 的请求体；duplicate_of 可写成编号、序号或数字。
type closeBody struct {
	Reason      string `json:"reason"`
	DuplicateOf any    `json:"duplicate_of"`
}

// closeTicket 关闭一张工单，返回原始响应。
func closeTicket(t testing.TB, s *tbtest.Server, c *http.Client, ref string, body closeBody) *http.Response {
	t.Helper()
	return s.Do(t, c, http.MethodPost, "/api/v1/tickets/"+ref+"/close", body, nil)
}

// assertTimelineAppendOnly 断言时间线只追加：旧事件原样保留，新事件序号递增。
func assertTimelineAppendOnly(t testing.TB, before, after []timelineEventBody) {
	t.Helper()
	if len(after) < len(before) {
		t.Fatalf("时间线变短了：%d → %d", len(before), len(after))
	}
	var lastSeq int64
	for i, ev := range after {
		if i < len(before) && !reflect.DeepEqual(ev, before[i]) {
			t.Errorf("事件 %d 被改动：%+v → %+v", i+1, before[i], ev)
		}
		if ev.Seq <= lastSeq {
			t.Errorf("事件序号应递增：%+v", ev)
		}
		lastSeq = ev.Seq
		if ev.Actor.Member.Name == "" {
			t.Errorf("事件 %d 缺作者：%+v", i+1, ev)
		}
		if !strings.Contains(ev.At, "T") {
			t.Errorf("事件 %d 缺时间：%+v", i+1, ev)
		}
	}
}
