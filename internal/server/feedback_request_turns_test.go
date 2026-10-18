package server_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func createFeedbackRequest(t testing.TB, w *handoffWorld, title, assignee string) (feedbackCreateResponse, *http.Client) {
	t.Helper()
	body := map[string]any{
		"kind": "report", "title": title, "brief_md": "", "labels": []string{"机型:UD-2300"},
		"guest_link": map[string]any{"note": "检验科-张三", "can_edit_brief": true},
	}
	if assignee != "" {
		body["assignee"] = assignee
	}
	res := w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets", body, nil)
	if res.StatusCode != http.StatusCreated {
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		t.Fatalf("请人反馈建单状态码 = %d（%s），想 201", res.StatusCode, got.Error.Code)
	}
	var created feedbackCreateResponse
	tbtest.DecodeJSON(t, res, &created)
	return created, w.s.GuestClient(strings.TrimPrefix(created.GuestLink.URL, "/g/"))
}

func Test手动补充可填写说明链接与撤销链接的轮次(t *testing.T) {
	w := newHandoffWorld(t)
	created := w.createHandoffTicket(t, w.webWang, "手动建单后补链接", "李工")
	number := created.Number

	normal := mustCreateGuestLink(t, w.s, w.webWang, number, map[string]any{"note": "普通访客"})
	_ = w.s.GuestClient(guestKeyOf(t, normal))
	assertWaitingOn(t, w.waitOf(t, w.webWang, number), waitingOnBody{
		Party: "assignee", Member: strp("李工"), Text: "李工（指派人）", Reason: "新指派给你",
	}, "普通访客链接不触发待补全")

	edit := mustCreateGuestLink(t, w.s, w.webWang, number, map[string]any{
		"note": "检验科-张三", "can_edit_brief": true,
	})
	assertWaitingOn(t, w.waitOf(t, w.webWang, number), waitingOnBody{
		Party: "guest", Text: "访客补全说明",
	}, "手动补发可填写说明链接")

	res := w.s.Do(t, w.webWang, http.MethodDelete,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(edit.ID, 10), nil, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("撤销可填写说明链接状态码 = %d, 想 204", res.StatusCode)
	}
	assertWaitingOn(t, w.waitOf(t, w.webWang, number), waitingOnBody{
		Party: "assignee", Member: strp("李工"), Text: "李工（指派人）", Reason: "访客未补全说明",
	}, "撤销可填写说明链接")
	if got := notificationsOf(t, w, w.webLi, "").Items; len(got) != 1 || got[0].Reason != "assigned" {
		t.Errorf("撤销链接不应产生新通知：%+v", got)
	}
}

func Test未指派请人反馈由初筛人接收补全通知(t *testing.T) {
	w := newHandoffWorld(t)
	mustSetTriager(t, w.s, w.webWang, memberID(t, w.s, w.webWang, "李工"), true)
	created, guest := createFeedbackRequest(t, w, "未指派的反馈", "")

	assertWaitingOn(t, w.waitOf(t, w.webWang, created.Number), waitingOnBody{
		Party: "guest", Text: "访客补全说明",
	}, "未指派也先等访客")
	res := w.s.Do(t, guest, http.MethodPatch, "/api/v1/tickets/"+created.Number,
		map[string]any{"brief_md": "补充说明。", "base_revision": 1}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("访客补全说明状态码 = %d, 想 200", res.StatusCode)
	}
	assertWaitingOn(t, w.waitOf(t, w.webWang, created.Number), waitingOnBody{
		Party: "department", Text: "本部门（未指派）", Reason: "访客补全了说明",
	}, "访客补全后轮到本部门")
	if got := triagerInboxOf(t, w.s, w.webLi).reasonByNumber()[created.Number]; got != "访客补全了说明" {
		t.Errorf("初筛人收件箱原因 = %q, 想 访客补全了说明", got)
	}
	li := notificationsOf(t, w, w.webLi, "")
	if len(li.Items) != 1 || li.Items[0].Kind != "turn" || li.Items[0].Reason != "guest_completed" {
		t.Fatalf("初筛人通知 = %+v, 想 turn/guest_completed", li.Items)
	}
}

func Test访客链接过期未补全时无通知地轮到指派人(t *testing.T) {
	w := newHandoffWorld(t)
	res := w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets", map[string]any{
		"kind": "report", "title": "UD-2300 质控结果偏高",
		"brief_md": "", "labels": []string{"机型:UD-2300"}, "assignee": "李工",
		"guest_link": map[string]any{"note": "检验科-张三", "valid_days": 7, "can_edit_brief": true},
	}, nil)
	if res.StatusCode != http.StatusCreated {
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		t.Fatalf("请人反馈建单状态码 = %d（%s），想 201", res.StatusCode, got.Error.Code)
	}
	var created feedbackCreateResponse
	tbtest.DecodeJSON(t, res, &created)

	w.s.Clock().Advance(time.Hour)
	assertWaitingOn(t, w.waitOf(t, w.wangA, created.Number), waitingOnBody{
		Party: "guest", Text: "访客补全说明",
	}, "链接仍有效")
	w.s.Clock().Advance(7*24*time.Hour - time.Hour)
	assertWaitingOn(t, w.waitOf(t, w.wangA, created.Number), waitingOnBody{
		Party: "assignee", Member: strp("李工"), Text: "李工（指派人）",
		Reason: "访客未补全说明", Why: "guest_not_completed",
	}, "链接过期")
	if got := w.inboxOf(t, w.liA).reasonByNumber()[created.Number]; got != "访客未补全说明" {
		t.Errorf("链接过期后收件箱原因 = %q, 想 访客未补全说明", got)
	}
	if got := notificationsOf(t, w, w.liA, ""); len(got.Items) != 0 {
		t.Errorf("链接过期不应产生通知：%+v", got.Items)
	}
	var view ticketResponseBody
	w.s.Do(t, w.wangA, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &view)
	for _, e := range view.Timeline {
		if e.Type == "guest_link_expired" {
			t.Error("链接过期只应在读取时推导，不应写时间线事件")
		}
	}
}

