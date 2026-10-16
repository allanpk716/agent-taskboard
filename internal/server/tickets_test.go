package server_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// createTicketBody 是 POST /api/v1/tickets 的请求体（#29：不用模板）。
type createTicketBody struct {
	Kind    string   `json:"kind"`
	Title   string   `json:"title"`
	BriefMD string   `json:"brief_md"`
	Labels  []string `json:"labels"`
}

// createTicketResponse 是 POST /api/v1/tickets 的响应结构。
type createTicketResponse struct {
	Number   string          `json:"number"`
	Seq      int64           `json:"seq"`
	URL      string          `json:"url"`
	Warnings []ticketWarning `json:"warnings"`
}

// ticketWarning 是用模板建单时「缺必填」的逐项提醒（工单照常创建）。
type ticketWarning struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// errorBodyWithExtra 是可能带附加字段（如 suggestions）的错误结构。
type errorBodyWithExtra struct {
	Error struct {
		Code            string   `json:"code"`
		Message         string   `json:"message"`
		Hint            string   `json:"hint"`
		Suggestions     []string `json:"suggestions"`
		CurrentRevision int64    `json:"current_revision"`
	} `json:"error"`
}

// createTicket 发一个建单请求，返回原始响应。
func createTicket(t testing.TB, s *tbtest.Server, c *http.Client, body createTicketBody) *http.Response {
	t.Helper()
	return s.Do(t, c, http.MethodPost, "/api/v1/tickets", body, nil)
}

// 成员（网页会话）可以不用模板建一张工单：拿到工单编号、序号和引用地址。
// 编号按北京时间当天从 001 开始；序号全局连续。说明是一整块 Markdown，revision 从 1 起。
func Test成员不用模板建单拿到编号序号与引用地址(t *testing.T) {
	s := tbtest.Start(t) // 假时钟起点：2026-10-16 09:00（北京时间）
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	res := createTicket(t, s, web, createTicketBody{
		Title:   "升级 UD-2300 固件",
		BriefMD: "背景：……\n要求：……",
		Labels:  []string{"机型:UD-2300"},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d, 想 %d", res.StatusCode, http.StatusCreated)
	}
	var created createTicketResponse
	tbtest.DecodeJSON(t, res, &created)
	if created.Number != "20261016-001" {
		t.Errorf("工单编号 = %q, 想 20261016-001", created.Number)
	}
	if created.Seq != 1 {
		t.Errorf("序号 = %d, 想 1", created.Seq)
	}
	if created.URL != "/t/20261016-001" {
		t.Errorf("url = %q, 想 /t/20261016-001", created.URL)
	}

	// 第二张：编号递增，序号连续。
	res = createTicket(t, s, web, createTicketBody{
		Kind:   "report",
		Title:  "LIS 导出报错",
		Labels: []string{"系统:LIS", "部门:事业一部"},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("第二张建单状态码 = %d, 想 %d", res.StatusCode, http.StatusCreated)
	}
	var second createTicketResponse
	tbtest.DecodeJSON(t, res, &second)
	if second.Number != "20261016-002" || second.Seq != 2 {
		t.Errorf("第二张 = %q #%d, 想 20261016-002 #2", second.Number, second.Seq)
	}
}

// 标题必填：缺失时返回 422 title_required。
func Test建单缺少标题返回422(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	for name, title := range map[string]string{"缺失": "", "空白": "   "} {
		t.Run(name, func(t *testing.T) {
			res := createTicket(t, s, web, createTicketBody{Title: title, Labels: []string{"机型:UD-2300"}})
			if res.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("状态码 = %d, 想 %d", res.StatusCode, http.StatusUnprocessableEntity)
			}
			var got errorBody
			tbtest.DecodeJSON(t, res, &got)
			if got.Error.Code != "title_required" {
				t.Errorf("错误码 = %q, 想 title_required", got.Error.Code)
			}
		})
	}
}

// 至少要打一个维度标签：没有（或只有不带维度的标签）时返回 422 dimension_label_required。
func Test建单没有维度标签返回dimensionLabelRequired(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	cases := map[string][]string{
		"没有标签":   {},
		"null":   nil,
		"只有不带维度": {"待初筛"},
	}
	for name, labels := range cases {
		t.Run(name, func(t *testing.T) {
			res := createTicket(t, s, web, createTicketBody{Title: "固件升级", Labels: labels})
			if res.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("状态码 = %d, 想 %d", res.StatusCode, http.StatusUnprocessableEntity)
			}
			var got errorBody
			tbtest.DecodeJSON(t, res, &got)
			if got.Error.Code != "dimension_label_required" {
				t.Errorf("错误码 = %q, 想 dimension_label_required", got.Error.Code)
			}
			if got.Error.Hint == "" {
				t.Error("hint 为空：应给出字典里的维度标签示例")
			}
		})
	}
}

