package server_test

// #36 成员间交接与收件箱：黑盒测试照搬 prototype/member-handoff 的 4 个场景
//（场景 1–4 分散在下面各测试里），加上等待方推导表、改派、交付、收件箱与
// Markdown「下一步」的逐项断言。附件（#33）与模板（#34）的原型步骤按
// 现有接口改写：交付回复不带附件引用，建单用 brief_md 而不是模板。

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// createAssignedResponse 是建单响应里 #36 新增的字段（与 #29 的
// createTicketResponse 合并解码）。
type createAssignedResponse struct {
	createTicketResponse
	Assignee *struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"assignee"`
	WaitingOn waitingOnBody `json:"waiting_on"`
	ShareText *string       `json:"share_text"`
}

// waitingOnBody 是 waiting_on 的对外结构。
type waitingOnBody struct {
	Party  string  `json:"party"`
	Member *string `json:"member"`
	Text   string  `json:"text"`
	Reason string  `json:"reason"`
}

// handoffWorld 是成员交接场景的固定班底：王工建单派活，李工接活，
// 赵工当第三位成员；每人都有网页会话和一个具名 Agent。
type handoffWorld struct {
	s       *tbtest.Server
	webWang *http.Client
	webLi   *http.Client
	webZhao *http.Client
	wangA   *http.Client // 王工 的 Agent · 笔记本
	liA     *http.Client // 李工 的 Agent · 工位
	zhaoA   *http.Client // 赵工 的 Agent · 工位
}

func newHandoffWorld(t testing.TB) *handoffWorld {
	t.Helper()
	s := tbtest.Start(t)
	s.CreateAdmin("王工", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	s.CreateMember("赵工", "secret-pass")
	webWang := s.Login("王工", "secret-pass")
	webLi := s.Login("李工", "secret-pass")
	webZhao := s.Login("赵工", "secret-pass")
	_, wangToken := s.IssueAgentToken(t, webWang, "笔记本")
	_, liToken := s.IssueAgentToken(t, webLi, "工位")
	_, zhaoToken := s.IssueAgentToken(t, webZhao, "工位")
	return &handoffWorld{
		s: s, webWang: webWang, webLi: webLi, webZhao: webZhao,
		wangA: s.AgentClient(wangToken), liA: s.AgentClient(liToken), zhaoA: s.AgentClient(zhaoToken),
	}
}

// createHandoffTicket 建一张带指派的工单，返回响应。失败时终止测试。
func (w *handoffWorld) createHandoffTicket(t testing.TB, c *http.Client, title, assignee string) createAssignedResponse {
	t.Helper()
	body := map[string]any{
		"title":    title,
		"brief_md": "背景：……",
		"labels":   []string{"机型:UD-2300"},
	}
	if assignee != "" {
		body["assignee"] = assignee
	}
	res := w.s.Do(t, c, http.MethodPost, "/api/v1/tickets", body, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单 %q（指派 %q）状态码 = %d, 想 201", title, assignee, res.StatusCode)
	}
	var created createAssignedResponse
	tbtest.DecodeJSON(t, res, &created)
	return created
}

// 成员的 Agent 建单并指派给同事：响应带 assignee、结构化 waiting_on 和一句
// 可直接转发的 share_text；时间线在 created 之后记一条 assigned 事件；
// 指派人在工单上的角色是指派人，轮到他，能交付；创建者则不能。
func Test建单时指派成员(t *testing.T) {
	w := newHandoffWorld(t)
	created := w.createHandoffTicket(t, w.wangA, "整理 Q3 质控规则对照表", "李工")

	if created.Assignee == nil || created.Assignee.Name != "李工" {
		t.Fatalf("assignee = %+v, 想 李工", created.Assignee)
	}
	wantShare := "李工：给你派了一张工单「整理 Q3 质控规则对照表」，让你的 Agent 读一下 /t/" +
		created.Number + "（它也会出现在你的收件箱里）"
	if created.ShareText == nil || *created.ShareText != wantShare {
		t.Errorf("share_text = %v, 想 %q", created.ShareText, wantShare)
	}
	if created.WaitingOn.Party != "assignee" || created.WaitingOn.Member == nil || *created.WaitingOn.Member != "李工" {
		t.Errorf("waiting_on = %+v, 想 party=assignee member=李工", created.WaitingOn)
	}
	if created.WaitingOn.Text != "李工（指派人）" || created.WaitingOn.Reason != "新指派给你" {
		t.Errorf("waiting_on text/reason = %q/%q, 想 李工（指派人）/新指派给你",
			created.WaitingOn.Text, created.WaitingOn.Reason)
	}

	var body ticketResponseBody
	w.s.Do(t, w.webLi, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &body)
	if len(body.Timeline) != 2 {
		t.Fatalf("时间线应有 created + assigned 两条事件，实际 %d 条", len(body.Timeline))
	}
	if body.Timeline[1].Type != "assigned" {
		t.Fatalf("第二条事件 type = %q, 想 assigned", body.Timeline[1].Type)
	}
	if body.Timeline[1].To != "李工" || body.Timeline[1].From != "" {
		t.Errorf("assigned 事件 from/to = %q/%q, 想 空/李工", body.Timeline[1].From, body.Timeline[1].To)
	}
	if body.Assignee == nil || body.Assignee.Member == nil || body.Assignee.Member.Name != "李工" {
		t.Errorf("GET 的 assignee = %+v, 想 李工", body.Assignee)
	}
	// 指派人：轮到他，能交付；创建者：不轮到他，不能交付。
	var liView, wangView ticketResponseBody
	w.s.Do(t, w.liA, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &liView)
	w.s.Do(t, w.wangA, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &wangView)
	if liView.Viewer.Role != "assignee" || !liView.Viewer.YourTurn || !liView.Viewer.Can.Deliver {
		t.Errorf("指派人视角 role/your_turn/can.deliver = %s/%v/%v, 想 assignee/true/true",
			liView.Viewer.Role, liView.Viewer.YourTurn, liView.Viewer.Can.Deliver)
	}
	if wangView.Viewer.Role != "creator" || wangView.Viewer.YourTurn || wangView.Viewer.Can.Deliver {
		t.Errorf("创建者视角 role/your_turn/can.deliver = %s/%v/%v, 想 creator/false/false",
			wangView.Viewer.Role, wangView.Viewer.YourTurn, wangView.Viewer.Can.Deliver)
	}
}

// 指派可以写成员 id 或姓名；自派自与不指派时 share_text 为空。
func Test指派写法与shareText为空的情况(t *testing.T) {
	w := newHandoffWorld(t)

	// 用姓名指派后，拿响应里的 id 再指派一张。
	first := w.createHandoffTicket(t, w.webWang, "第一张", "李工")
	byID := w.createHandoffTicket(t, w.webWang, "第二张", strconv.FormatInt(first.Assignee.ID, 10))
	if byID.Assignee == nil || byID.Assignee.Name != "李工" {
		t.Fatalf("按 id 指派：assignee = %+v, 想 李工", byID.Assignee)
	}

	self := w.createHandoffTicket(t, w.webWang, "自派自", "王工")
	if self.ShareText != nil {
		t.Errorf("自派自的 share_text = %q, 想为空", *self.ShareText)
	}
	if self.WaitingOn.Party != "assignee" || self.WaitingOn.Text != "王工（指派人）" {
		t.Errorf("自派自 waiting_on = %+v, 想 指派人 王工", self.WaitingOn)
	}

	none := w.createHandoffTicket(t, w.webWang, "不指派", "")
	if none.Assignee != nil || none.ShareText != nil {
		t.Errorf("不指派：assignee/share_text = %+v/%v, 想都为空", none.Assignee, none.ShareText)
	}
	if none.WaitingOn.Party != "department" || none.WaitingOn.Reason != "尚未指派" {
		t.Errorf("不指派 waiting_on = %+v, 想 department/尚未指派", none.WaitingOn)
	}
}

// 指派给成员 Agent 被拒绝（422 assignee_must_be_member）；成员 Agent 不能被指派。
// 写不存在的成员返回 422 unknown_member，hint 列出可选成员。
func Test指派给成员Agent被拒绝(t *testing.T) {
	w := newHandoffWorld(t)

	for _, assignee := range []string{"李工 的 Agent · 工位", "工位", "笔记本"} {
		res := w.s.Do(t, w.wangA, http.MethodPost, "/api/v1/tickets", map[string]any{
			"title": "整理对照表", "brief_md": "……", "labels": []string{"机型:UD-2300"}, "assignee": assignee,
		}, nil)
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("指派 %q 状态码 = %d, 想 422", assignee, res.StatusCode)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "assignee_must_be_member" {
			t.Errorf("指派 %q 错误码 = %q, 想 assignee_must_be_member", assignee, got.Error.Code)
		}
	}

	res := w.s.Do(t, w.wangA, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "整理对照表", "brief_md": "……", "labels": []string{"机型:UD-2300"}, "assignee": "张三丰",
	}, nil)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("指派不存在成员状态码 = %d, 想 422", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "unknown_member" {
		t.Errorf("错误码 = %q, 想 unknown_member", got.Error.Code)
	}
	if !strings.Contains(got.Error.Hint, "王工") || !strings.Contains(got.Error.Hint, "李工") {
		t.Errorf("hint 应列出可选成员：%q", got.Error.Hint)
	}
}
