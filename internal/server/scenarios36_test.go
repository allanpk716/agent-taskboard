package server_test

// #36 场景 1/2/4（照搬 prototype/member-handoff）与收件箱接口的黑盒测试。
// 附件（#33）与模板（#34）的原型步骤按现有接口改写：交付回复不带附件引用。

import (
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// inboxBody 是 GET /api/v1/inbox 的响应结构。
type inboxBody struct {
	Member string `json:"member"`
	Count  int    `json:"count"`
	Items  []struct {
		Number   string `json:"number"`
		Title    string `json:"title"`
		Role     string `json:"role"`
		Reason   string `json:"reason"`
		SinceSeq *int64 `json:"since_seq"`
		ReadNew  string `json:"read_new"`
		ReadFull string `json:"read_full"`
	} `json:"items"`
}

// inboxOf 拉取 c 的收件箱，失败时终止测试。
func (w *handoffWorld) inboxOf(t testing.TB, c *http.Client) inboxBody {
	t.Helper()
	var body inboxBody
	res := w.s.Do(t, c, http.MethodGet, "/api/v1/inbox", nil, &body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/inbox 状态码 = %d, 想 200", res.StatusCode)
	}
	return body
}

// reasonByNumber 把收件箱压成「编号 → 原因」映射，方便断言。
func (b inboxBody) reasonByNumber() map[string]string {
	out := map[string]string{}
	for _, it := range b.Items {
		out[it.Number] = it.Reason
	}
	return out
}

// readMarkdown 读取 Markdown 视图的正文。
func (w *handoffWorld) readMarkdown(t testing.TB, c *http.Client, path string) string {
	t.Helper()
	res := w.s.Do(t, c, http.MethodGet, path, nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s 状态码 = %d, 想 200", path, res.StatusCode)
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

// 场景 1（派活到验收，照搬原型）：王工的 Agent 建单指派李工；李工的 Agent 从
// 收件箱自己发现工单；交付后工单不关闭、出现在王工的收件箱（待你验收）；
// 王工的 Agent 验收关闭后两个收件箱都清空。
func Test场景1派活到验收(t *testing.T) {
	w := newHandoffWorld(t)
	created := w.createHandoffTicket(t, w.wangA, "BH-5100 质控模块回归测试", "李工")
	number := created.Number

	// 李工的 Agent 自己从收件箱发现这张工单：新指派给你。
	liInbox := w.inboxOf(t, w.liA)
	if liInbox.Member != "李工" || liInbox.Count != 1 {
		t.Fatalf("李工信箱 = %s/%d 项, 想 李工/1", liInbox.Member, liInbox.Count)
	}
	item := liInbox.Items[0]
	if item.Number != number || item.Role != "assignee" || item.Reason != "新指派给你" {
		t.Errorf("收件箱第一项 = %+v, 想 %s/assignee/新指派给你", item, number)
	}
	if item.ReadNew != "/api/v1/tickets/"+number+"/timeline?after=0" {
		t.Errorf("read_new = %q, 想从 0 开始读", item.ReadNew)
	}
	if item.ReadFull != "/api/v1/tickets/"+number+"?format=markdown" {
		t.Errorf("read_full = %q", item.ReadFull)
	}
	// 同一成员名下的所有 Agent（和本人）共用一份收件箱。
	if webInbox := w.inboxOf(t, w.webLi); webInbox.Count != 1 || webInbox.Items[0].Number != number {
		t.Errorf("李工本人的收件箱应与他的 Agent 一致：%+v", webInbox)
	}

	// 李工的 Agent 以 Markdown 读取：末尾「下一步」按 指派人×轮到我 生成。
	md := w.readMarkdown(t, w.liA, "/api/v1/tickets/"+number+"?format=markdown")
	for _, want := range []string{
		"你是 **李工 的 Agent · 工位**，在这张工单里的角色是 **指派人**",
		"现在轮到：**李工（指派人）**",
		"POST /api/v1/tickets/" + number + "/replies",
		`"deliver": true`,
		"交付后**不要关闭工单**，由发起人验收后关闭",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("指派人的 Markdown 缺少 %q：\n%s", want, md)
		}
	}

	// 交付：deliver:true；工单不关闭，轮到王工验收。
	var delivered replyBody
	w.s.Do(t, w.liA, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "6 条规则全部测完，1 条失败（1-3s 规则误报）。", "deliver": true}, &delivered)
	if delivered.WaitingOn.Party != "creator" || delivered.WaitingOn.Reason != "已交付，待你验收" {
		t.Errorf("交付后 waiting_on = %+v, 想 creator/已交付，待你验收", delivered.WaitingOn)
	}

	// 王工的 Agent 从收件箱得知「待你验收」。
	wangInbox := w.inboxOf(t, w.wangA)
	if got := wangInbox.reasonByNumber()[number]; got != "已交付，待你验收" {
		t.Errorf("王工信箱里 %s 的原因 = %q, 想 已交付，待你验收", number, got)
	}
	for _, it := range wangInbox.Items {
		if it.Number == number && it.Role != "creator" {
			t.Errorf("王工在这张工单里的角色 = %s, 想 creator", it.Role)
		}
	}

	// Markdown 的验收指引：通过就 close{done}，不通过就普通回复。
	md = w.readMarkdown(t, w.wangA, "/api/v1/tickets/"+number+"?format=markdown")
	for _, want := range []string{
		"你是 **王工 的 Agent · 笔记本**，在这张工单里的角色是 **发起人**",
		"对照说明中的验收标准检查交付物",
		"POST /api/v1/tickets/" + number + "/close",
		`{"reason": "done"}`,
		"普通回复，写明哪里不符合，工单会退回指派人",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("发起人的 Markdown 缺少 %q：\n%s", want, md)
		}
	}

	// 验收通过：以「完成」关闭；两个收件箱都清空。
	var closed struct {
		Status string `json:"status"`
	}
	res := w.s.Do(t, w.wangA, http.MethodPost, "/api/v1/tickets/"+number+"/close",
		map[string]string{"reason": "done"}, &closed)
	if res.StatusCode != http.StatusOK || closed.Status != "closed" {
		t.Fatalf("验收关闭状态码/状态 = %d/%s, 想 200/closed", res.StatusCode, closed.Status)
	}
	if inbox := w.inboxOf(t, w.liA); inbox.Count != 0 {
		t.Errorf("关闭后李工信箱应清空，仍有 %d 项", inbox.Count)
	}
	if inbox := w.inboxOf(t, w.wangA); inbox.Count != 0 {
		t.Errorf("关闭后王工信箱应清空，仍有 %d 项", inbox.Count)
	}
	// 已关闭的工单无等待方。
	assertWaitingOn(t, w.waitOf(t, w.webWang, number),
		waitingOnBody{Party: "none", Text: "无（已关闭）"}, "已关闭")
}