// 标签必须是字典中已有的值：未知的维度、未知的值、不在字典中的不带维度标签，
// 都返回 422 unknown_label_value；近似写法出现在 suggestions 里。
func Test建单标签值不在字典返回unknownLabelValue(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	t.Run("未知维度", func(t *testing.T) {
		res := createTicket(t, s, web, createTicketBody{Title: "固件升级", Labels: []string{"颜色:红"}})
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("状态码 = %d, 想 422", res.StatusCode)
		}
		var got errorBodyWithExtra
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "unknown_label_value" {
			t.Errorf("错误码 = %q, 想 unknown_label_value", got.Error.Code)
		}
		if !strings.Contains(got.Error.Message, "颜色:红") {
			t.Errorf("message 应包含出问题的标签：%q", got.Error.Message)
		}
	})

	t.Run("未知值", func(t *testing.T) {
		res := createTicket(t, s, web, createTicketBody{Title: "固件升级", Labels: []string{"机型:BH-9999"}})
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("状态码 = %d, 想 422", res.StatusCode)
		}
		var got errorBodyWithExtra
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "unknown_label_value" {
			t.Errorf("错误码 = %q, 想 unknown_label_value", got.Error.Code)
		}
	})

	t.Run("近似写法给suggestions", func(t *testing.T) {
		// norm（忽略大小写、空格、连字符）后与字典值一致 → 出现在 suggestions。
		res := createTicket(t, s, web, createTicketBody{Title: "固件升级", Labels: []string{"机型:ud 2300"}})
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("状态码 = %d, 想 422", res.StatusCode)
		}
		var got errorBodyWithExtra
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "unknown_label_value" {
			t.Fatalf("错误码 = %q, 想 unknown_label_value", got.Error.Code)
		}
		assertContains(t, got.Error.Suggestions, "机型:UD-2300")
	})

	t.Run("不在字典中的不带维度标签", func(t *testing.T) {
		res := createTicket(t, s, web, createTicketBody{Title: "固件升级", Labels: []string{"机型:UD-2300", "待复测"}})
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("状态码 = %d, 想 422", res.StatusCode)
		}
		var got errorBodyWithExtra
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "unknown_label_value" {
			t.Errorf("错误码 = %q, 想 unknown_label_value", got.Error.Code)
		}
	})
}

// 种子文件导入后，用字典里的值可以建单：预置七个维度，
// 机型（14 个）与部门（13 个）来自日报系统的一次性种子文件。
func Test种子导入后用字典里的值可以建单(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	res := createTicket(t, s, web, createTicketBody{
		Title: "整理机型对照表",
		Labels: []string{
			"机型:BM-10vet", "机型:US-1680", // 同一维度可以打多个值
			"部门:人工智能研究部", "部门:集团软件技术组",
			"产品线:血液", "系统:智慧实验室",
		},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("用种子值建单状态码 = %d, 想 %d", res.StatusCode, http.StatusCreated)
	}
	var created createTicketResponse
	tbtest.DecodeJSON(t, res, &created)

	// 用读取接口核对标签都落上了（详细读取行为另测）。
	var got ticketResponseBody
	res = s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("读工单状态码 = %d, 想 200", res.StatusCode)
	}
	for _, want := range []string{"机型:BM-10vet", "机型:US-1680", "部门:人工智能研究部", "部门:集团软件技术组", "产品线:血液", "系统:智慧实验室"} {
		assertContains(t, got.Labels, want)
	}
}

