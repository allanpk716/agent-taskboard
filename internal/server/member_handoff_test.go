package server_test

// #36 成员间交接与收件箱：黑盒测试照搬 prototype/member-handoff 的 4 个场景
//（场景 1–4 分散在下面各测试里），加上等待方推导表、改派、交付、收件箱与
// Markdown「下一步」的逐项断言。附件（#33）与模板（#34）的原型步骤按
// 现有接口改写：交付回复不带附件引用，建单用 brief_md 而不是模板。

import (
	"fmt"
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
	Why    string  `json:"why"`
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
	wantHuman := fmt.Sprintf("李工：给你派了一张工单 %s「整理 Q3 质控规则对照表」，网页 %s/t/%s（也在你的收件箱里）。下面这段交给你的 Agent：\n\n",
		created.Number, w.s.URL(), created.Number)
	if created.ShareText == nil || !strings.HasPrefix(*created.ShareText, wantHuman) {
		t.Errorf("share_text 的人话行 = %v, 想 %q", created.ShareText, wantHuman)
	}
	for _, want := range []string{
		w.s.URL() + "/agent.md", "Authorization: Bearer <token>",
		w.s.URL() + "/api/v1/tickets/" + created.Number + "?format=markdown",
		w.s.URL() + "/api/v1/inbox",
	} {
		if !strings.Contains(*created.ShareText, want) {
			t.Errorf("share_text 应包含 %q：%q", want, *created.ShareText)
		}
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

// waitOf 读取工单当前的 waiting_on。
func (w *handoffWorld) waitOf(t testing.TB, c *http.Client, number string) waitingOnBody {
	t.Helper()
	var body ticketResponseBody
	w.s.Do(t, c, http.MethodGet, "/api/v1/tickets/"+number, nil, &body)
	return waitingOnBody{
		Party: body.WaitingOn.Party, Member: body.WaitingOn.Member,
		Text: body.WaitingOn.Text, Reason: body.WaitingOn.Reason,
		Why: body.WaitingOn.Why,
	}
}

// assertWaitingOn 对照整行（party/member/text/reason）。
func assertWaitingOn(t testing.TB, got waitingOnBody, want waitingOnBody, note string) {
	t.Helper()
	if got.Party != want.Party {
		t.Errorf("%s：party = %q, 想 %q", note, got.Party, want.Party)
	}
	if want.Member == nil {
		if got.Member != nil {
			t.Errorf("%s：member = %q, 想为空", note, *got.Member)
		}
	} else if got.Member == nil || *got.Member != *want.Member {
		gotName := "<空>"
		if got.Member != nil {
			gotName = *got.Member
		}
		t.Errorf("%s：member = %q, 想 %q", note, gotName, *want.Member)
	}
	if want.Text != "" && got.Text != want.Text {
		t.Errorf("%s：text = %q, 想 %q", note, got.Text, want.Text)
	}
	if want.Reason != "" && got.Reason != want.Reason {
		t.Errorf("%s：reason = %q, 想 %q", note, got.Reason, want.Reason)
	}
	if want.Why != "" && got.Why != want.Why {
		t.Errorf("%s：why = %q, 想 %q", note, got.Why, want.Why)
	}
}

// 等待方推导表的每一行都有对应的一步（spec「等待方与收件箱」）。
// 成员 Agent 的回复算其所属成员一方；改派后原指派人的回复不再参与推导。
func Test等待方推导表(t *testing.T) {
	w := newHandoffWorld(t)
	// 一个任务（王工→李工，王工的 Agent 建的）、一个自派自的反馈和一个未指派的反馈。
	task := w.createHandoffTicket(t, w.wangA, "整理对照表", "李工").Number
	selfReportRes := w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets", map[string]any{
		"kind": "report", "title": "质控结果偏高", "brief_md": "……", "labels": []string{"机型:UD-2300"},
		"assignee": "王工",
	}, nil)
	var selfReportBody createAssignedResponse
	tbtest.DecodeJSON(t, selfReportRes, &selfReportBody)
	selfReport := selfReportBody.Number
	reportRes := w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets", map[string]any{
		"kind": "report", "title": "耗材到货异常", "brief_md": "……", "labels": []string{"机型:UD-2300"},
	}, nil)
	var reportBody createAssignedResponse
	tbtest.DecodeJSON(t, reportRes, &reportBody)
	report := reportBody.Number

	// 行 1a：尚无回复、未指派 → department / 尚未指派。
	plain := w.createHandoffTicket(t, w.webWang, "未指派的工单", "").Number
	assertWaitingOn(t, w.waitOf(t, w.webWang, plain),
		waitingOnBody{Party: "department", Text: "本部门（未指派）", Reason: "尚未指派"}, "尚无回复未指派")

	// 行 1b：尚无回复、已指派 → 指派人 / 新指派给你。
	m := strp("李工")
	assertWaitingOn(t, w.waitOf(t, w.webWang, task),
		waitingOnBody{Party: "assignee", Member: m, Text: "李工（指派人）", Reason: "新指派给你"}, "尚无回复已指派")

	// 行 2：最后是访客回复 → 指派人（访客链接由 #32 的接口创建）。
	link := w.s.CreateGuestLink(t, w.webWang, task, map[string]any{"note": "检验科-张三"})
	guest := w.s.GuestClient(link.Key(t))
	w.s.Do(t, guest, http.MethodPost, "/api/v1/tickets/"+task+"/replies",
		map[string]string{"body_md": "补充：试剂批号 R20260930。", "author_name": "张三"}, nil)
	assertWaitingOn(t, w.waitOf(t, w.webWang, task),
		waitingOnBody{Party: "assignee", Member: m, Text: "李工（指派人）", Reason: "访客回复了"}, "访客回复后轮到指派人")

	// 行 2b：访客回复、未指派 → department / 访客回复了。
	guestPlain := w.s.CreateGuestLink(t, w.webWang, plain, map[string]any{"note": "检验科-李四"})
	w.s.Do(t, w.s.GuestClient(guestPlain.Key(t)), http.MethodPost, "/api/v1/tickets/"+plain+"/replies",
		map[string]string{"body_md": "补充材料。"}, nil)
	assertWaitingOn(t, w.waitOf(t, w.webWang, plain),
		waitingOnBody{Party: "department", Text: "本部门（未指派）", Reason: "访客回复了"}, "访客回复未指派")

	// 行 3：任务、指派人一方回复（这里是李工的 Agent）→ 创建者 / 指派人回复了，待你答复。
	var delivered replyBody
	w.s.Do(t, w.liA, http.MethodPost, "/api/v1/tickets/"+task+"/replies",
		map[string]string{"body_md": "「适用机型」要细到软件版本吗？"}, &delivered)
	wang := strp("王工")
	assertWaitingOn(t, w.waitOf(t, w.webWang, task),
		waitingOnBody{Party: "creator", Member: wang, Text: "王工（发起人）", Reason: "指派人回复了，待你答复"}, "指派人回复轮到创建者")

	// 行 4：创建者一方回复 → 指派人 / 发起人回复了（此前没有交付）。
	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+task+"/replies",
		map[string]string{"body_md": "不用，写到机型即可。"}, nil)
	assertWaitingOn(t, w.waitOf(t, w.webWang, task),
		waitingOnBody{Party: "assignee", Member: m, Text: "李工（指派人）", Reason: "发起人回复了"}, "创建者回复轮到指派人")

	// 行 5：第三位成员回复 → 不改变轮次（仍轮到李工）。
	w.s.Do(t, w.zhaoA, http.MethodPost, "/api/v1/tickets/"+task+"/replies",
		map[string]string{"body_md": "我这边有类似经验，供参考。"}, nil)
	assertWaitingOn(t, w.waitOf(t, w.webWang, task),
		waitingOnBody{Party: "assignee", Member: m, Text: "李工（指派人）", Reason: "发起人回复了"}, "第三位成员不改轮次")

	// 行 6：其余情况（反馈、自派自）成员回复 → 访客。
	var selfReply replyBody
	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+selfReport+"/replies",
		map[string]string{"body_md": "收到，先查批号记录。"}, &selfReply)
	assertWaitingOn(t, w.waitOf(t, w.webWang, selfReport),
		waitingOnBody{Party: "guest", Text: "访客"}, "自派自反馈回复轮到访客")
	// 未指派的反馈没有对手，同样轮到访客。
	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+report+"/replies",
		map[string]string{"body_md": "先查物流记录。"}, nil)
	assertWaitingOn(t, w.waitOf(t, w.webWang, report),
		waitingOnBody{Party: "guest", Text: "访客"}, "反馈回复轮到访客")
	// 未指派的任务仍有对手（创建者）：第三位成员的回复不改轮次，
	// 创建者一方回复则轮到本部门（照抄原型 counterpartMember）。
	w.s.Do(t, w.webLi, http.MethodPost, "/api/v1/tickets/"+plain+"/replies",
		map[string]string{"body_md": "我来跟进。"}, nil)
	assertWaitingOn(t, w.waitOf(t, w.webWang, plain),
		waitingOnBody{Party: "department", Text: "本部门（未指派）", Reason: "访客回复了"}, "未指派任务第三者回复不改轮次")
	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+plain+"/replies",
		map[string]string{"body_md": "本周内处理。"}, nil)
	assertWaitingOn(t, w.waitOf(t, w.webWang, plain),
		waitingOnBody{Party: "department", Text: "本部门（未指派）", Reason: "尚未指派"}, "未指派任务创建者回复轮到本部门")

	// 行 7：交付 → 创建者 / 已交付，待你验收；交付不改变状态。
	var deliverRes replyBody
	w.s.Do(t, w.liA, http.MethodPost, "/api/v1/tickets/"+task+"/replies",
		map[string]any{"body_md": "对照表已完成，共 9 条规则。", "deliver": true}, &deliverRes)
	assertWaitingOn(t, w.waitOf(t, w.webWang, task),
		waitingOnBody{Party: "creator", Member: wang, Text: "王工（发起人）", Reason: "已交付，待你验收"}, "交付后轮到创建者")
	if !deliverRes.Deliver {
		t.Error("交付回复的响应 deliver 应为 true")
	}

	// 行 8：验收不通过，创建者普通回复 → 指派人 / 发起人退回修改。
	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+task+"/replies",
		map[string]string{"body_md": "漏了 10-08 发布的 R4s 规则，请补上。"}, nil)
	assertWaitingOn(t, w.waitOf(t, w.webWang, task),
		waitingOnBody{Party: "assignee", Member: m, Text: "李工（指派人）", Reason: "发起人退回修改"}, "退回修改")

	// 行 9：改派后，原指派人的回复不再参与推导（照抄原型：按当前指派人重算）。
	// 王工的退回回复仍相关（对手一方），轮到赵工、原因仍是发起人回复了。
	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+task+"/assign",
		map[string]string{"assignee": "赵工"}, nil)
	// 李工（已不是指派人）再回复：不改变轮次；轮次仍由王工的退回回复推导。
	w.s.Do(t, w.liA, http.MethodPost, "/api/v1/tickets/"+task+"/replies",
		map[string]string{"body_md": "已不是我负责，供赵工参考。"}, nil)
	assertWaitingOn(t, w.waitOf(t, w.webWang, task),
		waitingOnBody{Party: "assignee", Member: strp("赵工"), Text: "赵工（指派人）", Reason: "发起人回复了"}, "改派后旧指派人回复不参与推导")
}

