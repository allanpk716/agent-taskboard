package server_test

import (
	"net/http"
	"strings"
	"testing"
)

func Test知会通知覆盖关闭重开改派和取消指派(t *testing.T) {
	w := newHandoffWorld(t)
	number := w.createHandoffTicket(t, w.webWang, "整理对照表", "李工").Number

	assertReasons := func(note string, c *http.Client, want ...string) {
		t.Helper()
		got := notificationsOf(t, w, c, "")
		if len(got.Items) != len(want) {
			t.Fatalf("%s：通知 = %+v，想原因 %v", note, got.Items, want)
		}
		for i, wantKindReason := range want {
			wantKind, wantReason, _ := strings.Cut(wantKindReason, "/")
			if got.Items[i].Kind != wantKind || got.Items[i].Reason != wantReason {
				t.Errorf("%s 第 %d 条 = %s/%s，想 %s", note, i,
					got.Items[i].Kind, got.Items[i].Reason, wantKindReason)
			}
		}
	}

	w.s.Do(t, w.zhaoA, http.MethodPost, "/api/v1/tickets/"+number+"/close",
		map[string]string{"reason": "done"}, nil)
	assertReasons("第三方关闭：创建者", w.webWang, "fyi/closed")
	assertReasons("第三方关闭：指派人", w.webLi, "fyi/closed", "turn/assigned")
	assertReasons("操作者本人", w.webZhao)

	w.s.Do(t, w.webLi, http.MethodPost, "/api/v1/tickets/"+number+"/reopen", nil, nil)
	assertReasons("指派人重开：创建者", w.webWang, "fyi/reopened", "fyi/closed")
	assertReasons("操作者本人", w.webLi, "fyi/closed", "turn/assigned")

	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+number+"/assign",
		map[string]string{"assignee": "赵工"}, nil)
	assertReasons("改派：原指派人", w.webLi, "fyi/unassigned", "fyi/closed", "turn/assigned")
	assertReasons("改派：新指派人", w.webZhao, "turn/assigned")
	assertReasons("改派操作者", w.webWang, "fyi/reopened", "fyi/closed")

	res := w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+number+"/assign",
		map[string]string{"assignee": ""}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("取消指派状态码 = %d, 想 200", res.StatusCode)
	}
	assertReasons("取消指派", w.webLi, "fyi/unassigned", "fyi/closed", "turn/assigned")
	assertReasons("取消指派", w.webZhao, "fyi/unassigned", "turn/assigned")
}

func Test知会通知覆盖说明修订和第三方回复(t *testing.T) {
	w := newHandoffWorld(t)
	number := w.createHandoffTicket(t, w.webWang, "整理对照表", "李工").Number

	link := w.s.CreateGuestLink(t, w.webWang, number, map[string]any{
		"note": "检验科-张三", "can_edit_brief": true,
	})
	w.s.Do(t, w.s.GuestClient(link.Key(t)), http.MethodPatch, "/api/v1/tickets/"+number,
		map[string]any{"brief_md": "补充试剂批号。", "base_revision": 1}, nil)
	for _, c := range []*http.Client{w.webWang, w.webLi} {
		got := notificationsOf(t, w, c, "")
		if (c == w.webWang && len(got.Items) != 1) || (c == w.webLi && len(got.Items) != 2) {
			t.Fatalf("说明修订通知 = %+v，想 fyi/brief_revised", got.Items)
		}
		if got.Items[0].Kind != "fyi" || got.Items[0].Reason != "brief_revised" {
			t.Errorf("说明修订通知 = %s/%s，想 fyi/brief_revised", got.Items[0].Kind, got.Items[0].Reason)
		}
		if got.Items[0].Summary != "第 2 版" {
			t.Errorf("说明修订 summary = %q, 想 第 2 版", got.Items[0].Summary)
		}
	}

	w.s.Do(t, w.zhaoA, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "我这边有类似经验。"}, nil)
	for _, c := range []*http.Client{w.webWang, w.webLi} {
		got := notificationsOf(t, w, c, "")
		if (c == w.webWang && len(got.Items) != 2) || (c == w.webLi && len(got.Items) != 3) ||
			got.Items[0].Reason != "third_party_reply" {
			t.Fatalf("第三方回复通知 = %+v，想 third_party_reply", got.Items)
		}
	}
	got := notificationsOf(t, w, w.webZhao, "")
	if len(got.Items) != 0 {
		t.Fatalf("第三方回复者本人通知 = %+v", got.Items)
	}
}

