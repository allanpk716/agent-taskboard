package server_test

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

// #128：自派自的任务没有访客时，Agent 交付回到本人验收，并按现有收件箱类
// 规则通知本人。
func Test自派自任务Agent交付后本人验收(t *testing.T) {
	w := newHandoffWorld(t)
	number := w.createHandoffTicket(t, w.webWang, "整理自己的工具清单", "王工").Number

	var delivered replyBody
	res := w.s.Do(t, w.wangA, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "清单已完成。", "deliver": true}, &delivered)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("Agent 交付状态码 = %d, 想 201", res.StatusCode)
	}
	assertWaitingOn(t, delivered.WaitingOn, waitingOnBody{
		Party: "assignee", Member: strp("王工"), Text: "王工（指派人）", Reason: "已交付，待你验收",
	}, "Agent 交付后")
	assertWaitingOn(t, w.waitOf(t, w.webWang, number), waitingOnBody{
		Party: "assignee", Member: strp("王工"), Text: "王工（指派人）", Reason: "已交付，待你验收",
	}, "读取工单")

	inbox := w.inboxOf(t, w.webWang)
	if got := inbox.reasonByNumber()[number]; got != "已交付，待你验收" {
		t.Fatalf("王工信箱原因 = %q, 想 已交付，待你验收", got)
	}
	for _, it := range inbox.Items {
		if it.Number == number && it.Role != "assignee" {
			t.Errorf("自派自收件箱角色 = %s, 想 assignee", it.Role)
		}
	}

	got := notificationsOf(t, w, w.webWang, "")
	if len(got.Items) != 1 {
		t.Fatalf("王工通知数 = %d, 想 1", len(got.Items))
	}
	n := got.Items[0]
	if n.Ticket.Number != number || n.Kind != "turn" || n.Reason != "delivered" {
		t.Errorf("交付通知 = %+v, 想 %s/turn/delivered", n, number)
	}
	if n.Text != "王工 的 Agent · 笔记本 交付了「整理自己的工具清单」" {
		t.Errorf("交付通知文案 = %q", n.Text)
	}
}

// 没有访客链接时，Agent 的普通回复让工单留在本人收件箱；已轮到本人时再回复
// 仍按收件箱类规则通知本人，原因码固定为 agent_replied。
func Test自派自任务Agent普通回复轮到本人并通知(t *testing.T) {
	w := newHandoffWorld(t)
	number := w.createHandoffTicket(t, w.webWang, "更新自用脚本", "王工").Number

	var replied replyBody
	w.s.Do(t, w.wangA, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "脚本跑到一半，明天继续。"}, &replied)
	assertWaitingOn(t, replied.WaitingOn, waitingOnBody{
		Party: "assignee", Member: strp("王工"), Text: "王工（指派人）", Reason: "你的 Agent 回复了",
	}, "Agent 普通回复后")
	if got := w.inboxOf(t, w.webWang).reasonByNumber()[number]; got != "你的 Agent 回复了" {
		t.Fatalf("王工信箱原因 = %q, 想 你的 Agent 回复了", got)
	}

	w.s.Do(t, w.wangA, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "又遇到一个边界，先记录。"}, nil)
	got := notificationsOf(t, w, w.webWang, "")
	if len(got.Items) != 2 {
		t.Fatalf("两次 Agent 回复后的通知数 = %d, 想 2", len(got.Items))
	}
	for _, n := range got.Items {
		if n.Ticket.Number != number || n.Kind != "turn" || n.Reason != "agent_replied" {
			t.Errorf("Agent 回复通知 = %+v, 想 %s/turn/agent_replied", n, number)
		}
	}
	if got.Items[0].Text != "王工 的 Agent · 笔记本 回复了「更新自用脚本」" {
		t.Errorf("Agent 回复通知文案 = %q", got.Items[0].Text)
	}
}

// 本人交付前写备注：仍在收件箱、原因沿用上一条相关回复，只更新起始时间，
// 且本人操作不给自己补通知；交付后退回则原因改为发起人退回修改。
func Test自派自任务本人回复保留待办并在交付后退回(t *testing.T) {
	w := newHandoffWorld(t)
	number := w.createHandoffTicket(t, w.webWang, "维护个人清单", "王工").Number

	w.s.Do(t, w.wangA, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "今天先做第一部分。"}, nil)
	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "我稍后补充范围。"}, nil)
	assertWaitingOn(t, w.waitOf(t, w.webWang, number), waitingOnBody{
		Party: "assignee", Member: strp("王工"), Text: "王工（指派人）", Reason: "你的 Agent 回复了",
	}, "交付前本人备注")
	inbox := w.inboxOf(t, w.webWang)
	for _, it := range inbox.Items {
		if it.Number == number && (it.SinceSeq == nil || *it.SinceSeq != 4) {
			t.Errorf("本人备注后的 since_seq = %v, 想 4", it.SinceSeq)
		}
	}
	if got := notificationsOf(t, w, w.webWang, ""); len(got.Items) != 1 {
		t.Fatalf("本人备注后的通知数 = %d, 想 仍为 1", len(got.Items))
	}

	w.s.Do(t, w.wangA, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "第一版完成。", "deliver": true}, nil)
	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "还缺少异常处理，请补上。"}, nil)
	assertWaitingOn(t, w.waitOf(t, w.webWang, number), waitingOnBody{
		Party: "assignee", Member: strp("王工"), Text: "王工（指派人）", Reason: "发起人退回修改",
	}, "本人退回交付")
	if got := w.inboxOf(t, w.webWang).reasonByNumber()[number]; got != "发起人退回修改" {
		t.Fatalf("退回后的收件箱原因 = %q, 想 发起人退回修改", got)
	}
}

