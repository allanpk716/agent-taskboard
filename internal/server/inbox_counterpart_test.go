package server_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 收件箱项补齐「谁把它发给我」与「按谁归组」：改派后的触发者取执行改派
// 的成员；没有回复时 last_reply_at 为 null，并带回创建时间供前端显示。
func Test收件箱改派后触发者是改派人(t *testing.T) {
	w := newHandoffWorld(t)
	task := w.createHandoffTicket(t, w.webWang, "升级固件", "李工").Number

	w.s.Clock().Advance(time.Hour)
	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+task+"/assign",
		map[string]string{"assignee": "赵工"}, nil)

	inbox := w.inboxOf(t, w.webZhao)
	item := inbox.itemByNumber(task)
	if item == nil {
		t.Fatalf("赵工信箱缺少改派工单 %s：%+v", task, inbox.Items)
	}
	if item.Trigger.Type != "member" || item.Trigger.Name != "王工" {
		t.Errorf("触发者 = %+v, 想 member/王工", item.Trigger)
	}
	if item.Trigger.Member == nil || item.Trigger.Member.Name != "王工" {
		t.Errorf("触发者成员摘要 = %+v, 想 王工", item.Trigger.Member)
	}
	if item.Counterpart.Type != "member" || item.Counterpart.Name == nil || *item.Counterpart.Name != "王工" {
		t.Errorf("往来对象 = %+v, 想 member/王工", item.Counterpart)
	}
	if item.LastReplyAt != nil {
		t.Errorf("last_reply_at = %v, 想 null", item.LastReplyAt)
	}
	if want := w.s.Clock().Now().Add(-time.Hour); !item.CreatedAt.Equal(want) {
		t.Errorf("created_at = %v, 想 %v", item.CreatedAt, want)
	}
}

// 对方成员 Agent 的回复要让收件箱一眼看出是哪个主人的哪个 Agent。
func Test收件箱触发者区分对方成员Agent(t *testing.T) {
	w := newHandoffWorld(t)
	task := w.createHandoffTicket(t, w.webWang, "整理对照表", "李工").Number

	w.s.Clock().Advance(20 * time.Minute)
	w.s.Do(t, w.wangA, http.MethodPost, "/api/v1/tickets/"+task+"/replies",
		map[string]string{"body_md": "要写到机型吗？"}, nil)

	item := w.inboxOf(t, w.webLi).itemByNumber(task)
	if item == nil {
		t.Fatal("李工信箱缺少对方提问的工单")
	}
	if item.Trigger.Type != "member_agent" || item.Trigger.Name != "王工 的 Agent · 笔记本" {
		t.Errorf("触发者 = %+v, 想 member_agent/王工 的 Agent · 笔记本", item.Trigger)
	}
	if item.Trigger.Member == nil || item.Trigger.Member.Name != "王工" ||
		item.Trigger.Agent == nil || item.Trigger.Agent.Name != "笔记本" {
		t.Errorf("触发者成员/Agent = %+v/%+v, 想 王工/笔记本",
			item.Trigger.Member, item.Trigger.Agent)
	}
	if item.LastReplyAt == nil || !item.LastReplyAt.Equal(w.s.Clock().Now()) {
		t.Errorf("last_reply_at = %v, 想 %v", item.LastReplyAt, w.s.Clock().Now())
	}
}