// 场景 2（提问、退回、再交付，照搬原型）：轮次在两人之间来回；退回修改后
// 收件箱给出只读新内容的地址；最后由王工本人在网页上验收关闭。
func Test场景2提问退回再交付(t *testing.T) {
	w := newHandoffWorld(t)
	number := w.createHandoffTicket(t, w.wangA, "整理 Q3 质控规则对照表", "李工").Number

	// 李工的 Agent 提问（普通回复）→ 轮到王工。
	var asked replyBody
	w.s.Do(t, w.liA, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "「适用机型」要细到软件版本吗？"}, &asked)
	if asked.WaitingOn.Party != "creator" || asked.WaitingOn.Reason != "指派人回复了，待你答复" {
		t.Errorf("提问后 waiting_on = %+v, 想 creator/指派人回复了，待你答复", asked.WaitingOn)
	}

	// 王工的收件箱：指派人回复了，待你答复；since_seq 指向那条提问。
	wangInbox := w.inboxOf(t, w.wangA)
	if got := wangInbox.reasonByNumber()[number]; got != "指派人回复了，待你答复" {
		t.Errorf("王工信箱原因 = %q, 想 指派人回复了，待你答复", got)
	}
	for _, it := range wangInbox.Items {
		if it.Number == number {
			if it.SinceSeq == nil || *it.SinceSeq != 3 {
				t.Errorf("since_seq = %v, 想 3（提问回复的事件序号）", it.SinceSeq)
			}
		}
	}

	// 王工答复 → 轮回李工；李工交付；王工不通过：普通回复退回。
	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "不用，写到机型即可。"}, nil)
	w.s.Do(t, w.liA, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "对照表已完成，共 9 条规则。", "deliver": true}, nil)
	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "漏了 10-08 发布的 R4s 规则，请补上。"}, nil)

	// 李工的收件箱：发起人退回修改；read_new 从李工自己的最后一条事件开始。
	liInbox := w.inboxOf(t, w.liA)
	var readNew string
	for _, it := range liInbox.Items {
		if it.Number == number {
			if it.Reason != "发起人退回修改" {
				t.Errorf("退回后收件箱原因 = %q, 想 发起人退回修改", it.Reason)
			}
			readNew = it.ReadNew
		}
	}
	if want := "/api/v1/tickets/" + number + "/timeline?after=5"; readNew != want {
		t.Errorf("read_new = %q, 想 %q（李工最后一条事件是交付回复 seq=5）", readNew, want)
	}
	// 只读新内容：拿到的是王工的退回回复（seq=6）。
	var inc timelineBody
	w.s.Do(t, w.liA, http.MethodGet, readNew, nil, &inc)
	if len(inc.Events) != 1 || inc.Events[0].Seq != 6 ||
		inc.Events[0].BodyMD != "漏了 10-08 发布的 R4s 规则，请补上。" {
		t.Errorf("增量时间线 = %+v, 想只有 seq=6 的退回回复", inc.Events)
	}

	// 退回的提示在 Markdown 里写明来源。
	md := w.readMarkdown(t, w.liA, "/api/v1/tickets/"+number+"?format=markdown")
	if !strings.Contains(md, "发起人在 [6] 退回了上次的交付") {
		t.Errorf("退回后 Markdown 应提示来源事件：\n%s", md)
	}

	// 再次交付；王工本人在网页上验收并关闭。
	w.s.Do(t, w.liA, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "已补上 R4s，共 10 条。", "deliver": true}, nil)
	res := w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+number+"/close",
		map[string]string{"reason": "done"}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("本人验收关闭状态码 = %d, 想 200", res.StatusCode)
	}
	// 两次交付都留在时间线上。
	var body ticketResponseBody
	w.s.Do(t, w.webWang, http.MethodGet, "/api/v1/tickets/"+number, nil, &body)
	delivers := 0
	for _, e := range body.Timeline {
		if e.Type == "reply" && e.Deliver {
			delivers++
		}
	}
	if delivers != 2 {
		t.Errorf("时间线上应有 2 条交付回复，实际 %d 条", delivers)
	}
}