// 有仍有效的访客链接（任何种类）时：交付仍请本人验收，普通回复才轮到访客。
func Test自派自任务有访客链接时交付与普通回复(t *testing.T) {
	w := newHandoffWorld(t)
	number := w.createHandoffTicket(t, w.webWang, "请外部确认格式", "王工").Number
	w.s.CreateGuestLink(t, w.webWang, number, map[string]any{"note": "检验科-张三"})

	var delivered replyBody
	w.s.Do(t, w.wangA, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "格式已整理。", "deliver": true}, &delivered)
	assertWaitingOn(t, delivered.WaitingOn, waitingOnBody{
		Party: "assignee", Member: strp("王工"), Text: "王工（指派人）", Reason: "已交付，待你验收",
	}, "有访客链接时交付")

	var replied replyBody
	w.s.Do(t, w.wangA, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "请张三确认字段口径。"}, &replied)
	assertWaitingOn(t, replied.WaitingOn, waitingOnBody{Party: "guest", Text: "访客"}, "有访客链接时普通回复")
}

// 链接过期与撤销都只影响读取时的轮次推导，不产生事件或通知；失效后本人
// 一方回复不再轮到访客。
func Test自派自任务访客链接过期或撤销后回到本人(t *testing.T) {
	w := newHandoffWorld(t)
	expired := w.createHandoffTicket(t, w.webWang, "短期确认一", "王工").Number
	w.s.CreateGuestLink(t, w.webWang, expired, map[string]any{"note": "短期一", "valid_days": 7})
	w.s.Clock().Advance(7 * 24 * time.Hour)

	var expiredReply replyBody
	w.s.Do(t, w.wangA, http.MethodPost, "/api/v1/tickets/"+expired+"/replies",
		map[string]string{"body_md": "链接过期后的备注。"}, &expiredReply)
	assertWaitingOn(t, expiredReply.WaitingOn, waitingOnBody{
		Party: "assignee", Member: strp("王工"), Text: "王工（指派人）", Reason: "你的 Agent 回复了",
	}, "链接过期后")

	w = newHandoffWorld(t)
	revoked := w.createHandoffTicket(t, w.webWang, "短期确认二", "王工").Number
	link := w.s.CreateGuestLink(t, w.webWang, revoked, map[string]any{"note": "短期二"})
	res := w.s.Do(t, w.webWang, http.MethodDelete,
		"/api/v1/tickets/"+revoked+"/guest-links/"+strconv.FormatInt(link.ID, 10), nil, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("撤销访客链接状态码 = %d, 想 204", res.StatusCode)
	}
	var revokedReply replyBody
	w.s.Do(t, w.wangA, http.MethodPost, "/api/v1/tickets/"+revoked+"/replies",
		map[string]string{"body_md": "链接撤销后的备注。"}, &revokedReply)
	assertWaitingOn(t, revokedReply.WaitingOn, waitingOnBody{
		Party: "assignee", Member: strp("王工"), Text: "王工（指派人）", Reason: "你的 Agent 回复了",
	}, "链接撤销后")
}

// 第三位成员的回复不进入自派自任务的相关回复集，轮次和原因都保持不变。
func Test自派自任务第三位成员回复不改轮次(t *testing.T) {
	w := newHandoffWorld(t)
	number := w.createHandoffTicket(t, w.webWang, "记录实验参数", "王工").Number
	w.s.Do(t, w.wangA, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "我先整理一版。"}, nil)
	before := w.waitOf(t, w.webWang, number)

	w.s.Do(t, w.zhaoA, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "以前那台仪器有类似参数。"}, nil)
	after := w.waitOf(t, w.webWang, number)
	if after.Party != before.Party || after.Reason != before.Reason || after.Text != before.Text {
		t.Fatalf("第三位回复后轮次从 %+v 变成 %+v", before, after)
	}
}

// 从别人改派给自己后按自派自规则重算：原指派人的旧回复和新回复都不参与推导。
func Test改派给自己后原指派人回复不参与自派自推导(t *testing.T) {
	w := newHandoffWorld(t)
	number := w.createHandoffTicket(t, w.webWang, "接管整理工作", "李工").Number
	w.s.Do(t, w.liA, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "我查了一半。"}, nil)

	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+number+"/assign",
		map[string]string{"assignee": "王工"}, nil)
	w.s.Do(t, w.liA, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "已不是我的任务，补充一点背景。"}, nil)
	assertWaitingOn(t, w.waitOf(t, w.webWang, number), waitingOnBody{
		Party: "assignee", Member: strp("王工"), Text: "王工（指派人）", Reason: "新指派给你",
	}, "改派给自己后")
}