// strp 取字符串指针（测试里写期望值用）。
func strp(s string) *string { return &s }

// 交付标记只限指派人一方：非指派人的成员、创建者、访客、未指派与已关闭的
// 工单一律 403 deliver_assignee_only；被拒的回复不落地。
func Test交付只限指派人一方(t *testing.T) {
	w := newHandoffWorld(t)
	task := w.createHandoffTicket(t, w.wangA, "整理对照表", "李工").Number

	deliver := func(c *http.Client, body string) *http.Response {
		t.Helper()
		return w.s.Do(t, c, http.MethodPost, "/api/v1/tickets/"+task+"/replies",
			map[string]any{"body_md": body, "deliver": true}, nil)
	}
	assertRefused := func(res *http.Response, note string) {
		t.Helper()
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("%s：状态码 = %d, 想 403", note, res.StatusCode)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "deliver_assignee_only" {
			t.Errorf("%s：错误码 = %q, 想 deliver_assignee_only", note, got.Error.Code)
		}
	}

	// 第三位成员（赵工 的 Agent）不能交付。
	assertRefused(deliver(w.zhaoA, "我顺手做了"), "第三位成员")
	// 创建者（王工 的 Agent）也不能交付——活是指派给李工的。
	assertRefused(deliver(w.wangA, "我自己做完吧"), "创建者")
	// 访客不能交付。
	link := w.s.CreateGuestLink(t, w.webWang, task, map[string]any{"note": "检验科-张三"})
	assertRefused(w.s.Do(t, w.s.GuestClient(link.Key(t)), http.MethodPost,
		"/api/v1/tickets/"+task+"/replies",
		map[string]any{"body_md": "已解决", "deliver": true}, nil), "访客")

	// 指派人本人的 Agent 可以交付；交付不改变工单状态。
	var delivered replyBody
	w.s.Do(t, w.liA, http.MethodPost, "/api/v1/tickets/"+task+"/replies",
		map[string]any{"body_md": "已完成。", "deliver": true}, &delivered)
	if !delivered.Deliver {
		t.Error("指派人 Agent 的交付回复应成功且 deliver=true")
	}
	var body ticketResponseBody
	w.s.Do(t, w.webWang, http.MethodGet, "/api/v1/tickets/"+task, nil, &body)
	if body.Status != "open" {
		t.Errorf("交付后状态 = %q, 想仍为 open（验收后才关闭）", body.Status)
	}

	// 工单关闭后，即使是指派人也不能交付。
	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+task+"/close",
		map[string]string{"reason": "done"}, nil)
	res := deliver(w.liA, "再交一次")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("关闭后交付状态码 = %d, 想 403", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "deliver_assignee_only" || got.Error.Message != "工单已关闭，不能交付" {
		t.Errorf("关闭后交付错误 = %s/%s, 想 deliver_assignee_only/工单已关闭，不能交付",
			got.Error.Code, got.Error.Message)
	}

	// 被拒的回复不落地。
	w.s.Do(t, w.webWang, http.MethodGet, "/api/v1/tickets/"+task, nil, &body)
	for _, e := range body.Timeline {
		if e.Type == "reply" && e.Actor.Member != nil && e.Actor.Member.Name == "赵工" {
			t.Errorf("被拒的交付回复不应落地：%+v", e)
		}
	}
}