// kind 只能是 task（默认）或 report，其他值返回 422 invalid_kind。
func Test建单kind非法返回422(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	res := createTicket(t, s, web, createTicketBody{Kind: "bug", Title: "固件升级", Labels: []string{"机型:UD-2300"}})
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("状态码 = %d, 想 422", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "invalid_kind" {
		t.Errorf("错误码 = %q, 想 invalid_kind", got.Error.Code)
	}
}

// 未登录不能建单，也不能读工单。
func Test未登录建单读单返回401(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")

	res := createTicket(t, s, s.NewClient(), createTicketBody{Title: "固件升级", Labels: []string{"机型:UD-2300"}})
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录建单状态码 = %d, 想 401", res.StatusCode)
	}
	res = s.Do(t, s.NewClient(), http.MethodGet, "/api/v1/tickets/20261016-001", nil, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录读单状态码 = %d, 想 401", res.StatusCode)
	}
}

// 建单请求体不是合法 JSON 时返回 400 invalid_json。
func Test建单请求体不是JSON时返回400(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	req, err := http.NewRequest(http.MethodPost, s.URL()+"/api/v1/tickets", strings.NewReader("not json"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := web.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, 想 %d", res.StatusCode, http.StatusBadRequest)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "invalid_json" {
		t.Errorf("错误码 = %q, 想 invalid_json", got.Error.Code)
	}
}

// authorBody 是作者结构 {member, agent}，本人操作时 agent 为 null（data-model）。
type authorBody struct {
	Member *struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"member"`
	Agent *struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"agent"`
	LinkNote   *string `json:"link_note"`
	AuthorName *string `json:"author_name"`
	ViaAgent   *bool   `json:"via_agent"`
}

// ticketResponseBody 是 GET /api/v1/tickets/{编号或序号} 的响应结构。
type ticketResponseBody struct {
	Number      string      `json:"number"`
	Seq         int64       `json:"seq"`
	URL         string      `json:"url"`
	Kind        string      `json:"kind"`
	Title       string      `json:"title"`
	Status      string      `json:"status"`
	CloseReason *string     `json:"close_reason"`
	Template    *string     `json:"template"`
	Creator     authorBody  `json:"creator"`
	Assignee    *authorBody `json:"assignee"`
	WaitingOn   struct {
		Party  string  `json:"party"`
		Member *string `json:"member"`
		Text   string  `json:"text"`
		Reason string  `json:"reason"`
	} `json:"waiting_on"`
	Labels []string `json:"labels"` // 访客视角没有这个字段（解码为 nil；另用原始 JSON 断言字段不存在）
	Brief  struct {
		Fields   *map[string]string `json:"fields"`
		Markdown string             `json:"markdown"`
		Revision int64              `json:"revision"`
		Template *string            `json:"template"`
	} `json:"brief"`
	Viewer struct {
		Kind   string `json:"kind"`
		Member *struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"member"`
		Agent *struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"agent"`
		LinkNote      *string `json:"link_note"`
		LinkExpiresAt *string `json:"link_expires_at"`
		Role          string  `json:"role"`
		YourTurn      bool    `json:"your_turn"`
		Can           struct {
			Reply            bool `json:"reply"`
			Upload           bool `json:"upload"`
			EditBrief        bool `json:"edit_brief"`
			Label            bool `json:"label"`
			Close            bool `json:"close"`
			Reopen           bool `json:"reopen"`
			ManageGuestLinks bool `json:"manage_guest_links"`
			Deliver          bool `json:"deliver"`
			Assign           bool `json:"assign"`
		} `json:"can"`
	} `json:"viewer"`
	Timeline []timelineEventBody `json:"timeline"`
}

// 用工单编号和用序号读到的是同一张工单；响应里标题、类型、状态、创建者、标签、
// 说明（含 revision）、时间线和 viewer.can 齐全；不存在的工单返回 404 ticket_not_found。
func Test用编号和序号读到同一张工单(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	res := createTicket(t, s, web, createTicketBody{
		Title:   "升级 UD-2300 固件",
		BriefMD: "背景：……\n要求：……",
		Labels:  []string{"机型:UD-2300"},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d, 想 201", res.StatusCode)
	}

	var byNumber, bySeq ticketResponseBody
	res = s.Do(t, web, http.MethodGet, "/api/v1/tickets/20261016-001", nil, &byNumber)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("按编号读状态码 = %d, 想 200", res.StatusCode)
	}
	res = s.Do(t, web, http.MethodGet, "/api/v1/tickets/1", nil, &bySeq)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("按序号读状态码 = %d, 想 200", res.StatusCode)
	}
	// 序号带 # 前缀（URL 编码为 %23）也能读。
	res = s.Do(t, web, http.MethodGet, "/api/v1/tickets/%231", nil, &bySeq)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("带 # 读状态码 = %d, 想 200", res.StatusCode)
	}
	if bySeq.Number != byNumber.Number || bySeq.Title != byNumber.Title || bySeq.Seq != byNumber.Seq {
		t.Errorf("两种引用读到不同的工单：%+v vs %+v", bySeq, byNumber)
	}

	check := func(name string, got ticketResponseBody) {
		t.Helper()
		if got.Number != "20261016-001" || got.Seq != 1 {
			t.Errorf("[%s] 编号/序号 = %q #%d", name, got.Number, got.Seq)
		}
		if got.URL != "/t/20261016-001" {
			t.Errorf("[%s] url = %q, 想 /t/20261016-001", name, got.URL)
		}
		if got.Kind != "task" {
			t.Errorf("[%s] kind = %q, 想 task（默认）", name, got.Kind)
		}
		if got.Title != "升级 UD-2300 固件" {
			t.Errorf("[%s] title = %q", name, got.Title)
		}
		if got.Status != "open" {
			t.Errorf("[%s] status = %q, 想 open", name, got.Status)
		}
		if got.CloseReason != nil {
			t.Errorf("[%s] close_reason 应为 null", name)
		}
		if got.Template != nil {
			t.Errorf("[%s] template 应为 null（不用模板）", name)
		}
		if got.Creator.Member.Name != "陈工" || got.Creator.Agent != nil {
			t.Errorf("[%s] creator 不对：%+v", name, got.Creator)
		}
		if got.Assignee != nil {
			t.Errorf("[%s] assignee 应为 null（指派在后续工单）", name)
		}
		// 尚无回复且未指派：轮到本部门。
		if got.WaitingOn.Party != "department" || got.WaitingOn.Text != "本部门（未指派）" {
			t.Errorf("[%s] waiting_on 不对：%+v", name, got.WaitingOn)
		}
		assertContains(t, got.Labels, "机型:UD-2300")
		if got.Brief.Markdown != "背景：……\n要求：……" {
			t.Errorf("[%s] brief.markdown = %q", name, got.Brief.Markdown)
		}
		if got.Brief.Revision != 1 {
			t.Errorf("[%s] brief.revision = %d, 想 1", name, got.Brief.Revision)
		}
		if got.Brief.Fields != nil {
			t.Errorf("[%s] brief.fields 应为 null（不用模板）", name)
		}
		// 时间线：一条 created 事件，带序号、时间、作者和全局变更序号。
		if len(got.Timeline) != 1 {
			t.Fatalf("[%s] 时间线有 %d 条, 想 1", name, len(got.Timeline))
		}
		ev := got.Timeline[0]
		if ev.Type != "created" || ev.Seq != 1 || ev.Change != 1 || ev.At == "" {
			t.Errorf("[%s] created 事件不对：%+v", name, ev)
		}
		if ev.Actor.Member.Name != "陈工" || ev.Actor.Agent != nil {
			t.Errorf("[%s] 事件作者不对：%+v", name, ev.Actor)
		}
		// viewer：创建者本人。
		if got.Viewer.Kind != "member" || got.Viewer.Role != "creator" || got.Viewer.YourTurn {
			t.Errorf("[%s] viewer 不对：%+v", name, got.Viewer)
		}
		can := got.Viewer.Can
		if !can.Reply || !can.Upload || !can.EditBrief || !can.Label || !can.Close || can.Reopen ||
			!can.ManageGuestLinks || can.Deliver || !can.Assign {
			t.Errorf("[%s] viewer.can 不对：%+v", name, can)
		}
	}
	check("编号", byNumber)
	check("序号", bySeq)

	// 全局变更序号：第二张工单的 created 事件是 change 2。
	if _, token := s.IssueAgentToken(t, web, "笔记本"); token == "" {
		t.Fatal("签发 Agent token 失败")
	}
	res = createTicket(t, s, web, createTicketBody{Title: "另一张", Labels: []string{"机型:US-500"}})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("第二张建单状态码 = %d", res.StatusCode)
	}
	var second ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/2", nil, &second)
	if len(second.Timeline) != 1 || second.Timeline[0].Change != 2 {
		t.Errorf("第二张工单的 created 事件 change = %+v, 想 2（全局连续）", second.Timeline)
	}

	// 不存在的工单：404 ticket_not_found（编号和序号两种写法都试）。
	for _, ref := range []string{"20260101-001", "999999", "不是编号"} {
		res := s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+ref, nil, nil)
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("读 %q 状态码 = %d, 想 404", ref, res.StatusCode)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "ticket_not_found" {
			t.Errorf("读 %q 错误码 = %q, 想 ticket_not_found", ref, got.Error.Code)
		}
	}
}