// 「请人反馈」的说明补全参与轮次推导（#127）：访客还没填、也没有回复时，
// 只等待访客，不占任何成员的收件箱。
func Test请人反馈建单后等待访客补全且不进收件箱(t *testing.T) {
	w := newHandoffWorld(t)
	created, _ := createFeedbackRequest(t, w, "UD-2300 质控结果偏高", "李工")

	assertWaitingOn(t, w.waitOf(t, w.webWang, created.Number), waitingOnBody{
		Party: "guest", Text: "访客补全说明",
	}, "访客还没补全说明")
	if got := w.inboxOf(t, w.webLi).reasonByNumber()[created.Number]; got != "" {
		t.Errorf("指派人收件箱不应出现待补全工单，原因 = %q", got)
	}
	if got := w.inboxOf(t, w.webWang).reasonByNumber()[created.Number]; got != "" {
		t.Errorf("创建者收件箱不应出现待补全工单，原因 = %q", got)
	}
}

func Test访客补全说明进入指派人收件箱并只通知一次(t *testing.T) {
	w := newHandoffWorld(t)
	created, guest := createFeedbackRequest(t, w, "UD-2300 质控结果偏高", "李工")

	res := w.s.Do(t, guest, http.MethodPatch, "/api/v1/tickets/"+created.Number,
		map[string]any{"brief_md": "今早开始报 E-203。", "base_revision": 1}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("访客补全说明状态码 = %d, 想 200", res.StatusCode)
	}

	assertWaitingOn(t, w.waitOf(t, w.webWang, created.Number), waitingOnBody{
		Party: "assignee", Member: strp("李工"), Text: "李工（指派人）",
		Reason: "访客补全了说明", Why: "guest_completed",
	}, "访客首次补全说明")
	if got := w.inboxOf(t, w.webLi).reasonByNumber()[created.Number]; got != "访客补全了说明" {
		t.Errorf("指派人收件箱原因 = %q, 想 访客补全了说明", got)
	}

	li := notificationsOf(t, w, w.webLi, "")
	if len(li.Items) != 1 {
		t.Fatalf("指派人通知数 = %d, 想 1（只有补全说明）", len(li.Items))
	}
	latest := li.Items[0]
	if latest.Kind != "turn" || latest.Reason != "guest_completed" || latest.Event.Type != "brief_revised" {
		t.Fatalf("补全说明通知 = %s/%s/%s, 想 turn/guest_completed/brief_revised",
			latest.Kind, latest.Reason, latest.Event.Type)
	}
	wang := notificationsOf(t, w, w.webWang, "")
	if len(wang.Items) != 1 || wang.Items[0].Kind != "fyi" || wang.Items[0].Reason != "brief_revised" {
		t.Fatalf("非指派创建者只应收一条说明修订知会：%+v", wang.Items)
	}
}

func Test访客修订说明会更新原因和轮次事件(t *testing.T) {
	w := newHandoffWorld(t)
	created, guest := createFeedbackRequest(t, w, "UD-2300 质控结果偏高", "李工")
	revise := func(body string, revision int64) {
		t.Helper()
		res := w.s.Do(t, guest, http.MethodPatch, "/api/v1/tickets/"+created.Number,
			map[string]any{"brief_md": body, "base_revision": revision}, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("访客修订说明状态码 = %d, 想 200", res.StatusCode)
		}
	}
	revise("今早开始报 E-203。", 1)
	revise("今早开始报 E-203，重启后仍复现。", 2)

	assertWaitingOn(t, w.waitOf(t, w.webWang, created.Number), waitingOnBody{
		Party: "assignee", Member: strp("李工"), Text: "李工（指派人）", Reason: "访客修订了说明",
	}, "访客第二次修订")
	inbox := w.inboxOf(t, w.webLi)
	if got := inbox.reasonByNumber()[created.Number]; got != "访客修订了说明" {
		t.Fatalf("指派人收件箱原因 = %q, 想 访客修订了说明", got)
	}
	for _, it := range inbox.Items {
		if it.Number == created.Number && (it.SinceSeq == nil || *it.SinceSeq != 5) {
			t.Errorf("收件箱 since_seq = %v, 想 5（第二次修订事件）", it.SinceSeq)
		}
	}

	// 成员追问后轮到访客；访客不回复而再次修改说明，也应轮回指派人。
	w.s.Do(t, w.webLi, http.MethodPost, "/api/v1/tickets/"+created.Number+"/replies",
		map[string]string{"body_md": "请附上报警截图的时间。"}, nil)
	assertWaitingOn(t, w.waitOf(t, w.webWang, created.Number), waitingOnBody{
		Party: "guest", Text: "访客",
	}, "成员追问后")
	revise("08:12 报 E-203，08:40 又报一次。", 3)
	assertWaitingOn(t, w.waitOf(t, w.webWang, created.Number), waitingOnBody{
		Party: "assignee", Member: strp("李工"), Text: "李工（指派人）", Reason: "访客修订了说明",
	}, "访客以修订代替回复")
}