// 访客修订说明虽然不是 reply 事件，也按最后一次回复时间进入收件箱摘要。
func Test收件箱把访客修订说明算作最后回复(t *testing.T) {
	w := newHandoffWorld(t)
	task := w.createHandoffTicket(t, w.webWang, "补充质控结果", "李工").Number
	link := w.s.CreateGuestLink(t, w.webWang, task, map[string]any{
		"note": "检验科-王五", "can_edit_brief": true,
	})
	guest := w.s.GuestClient(link.Key(t))

	w.s.Clock().Advance(30 * time.Minute)
	res := w.s.Do(t, guest, http.MethodPatch, "/api/v1/tickets/"+task,
		map[string]any{"brief_md": "现象：ALT 偏高。", "base_revision": 1, "author_name": "王五"}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("访客修订说明状态码 = %d, 想 200", res.StatusCode)
	}
	w.s.Clock().Advance(10 * time.Minute)
	res = w.s.Do(t, guest, http.MethodPatch, "/api/v1/tickets/"+task,
		map[string]any{"brief_md": "现象：ALT 偏高，已复测两次。", "base_revision": 2, "author_name": "王五"}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("访客第二次修订说明状态码 = %d, 想 200", res.StatusCode)
	}

	item := w.inboxOf(t, w.webLi).itemByNumber(task)
	if item == nil {
		t.Fatal("李工信箱缺少访客修订后的工单")
	}
	if item.Reason != "访客修订了说明" {
		t.Errorf("reason = %q, 想 访客修订了说明", item.Reason)
	}
	if item.Trigger.Type != "guest" || item.Trigger.Name != "王五" {
		t.Errorf("触发者 = %+v, 想 guest/王五", item.Trigger)
	}
	if item.LastReplyAt == nil || !item.LastReplyAt.Equal(w.s.Clock().Now()) {
		t.Errorf("last_reply_at = %v, 想 %v", item.LastReplyAt, w.s.Clock().Now())
	}
}

// 自派自的工单归「我自己」，未指派待初筛的工单不归任何往来对象。
func Test收件箱标记自派自与待初筛的往来对象(t *testing.T) {
	w := newHandoffWorld(t)
	self := w.createHandoffTicket(t, w.webWang, "整理自己的周计划", "王工").Number
	plain := w.createHandoffTicket(t, w.webWang, "新员工账号", "").Number

	inbox := w.inboxOf(t, w.webWang)
	selfItem := inbox.itemByNumber(self)
	if selfItem == nil {
		t.Fatalf("王工信箱缺少自派自工单 %s：%+v", self, inbox.Items)
	}
	if selfItem.Counterpart.Type != "self" || selfItem.Counterpart.Name == nil ||
		*selfItem.Counterpart.Name != "王工" || selfItem.Counterpart.Member == nil {
		t.Errorf("自派自往来对象 = %+v, 想 self/王工", selfItem.Counterpart)
	}

	plainItem := inbox.itemByNumber(plain)
	if plainItem == nil {
		t.Fatalf("初筛人收件箱缺少未指派工单 %s：%+v", plain, inbox.Items)
	}
	if plainItem.Counterpart.Type != "pending_triage" ||
		plainItem.Counterpart.Name != nil || plainItem.Counterpart.Member != nil {
		t.Errorf("待初筛往来对象 = %+v, 想 pending_triage 且不指向人", plainItem.Counterpart)
	}
	if plainItem.Trigger.Type != "member" || plainItem.Trigger.Name != "王工" {
		t.Errorf("待初筛触发者 = %+v, 想 member/王工", plainItem.Trigger)
	}
}

// 反馈单按访客归组；访客自报姓名用于展示，不用成员创建者的名字。
func Test收件箱反馈单按访客归组(t *testing.T) {
	w := newHandoffWorld(t)
	created, guest := createFeedbackRequest(t, w, "UD-2300 质控结果偏高", "李工")

	w.s.Clock().Advance(15 * time.Minute)
	res := w.s.Do(t, guest, http.MethodPatch, "/api/v1/tickets/"+created.Number,
		map[string]any{"brief_md": "现象：ALT 偏高。", "base_revision": 1, "author_name": "王五"}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("访客补全说明状态码 = %d, 想 200", res.StatusCode)
	}

	item := w.inboxOf(t, w.webLi).itemByNumber(created.Number)
	if item == nil {
		t.Fatal("李工信箱缺少访客补全后的反馈")
	}
	if item.Counterpart.Type != "guest" || item.Counterpart.Name == nil || *item.Counterpart.Name != "王五" {
		t.Errorf("反馈往来对象 = %+v, 想 guest/王五", item.Counterpart)
	}
	if item.Trigger.Type != "guest" || item.Trigger.Name != "王五" {
		t.Errorf("反馈触发者 = %+v, 想 guest/王五", item.Trigger)
	}
}