// 成员 Agent 用 token 建单：工单的创建者署名为该成员的具名 Agent（ADR 0003）。
func Test成员Agent建单署名为具名Agent(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	_, token := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(token)

	res := createTicket(t, s, agent, createTicketBody{Title: "整理对照表", Labels: []string{"机型:SL-1000"}})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("Agent 建单状态码 = %d, 想 201", res.StatusCode)
	}
	var got ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/20261016-001", nil, &got)
	if got.Creator.Agent == nil || got.Creator.Agent.Name != "笔记本" {
		t.Errorf("creator.agent 不对：%+v", got.Creator.Agent)
	}
	if got.Creator.Member.Name != "陈工" {
		t.Errorf("creator.member.name = %q, 想 陈工", got.Creator.Member.Name)
	}
	if len(got.Timeline) != 1 || got.Timeline[0].Actor.Agent == nil || got.Timeline[0].Actor.Agent.Name != "笔记本" {
		t.Errorf("created 事件的作者不是具名 Agent：%+v", got.Timeline)
	}
	// 创建者的 Agent 也能编辑说明（权限按创建者一方算）。
	var byAgent ticketResponseBody
	s.Do(t, agent, http.MethodGet, "/api/v1/tickets/20261016-001", nil, &byAgent)
	if !byAgent.Viewer.Can.EditBrief || byAgent.Viewer.Role != "creator" {
		t.Errorf("Agent 读单 viewer 不对：%+v", byAgent.Viewer)
	}
}