// 改派：guest 403、改派给当前指派人 422 already_assigned、查无此人 422
// unknown_member、写成成员 Agent 422 assignee_must_be_member；时间线记
// assigned{from,to}，Markdown 里写成「改派：李工 → 赵工」。
func Test改派接口的边界(t *testing.T) {
	w := newHandoffWorld(t)
	task := w.createHandoffTicket(t, w.wangA, "整理对照表", "李工").Number
	unassigned := w.createHandoffTicket(t, w.wangA, "未指派", "").Number

	link := w.s.CreateGuestLink(t, w.webWang, task, map[string]any{"note": "检验科-张三"})
	res := w.s.Do(t, w.s.GuestClient(link.Key(t)), http.MethodPost,
		"/api/v1/tickets/"+task+"/assign", map[string]string{"assignee": "赵工"}, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("访客改派状态码 = %d, 想 403", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "members_only" {
		t.Errorf("访客改派错误码 = %q, 想 members_only", got.Error.Code)
	}

	for name, body := range map[string]struct {
		assignee string
		code     string
	}{
		"改派给当前指派人":  {assignee: "李工", code: "already_assigned"},
		"查无此人":      {assignee: "张三丰", code: "unknown_member"},
		"写成成员Agent": {assignee: "工位", code: "assignee_must_be_member"},
	} {
		res := w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+task+"/assign",
			map[string]string{"assignee": body.assignee}, nil)
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%s：状态码 = %d, 想 422", name, res.StatusCode)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != body.code {
			t.Errorf("%s：错误码 = %q, 想 %s", name, got.Error.Code, body.code)
		}
	}

	var unchanged struct {
		Assignee any `json:"assignee"`
	}
	res = w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+unassigned+"/assign",
		map[string]string{"assignee": ""}, &unchanged)
	if res.StatusCode != http.StatusOK || unchanged.Assignee != nil {
		t.Fatalf("未指派工单再传空 assignee = %d/%v, 想 200 且保持未指派", res.StatusCode, unchanged.Assignee)
	}

	// 改派给赵工：响应带指派人、等待方与改派版 share_text；assigned 事件落地。
	var view struct {
		Assignee struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"assignee"`
		WaitingOn waitingOnBody `json:"waiting_on"`
		ShareText *string       `json:"share_text"`
	}
	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+task+"/assign",
		map[string]string{"assignee": "赵工"}, &view)
	if view.Assignee.Name != "赵工" || view.WaitingOn.Member == nil || *view.WaitingOn.Member != "赵工" {
		t.Errorf("改派响应 assignee/waiting_on 不对：%+v", view)
	}
	wantHuman := fmt.Sprintf("赵工：工单 %s「整理对照表」转给你了，网页 %s/t/%s（也在你的收件箱里）。下面这段交给你的 Agent：\n\n",
		task, w.s.URL(), task)
	if view.ShareText == nil || !strings.HasPrefix(*view.ShareText, wantHuman) {
		t.Errorf("改派 share_text 的人话行 = %v, 想 %q", view.ShareText, wantHuman)
	}
	var body ticketResponseBody
	w.s.Do(t, w.webZhao, http.MethodGet, "/api/v1/tickets/"+task, nil, &body)
	found := false
	for _, e := range body.Timeline {
		if e.Type == "assigned" && e.From == "李工" && e.To == "赵工" {
			found = true
		}
	}
	if !found {
		t.Errorf("时间线缺少 assigned{from:李工,to:赵工} 事件：%+v", body.Timeline)
	}
	// 改派给自己时不需要通知。
	var selfView struct {
		ShareText *string `json:"share_text"`
	}
	w.s.Do(t, w.webZhao, http.MethodPost, "/api/v1/tickets/"+task+"/assign",
		map[string]string{"assignee": "王工"}, nil)
	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+task+"/assign",
		map[string]string{"assignee": "王工"}, &selfView)
	if selfView.ShareText != nil {
		t.Errorf("改派给自己的 share_text = %q, 想为空", *selfView.ShareText)
	}

	// Markdown 时间线里的改派文案。
	res = w.s.Do(t, w.webWang, http.MethodGet, "/api/v1/tickets/"+task+"?format=markdown", nil, nil)
	var md strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		md.Write(buf[:n])
		if err != nil {
			break
		}
	}
	if !strings.Contains(md.String(), "改派：李工 → 赵工") {
		t.Errorf("Markdown 应含改派文案：\n%s", md.String())
	}
}