// 「访客未补全说明」没有触发动作，触发者仍取访客本身，而不是建单成员。
func Test收件箱访客未补全说明时触发者取访客(t *testing.T) {
	w := newHandoffWorld(t)
	res := w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets", map[string]any{
		"kind": "report", "title": "等待补充的反馈", "brief_md": "", "labels": []string{"机型:UD-2300"},
		"assignee":   "李工",
		"guest_link": map[string]any{"note": "检验科 张三", "valid_days": 7, "can_edit_brief": true},
	}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("请人反馈建单状态码 = %d, 想 201", res.StatusCode)
	}
	var created feedbackCreateResponse
	tbtest.DecodeJSON(t, res, &created)

	w.s.Clock().Advance(7 * 24 * time.Hour)
	w.webLi = w.s.Login("李工", "secret-pass")
	item := w.inboxOf(t, w.webLi).itemByNumber(created.Number)
	if item == nil {
		t.Fatalf("链接过期后李工信箱缺少访客未补全说明的反馈；waiting_on=%+v items=%+v",
			w.waitOf(t, w.webLi, created.Number), w.inboxOf(t, w.webLi).Items)
	}
	if item.Reason != "访客未补全说明" {
		t.Fatalf("reason = %q, 想 访客未补全说明", item.Reason)
	}
	if item.Trigger.Type != "guest" || item.Trigger.Name != "检验科 张三" {
		t.Errorf("触发者 = %+v, 想 guest/检验科 张三", item.Trigger)
	}
	if item.Counterpart.Type != "guest" {
		t.Errorf("往来对象 = %+v, 想 guest", item.Counterpart)
	}
}

// 自己名下 Agent 的回复也要能从触发者结构里看出主人和 Agent 名字。
func Test收件箱触发者区分自己的成员Agent(t *testing.T) {
	w := newHandoffWorld(t)
	self := w.createHandoffTicket(t, w.webWang, "自己的周计划", "王工").Number

	w.s.Clock().Advance(5 * time.Minute)
	w.s.Do(t, w.wangA, http.MethodPost, "/api/v1/tickets/"+self+"/replies",
		map[string]string{"body_md": "我先草拟了三件事。"}, nil)

	item := w.inboxOf(t, w.webWang).itemByNumber(self)
	if item == nil {
		t.Fatal("王工信箱缺少自己的 Agent 回复后的工单")
	}
	if item.Trigger.Type != "member_agent" || item.Trigger.Name != "王工 的 Agent · 笔记本" {
		t.Errorf("触发者 = %+v, 想 member_agent/王工 的 Agent · 笔记本", item.Trigger)
	}
	if item.Trigger.Member == nil || item.Trigger.Member.Name != "王工" ||
		item.Trigger.Agent == nil || item.Trigger.Agent.Name != "笔记本" {
		t.Errorf("触发者成员/Agent = %+v/%+v, 想 王工/笔记本",
			item.Trigger.Member, item.Trigger.Agent)
	}
}

// 有回复的按最后回复时间倒序；没有回复的用创建时间参与同一顺序。
func Test收件箱默认按最后回复时间倒序(t *testing.T) {
	w := newHandoffWorld(t)
	older := w.createHandoffTicket(t, w.webWang, "较早创建", "李工").Number
	w.s.Clock().Advance(30 * time.Minute)
	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+older+"/replies",
		map[string]string{"body_md": "要写到机型吗？"}, nil)
	w.s.Clock().Advance(10 * time.Minute)
	newer := w.createHandoffTicket(t, w.webWang, "较晚创建但未回复", "李工").Number

	inbox := w.inboxOf(t, w.webLi)
	if len(inbox.Items) < 2 || inbox.Items[0].Number != newer || inbox.Items[1].Number != older {
		got := []string{}
		for _, it := range inbox.Items {
			got = append(got, it.Number)
		}
		t.Fatalf("收件箱顺序 = %v, 想 %s 在 %s 前", got, newer, older)
	}
	if inbox.Items[0].LastReplyAt != nil || inbox.Items[1].LastReplyAt == nil {
		t.Errorf("最后回复时间 = [%v, %v], 想 [null, 有时间]",
			inbox.Items[0].LastReplyAt, inbox.Items[1].LastReplyAt)
	}
}