// ?format=markdown 返回给 Agent 读的 Markdown；署名按 ADR 0003 写成
// 「陈某 的 Agent · 名字」，成员本人写成「陈某」。
func TestMarkdown视图按ADR0003署名(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	_, token := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(token)

	if res := createTicket(t, s, agent, createTicketBody{
		Title: "升级 UD-2300 固件", BriefMD: "背景：……", Labels: []string{"机型:UD-2300", "部门:研发部"},
	}); res.StatusCode != http.StatusCreated {
		t.Fatalf("Agent 建单状态码 = %d", res.StatusCode)
	}
	if res := createTicket(t, s, web, createTicketBody{
		Title: "另一件事", BriefMD: "说明……", Labels: []string{"机型:US-1000"},
	}); res.StatusCode != http.StatusCreated {
		t.Fatalf("本人建单状态码 = %d", res.StatusCode)
	}

	readMD := func(t testing.TB, c *http.Client, path string) string {
		t.Helper()
		res := s.Do(t, c, http.MethodGet, path, nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET %s 状态码 = %d, 想 200", path, res.StatusCode)
		}
		if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
			t.Fatalf("Content-Type = %q, 想 text/markdown", ct)
		}
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := res.Body.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		return sb.String()
	}

	// Agent 建的工单：发起人与时间线署名都是「陈工 的 Agent · 笔记本」。
	md := readMD(t, web, "/api/v1/tickets/20261016-001?format=markdown")
	for _, want := range []string{
		"# 工单 20261016-001：升级 UD-2300 固件",
		"- 发起人：陈工 的 Agent · 笔记本",
		"- 类型：任务 · 状态：开启",
		"- 现在轮到：本部门（未指派）",
		"- 标签：机型:UD-2300、部门:研发部",
		"## 说明（第 1 版）",
		"背景：……",
		"## 时间线",
		"创建工单",
		"陈工 的 Agent · 笔记本 创建工单",
		"## 下一步（给 Agent 的操作指引）",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown 缺少 %q：\n%s", want, md)
		}
	}

	// 本人建的工单：署名就是成员名。
	md = readMD(t, web, "/api/v1/tickets/2?format=markdown")
	if !strings.Contains(md, "- 发起人：陈工 · 指派人：未指派") {
		t.Errorf("Markdown 发起人应写成员名：\n%s", md)
	}
	if strings.Contains(md, "的 Agent") {
		t.Errorf("本人建的工单 Markdown 里不应出现 Agent 署名：\n%s", md)
	}

	// Agent 自己读：署名行写 Agent 身份。
	md = readMD(t, agent, "/api/v1/tickets/20261016-001?format=markdown")
	if !strings.Contains(md, "你是 **陈工 的 Agent · 笔记本**") {
		t.Errorf("Markdown「下一步」应写 Agent 身份：\n%s", md)
	}
}