// 场景 4（反馈转给同事，照搬原型）：反馈的另一方是访客，不是发起人——李工
// 回复后轮到访客，王工的收件箱里不会出现这张工单。
func Test场景4反馈转给同事(t *testing.T) {
	w := newHandoffWorld(t)
	// 王工建一张反馈并自派自，发给检验科张三。
	var fb createAssignedResponse
	res := w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets", map[string]any{
		"kind": "report", "title": "BH-7600CS 质控结果偏高",
		"brief_md": "检验科反馈：ALT 质控连续 3 次偏高 2SD 以上。",
		"labels":   []string{"机型:UD-2300"}, "assignee": "王工",
	}, &fb)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建反馈状态码 = %d", res.StatusCode)
	}
	link := w.s.CreateGuestLink(t, w.webWang, fb.Number, map[string]any{"note": "检验科-张三"})
	guest := w.s.GuestClient(link.Key(t))

	// 张三回复试剂批号 → 轮到王工（指派人）；王工的收件箱出现「访客回复了」。
	w.s.Do(t, guest, http.MethodPost, "/api/v1/tickets/"+fb.Number+"/replies",
		map[string]string{"body_md": "试剂批号 R20260930。", "author_name": "张三"}, nil)
	if got := w.inboxOf(t, w.webWang).reasonByNumber()[fb.Number]; got != "访客回复了" {
		t.Errorf("访客回复后王工信箱原因 = %q, 想 访客回复了", got)
	}

	// 王工转给李工：李工的收件箱显示「访客回复了」（最后一条相关回复是张三的）。
	var view struct {
		Assignee struct {
			Name string `json:"name"`
		} `json:"assignee"`
		WaitingOn waitingOnBody `json:"waiting_on"`
	}
	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+fb.Number+"/assign",
		map[string]string{"assignee": "李工"}, &view)
	if got := w.inboxOf(t, w.webLi).reasonByNumber()[fb.Number]; got != "访客回复了" {
		t.Errorf("转派后李工信箱原因 = %q, 想 访客回复了", got)
	}

	// 李工回复张三 → 轮到访客（反馈的对手是访客）；王工的收件箱没有这张工单。
	var replied replyBody
	w.s.Do(t, w.webLi, http.MethodPost, "/api/v1/tickets/"+fb.Number+"/replies",
		map[string]string{"body_md": "该批号有已知问题，请换批号 R20261008 复测。"}, &replied)
	if replied.WaitingOn.Party != "guest" {
		t.Errorf("反馈上成员回复后 waiting_on = %+v, 想 guest", replied.WaitingOn)
	}
	if _, ok := w.inboxOf(t, w.webWang).reasonByNumber()[fb.Number]; ok {
		t.Error("王工的收件箱不应再出现这张反馈")
	}

	// 张三以 Markdown 读取：轮到访客，「下一步」给回复示例。
	md := w.readMarkdown(t, guest, "/api/v1/tickets/"+fb.Number+"?format=markdown")
	for _, want := range []string{
		"- 发起人：王工 · 指派人：李工",
		"现在轮到：**访客**",
		"POST /api/v1/tickets/" + fb.Number + "/replies",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("访客的 Markdown 缺少 %q：\n%s", want, md)
		}
	}
}