func (w *handoffWorld) waitingCounterpartsOf(t testing.TB, c *http.Client) inboxBody {
	t.Helper()
	var body inboxBody
	res := w.s.Do(t, c, http.MethodGet, "/api/v1/inbox/waiting-on-counterparts", nil, &body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/inbox/waiting-on-counterparts 状态码 = %d, 想 200", res.StatusCode)
	}
	return body
}

// 「在等他」是只读参考：字段沿用收件箱摘要，但不计入收件箱数量，
// 也只包含当前轮到往来对象的开启工单。
func Test在等他查询不计入收件箱且只含轮到对方的工单(t *testing.T) {
	w := newHandoffWorld(t)
	assigned := w.createHandoffTicket(t, w.webWang, "新指派后在等李工", "李工").Number
	asked := w.createHandoffTicket(t, w.webWang, "李工提问后轮到王工", "李工").Number
	delivered := w.createHandoffTicket(t, w.webWang, "李工交付后在等验收", "李工").Number
	waiting := w.createHandoffTicket(t, w.webWang, "王工追问后在等李工", "李工").Number
	self := w.createHandoffTicket(t, w.webWang, "自己的单", "王工").Number
	closed := w.createHandoffTicket(t, w.webWang, "已关闭", "李工").Number

	w.s.Do(t, w.webLi, http.MethodPost, "/api/v1/tickets/"+asked+"/replies",
		map[string]string{"body_md": "要写到机型吗？"}, nil)
	w.s.Clock().Advance(10 * time.Minute)
	w.s.Do(t, w.webLi, http.MethodPost, "/api/v1/tickets/"+delivered+"/replies",
		map[string]any{"body_md": "已完成。", "deliver": true}, nil)
	w.s.Clock().Advance(10 * time.Minute)
	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+waiting+"/replies",
		map[string]string{"body_md": "请补充测试截图。"}, nil)
	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+closed+"/close",
		map[string]string{"reason": "done"}, nil)

	inbox := w.inboxOf(t, w.webWang)
	if got := inbox.reasonByNumber(); got[asked] != "指派人回复了，待你答复" {
		t.Fatalf("王工信箱提问工单 = %v, 想 指派人回复了，待你答复", got[asked])
	}
	if _, ok := inbox.reasonByNumber()[assigned]; ok {
		t.Errorf("新指派后在等李工的 %s 不应在王工信箱：%+v", assigned, inbox.Items)
	}
	if got := inbox.reasonByNumber()[delivered]; got != "已交付，待你验收" {
		t.Errorf("交付后的 %s 应在王工信箱，原因 = %q", delivered, got)
	}

	waitingBody := w.waitingCounterpartsOf(t, w.webWang)
	want := []string{waiting, assigned}
	if waitingBody.Count != len(want) || len(waitingBody.Items) != len(want) {
		t.Fatalf("在等他数量 = %d/%d, 想 2：%+v", waitingBody.Count, len(waitingBody.Items), waitingBody.Items)
	}
	for i, number := range want {
		if waitingBody.Items[i].Number != number {
			t.Fatalf("在等他顺序 = [%s %s], 想 %v",
				waitingBody.Items[0].Number, waitingBody.Items[len(waitingBody.Items)-1].Number, want)
		}
	}
	item := waitingBody.itemByNumber(waiting)
	if item == nil {
		t.Fatalf("在等他缺少追问工单 %s：%+v", waiting, waitingBody.Items)
	}
	if item.Role != "creator" || item.Counterpart.Type != "member" ||
		item.Counterpart.Name == nil || *item.Counterpart.Name != "李工" {
		t.Errorf("追问工单 role/往来对象 = %s/%+v, 想 creator/member/李工", item.Role, item.Counterpart)
	}
	if item.Trigger.Type != "member" || item.Trigger.Name != "王工" {
		t.Errorf("追问工单触发者 = %+v, 想 member/王工", item.Trigger)
	}
	for _, number := range []string{asked, delivered, self, closed} {
		if waitingBody.itemByNumber(number) != nil {
			t.Errorf("在等他不应包含 %s", number)
		}
	}
}