// assertContains 断言列表里有某个元素。
func assertContains[T comparable](t testing.TB, list []T, want T) {
	t.Helper()
	for _, x := range list {
		if x == want {
			return
		}
	}
	t.Errorf("列表 %v 里没有 %v", list, want)
}

// seedTicketsForDay 是「第 1000 张」测试的快路径：直接往库里补当日已有工单，
// 只造编号分配需要的数据（per-ticket 笔记允许这种造数方式，外部行为仍由 API 验证）。
func seedTicketsForDay(t testing.TB, s *tbtest.Server, date string, start, end int) {
	t.Helper()
	tx, err := s.Store().DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("开事务失败：%v", err)
	}
	defer tx.Rollback()
	for i := start; i <= end; i++ {
		num := fmt.Sprintf("%s-%03d", date, i)
		if _, err := tx.Exec(`INSERT INTO tickets (number, kind, title, created_at, last_change) VALUES (?, 'task', '占位', ?, 0)`,
			num, "2026-10-16T09:00:00+08:00"); err != nil {
			t.Fatalf("插入占位工单 %s 失败：%v", num, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("提交失败：%v", err)
	}
}

// 用注入时钟测编号规则：跨天后从 001 重新开始、第 1000 张自动变 4 位、序号全局连续。
// 造 999 张工单用直连数据库的快路径（per-ticket 笔记允许，外部行为仍由 API 验证）。
func Test编号跨天重置第1000张变四位序号连续(t *testing.T) {
	day1 := time.Date(2026, 10, 16, 9, 0, 0, 0, clock.Beijing)
	s := tbtest.Start(t, tbtest.WithClockStart(day1))
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	mk := func() createTicketResponse {
		t.Helper()
		res := createTicket(t, s, web, createTicketBody{Title: "占位", Labels: []string{"机型:UD-2300"}})
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("建单状态码 = %d, 想 201", res.StatusCode)
		}
		var out createTicketResponse
		tbtest.DecodeJSON(t, res, &out)
		return out
	}

	// 第一天：两张。
	if a, b := mk(), mk(); a.Number != "20261016-001" || b.Number != "20261016-002" || b.Seq != 2 {
		t.Fatalf("第一天编号/序号不对：%q#%d %q#%d", a.Number, a.Seq, b.Number, b.Seq)
	}

	// 跨天：第二天从 001 重新开始，序号继续。
	s.Clock().Set(time.Date(2026, 10, 17, 10, 0, 0, 0, clock.Beijing))
	if c := mk(); c.Number != "20261017-001" || c.Seq != 3 {
		t.Fatalf("跨天后编号/序号不对：%q#%d, 想 20261017-001 #3", c.Number, c.Seq)
	}

	// 快路径：把 2026-10-17 补到已有 999 张（上面已建 1 张，再补 998 张）。
	seedTicketsForDay(t, s, "20261017", 2, 999)

	// 第 1000 张：编号变 4 位，序号连续（3 + 998 + 1 = 1002）。
	if d := mk(); d.Number != "20261017-1000" || d.Seq != 1002 {
		t.Fatalf("第 1000 张编号/序号不对：%q#%d, 想 20261017-1000 #1002", d.Number, d.Seq)
	}

	// 再跨一天：重新从 001 开始，序号继续。
	s.Clock().Set(time.Date(2026, 10, 18, 8, 30, 0, 0, clock.Beijing))
	if e := mk(); e.Number != "20261018-001" || e.Seq != 1003 {
		t.Fatalf("第三天编号/序号不对：%q#%d, 想 20261018-001 #1003", e.Number, e.Seq)
	}
}