func Test未指派工单通知初筛人并进入收件箱(t *testing.T) {
	w := newHandoffWorld(t)
	mustSetTriager(t, w.s, w.webWang, memberID(t, w.s, w.webWang, "李工"), true)
	mustSetTriager(t, w.s, w.webWang, memberID(t, w.s, w.webWang, "赵工"), true)
	number := w.createHandoffTicket(t, w.webWang, "未指派的反馈", "").Number

	assertDepartment := func(note string, c *http.Client, want int) {
		t.Helper()
		got := notificationsOf(t, w, c, "")
		count := 0
		for _, n := range got.Items {
			if n.Ticket.Number != number {
				continue
			}
			if n.Kind != "turn" || n.Reason != "department" {
				t.Errorf("%s：通知 = %s/%s，想 turn/department", note, n.Kind, n.Reason)
			}
			if n.Ticket.Number != number {
				t.Errorf("%s：通知工单 = %s, 想 %s", note, n.Ticket.Number, number)
			}
			count++
		}
		if count != want {
			t.Errorf("%s：department 通知数 = %d, 想 %d", note, count, want)
		}
		inbox := triagerInboxOf(t, w.s, c)
		if has := inbox.reasonByNumber()[number]; has != "待初筛" {
			t.Errorf("%s：收件箱原因 = %q, 想 待初筛", note, has)
		}
	}
	assertDepartment("新反馈", w.webLi, 1)
	assertDepartment("新反馈", w.webZhao, 1)

	createIntake(t, w.s, w.webWang, map[string]any{"slug": "fyi-bug", "name": "Bug 反馈", "template": "bug-report"})
	intakeTicket := submitToIntake(t, w.s, "fyi-bug", map[string]any{
		"title": "入口提交的新反馈", "fields": bugFields(), "author_name": "赵六",
	}).Ticket.Number
	for _, c := range []*http.Client{w.webLi, w.webZhao} {
		got := notificationsOf(t, w, c, "")
		if len(got.Items) != 2 || got.Items[0].Ticket.Number != intakeTicket ||
			got.Items[0].Kind != "turn" || got.Items[0].Reason != "department" {
			t.Fatalf("入口新反馈通知 = %+v，想 turn/department", got.Items)
		}
		if has := triagerInboxOf(t, w.s, c).reasonByNumber()[intakeTicket]; has != "待初筛" {
			t.Fatalf("入口新反馈收件箱原因 = %q, 想 待初筛", has)
		}
	}

	link := w.s.CreateGuestLink(t, w.webWang, number, map[string]any{"note": "检验科-张三"})
	w.s.Do(t, w.s.GuestClient(link.Key(t)), http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "补充材料。"}, nil)
	assertDepartment("访客回复未指派工单", w.webLi, 2)
	assertDepartment("访客回复未指派工单", w.webZhao, 2)
	if got := notificationsOf(t, w, w.webWang, ""); len(got.Items) != 0 {
		t.Fatalf("创建者不应收到本部门轮次通知：%+v", got.Items)
	}
}

func Test标签和访客链接变更不生成通知(t *testing.T) {
	w := newHandoffWorld(t)
	number := w.createHandoffTicket(t, w.webWang, "整理对照表", "李工").Number

	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+number+"/labels",
		map[string]any{"add": []string{"待初筛"}}, nil)
	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+number+"/labels",
		map[string]any{"remove": []string{"待初筛"}}, nil)
	link := w.s.CreateGuestLink(t, w.webWang, number, map[string]any{"note": "检验科-张三"})
	w.s.Do(t, w.webWang, http.MethodDelete,
		"/api/v1/tickets/"+number+"/guest-links/"+itoa(link.ID), nil, nil)

	for _, c := range []*http.Client{w.webWang, w.webLi} {
		got := notificationsOf(t, w, c, "")
		if len(got.Items) != (map[*http.Client]int{w.webWang: 0, w.webLi: 1}[c]) {
			t.Fatalf("标签和链接变更不应生成通知：%+v", got.Items)
		}
	}
}