// 收件箱只对成员开放：访客 403 members_only，匿名 401；未指派的工单进入
// 管理员初筛人的收件箱（#75 默认规则），非初筛人不会看到。
func Test收件箱仅限成员且未指派工单只进初筛人邮箱(t *testing.T) {
	w := newHandoffWorld(t)
	plain := w.createHandoffTicket(t, w.webWang, "未指派", "").Number
	assigned := w.createHandoffTicket(t, w.webWang, "已指派", "李工").Number

	res := w.s.Do(t, w.s.NewClient(), http.MethodGet, "/api/v1/inbox", nil, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("匿名收件箱状态码 = %d, 想 401", res.StatusCode)
	}
	link := w.s.CreateGuestLink(t, w.webWang, assigned, map[string]any{"note": "检验科-张三"})
	res = w.s.Do(t, w.s.GuestClient(link.Key(t)), http.MethodGet, "/api/v1/inbox", nil, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("访客收件箱状态码 = %d, 想 403", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "members_only" {
		t.Errorf("访客收件箱错误码 = %q, 想 members_only", got.Error.Code)
	}

	if got := w.inboxOf(t, w.webWang).reasonByNumber()[plain]; got != "待初筛" {
		t.Errorf("管理员默认初筛人的收件箱 = %v, 想包含 %s（待初筛）", got, plain)
	}
	for _, inbox := range []inboxBody{w.inboxOf(t, w.webLi), w.inboxOf(t, w.webZhao)} {
		for _, it := range inbox.Items {
			if it.Number == plain {
				t.Errorf("非初筛人 %s 的收件箱不应包含 %s", inbox.Member, plain)
			}
		}
	}
	if got := w.inboxOf(t, w.webLi).reasonByNumber(); got[assigned] != "新指派给你" {
		t.Errorf("李工信箱 = %v, 想包含已指派的 %s", got, assigned)
	}
}

// 已接受边界（spec「等待方与收件箱」）：重新开启后按最后一条相关回复推导
// 轮次；Markdown「下一步」提示重开的人回复说明原因。
func Test重开后按最后一条相关回复推导并提示重开者(t *testing.T) {
	w := newHandoffWorld(t)
	number := w.createHandoffTicket(t, w.wangA, "整理对照表", "李工").Number

	// 李工提问、王工答复后轮到李工；王工直接关闭再重开。
	w.s.Do(t, w.liA, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "要写到机型吗？"}, nil)
	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "写到机型即可。"}, nil)
	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+number+"/close",
		map[string]string{"reason": "wontfix"}, nil)
	res := w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+number+"/reopen", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("重开状态码 = %d, 想 200", res.StatusCode)
	}

	// 重开后轮次回到最后一条相关回复推导的结果：王工（发起人）答复过 → 轮到李工。
	assertWaitingOn(t, w.waitOf(t, w.webWang, number),
		waitingOnBody{Party: "assignee", Member: strp("李工"), Reason: "发起人回复了"}, "重开后")
	// 李工（轮到他但不是重开者）没有重开提示。
	md := w.readMarkdown(t, w.liA, "/api/v1/tickets/"+number+"?format=markdown")
	if strings.Contains(md, "重新开启") && strings.Contains(md, "说明重开原因") {
		t.Errorf("非重开者不应收到重开提示：\n%s", md)
	}
	// 王工（重开者，即使不轮到他）被提示回复说明原因。
	md = w.readMarkdown(t, w.wangA, "/api/v1/tickets/"+number+"?format=markdown")
	for _, want := range []string{
		"这张工单刚由你重新开启",
		"先回复说明重开原因：",
		"POST /api/v1/tickets/" + number + "/replies",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("重开者的 Markdown 缺少 %q：\n%s", want, md)
		}
	}
}
