package notifications_test

import (
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/notifications"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

func Test通知规则为新指派生成一句话(t *testing.T) {
	now := time.Date(2026, 11, 3, 16, 30, 0, 0, time.Local)
	after := tickets.Full{Ticket: tickets.Ticket{
		Seq: 412, Number: "T-0412", Kind: "task", Status: "open",
		Title: "生产线通讯中断", Creator: tickets.Author{MemberID: 1, MemberName: "张三"},
		AssigneeID: 7, AssigneeName: "陈明",
	}}
	in := tickets.NotificationInput{
		After: after,
		Actor: tickets.Author{MemberID: 2, MemberName: "赵六"},
		Event: tickets.Event{Seq: 2, Type: "assigned"},
		Now:   now,
	}

	got := notifications.Rules{}.Decide(in)

	if len(got) != 1 {
		t.Fatalf("通知数 = %d，想 1：%+v", len(got), got)
	}
	if got[0].Headline != "赵六 派给你" {
		t.Fatalf("一句话 = %q，想 %q", got[0].Headline, "赵六 派给你")
	}
}

func Test通知规则为改派生成一句话(t *testing.T) {
	now := time.Date(2026, 11, 3, 16, 30, 0, 0, time.Local)
	after := tickets.Full{Ticket: tickets.Ticket{
		Seq: 380, Number: "T-0380", Kind: "task", Status: "open",
		Title: "测试环境数据库迁移", Creator: tickets.Author{MemberID: 1, MemberName: "张三"},
		AssigneeID: 7, AssigneeName: "陈明",
	}}
	in := tickets.NotificationInput{
		Before: tickets.Full{Ticket: tickets.Ticket{AssigneeID: 8, AssigneeName: "李四"}},
		After:  after,
		Actor:  tickets.Author{MemberID: 2, MemberName: "李四"},
		Event:  tickets.Event{Seq: 3, Type: "assigned"},
		Now:    now,
	}

	got := notifications.Rules{}.Decide(in)

	if len(got) != 2 {
		t.Fatalf("通知数 = %d，想 2（新指派人和原指派人）：%+v", len(got), got)
	}
	if got[0].Headline != "李四 改派给你" {
		t.Fatalf("一句话 = %q，想 %q", got[0].Headline, "李四 改派给你")
	}
}

func Test通知规则为交付生成一句话(t *testing.T) {
	now := time.Date(2026, 11, 3, 16, 30, 0, 0, time.Local)
	after := tickets.Full{Ticket: tickets.Ticket{
		Seq: 408, Number: "T-0408", Kind: "task", Status: "open",
		Title: "季度报表导出", Creator: tickets.Author{MemberID: 1, MemberName: "陈明"},
		AssigneeID: 7, AssigneeName: "李四",
	}, Timeline: []tickets.Event{{
		Seq: 2, Type: "reply", Actor: tickets.Author{MemberID: 7, MemberName: "李四"}, Deliver: true,
	}}}
	in := tickets.NotificationInput{
		After: after,
		Actor: tickets.Author{MemberID: 7, MemberName: "李四"},
		Event: tickets.Event{Seq: 2, Type: "reply", Deliver: true},
		Now:   now,
	}

	got := notifications.Rules{}.Decide(in)

	if len(got) != 1 || got[0].MemberID != 1 {
		t.Fatalf("通知 = %+v，想发起人收到 1 条交付通知", got)
	}
	if got[0].Headline != "李四 交付了，等你验收" {
		t.Fatalf("一句话 = %q，想 %q", got[0].Headline, "李四 交付了，等你验收")
	}
}

func Test通知规则为访客回复生成一句话(t *testing.T) {
	now := time.Date(2026, 11, 3, 16, 30, 0, 0, time.Local)
	after := tickets.Full{Ticket: tickets.Ticket{
		Seq: 405, Number: "T-0405", Kind: "report", Status: "open",
		Title: "设备开机界面乱码", Creator: tickets.Author{MemberID: 1, MemberName: "陈明"},
		AssigneeID: 7, AssigneeName: "陈明",
	}, Timeline: []tickets.Event{{
		Seq: 2, Type: "reply", Actor: tickets.Author{AuthorName: "王五"},
	}}}
	in := tickets.NotificationInput{
		After: after,
		Actor: tickets.Author{AuthorName: "王五"},
		Event: tickets.Event{Seq: 2, Type: "reply", BodyMD: "英文系统下复现。"},
		Now:   now,
	}

	got := notifications.Rules{}.Decide(in)

	if len(got) != 1 || got[0].MemberID != 7 {
		t.Fatalf("通知 = %+v，想指派人收到 1 条访客回复通知", got)
	}
	if got[0].Headline != "访客 王五 回复了" {
		t.Fatalf("一句话 = %q，想 %q", got[0].Headline, "访客 王五 回复了")
	}
}

func Test通知规则为成员自己的Agent回复生成一句话(t *testing.T) {
	now := time.Date(2026, 11, 3, 16, 30, 0, 0, time.Local)
	after := tickets.Full{Ticket: tickets.Ticket{
		Seq: 398, Number: "T-0398", Kind: "task", Status: "open",
		Title: "整理我自己的周计划模板", Creator: tickets.Author{MemberID: 7, MemberName: "陈明"},
		AssigneeID: 7, AssigneeName: "陈明",
	}, Timeline: []tickets.Event{{
		Seq: 2, Type: "reply",
		Actor: tickets.Author{MemberID: 7, MemberName: "陈明", AgentID: 9, AgentName: "小舟"},
	}}}
	in := tickets.NotificationInput{
		After: after,
		Actor: tickets.Author{MemberID: 7, MemberName: "陈明", AgentID: 9, AgentName: "小舟"},
		Event: tickets.Event{Seq: 2, Type: "reply", BodyMD: "模板已经整理好。"},
		Now:   now,
	}

	got := notifications.Rules{}.Decide(in)

	if len(got) != 1 || got[0].MemberID != 7 {
		t.Fatalf("通知 = %+v，想成员收到自己的 Agent 回复通知", got)
	}
	if got[0].Headline != "你的 Agent 回复了" {
		t.Fatalf("一句话 = %q，想 %q", got[0].Headline, "你的 Agent 回复了")
	}
}

func Test通知规则为待初筛生成一句话(t *testing.T) {
	now := time.Date(2026, 11, 3, 16, 30, 0, 0, time.Local)
	after := tickets.Full{Ticket: tickets.Ticket{
		Seq: 396, Number: "T-0396", Kind: "task", Status: "open",
		Title: "新员工账号批量开通", Creator: tickets.Author{MemberID: 6, MemberName: "赵六"},
	}}
	in := tickets.NotificationInput{
		After: after,
		Actor: tickets.Author{MemberID: 6, MemberName: "赵六"},
		Event: tickets.Event{Seq: 1, Type: "created"},
		Now:   now, Triagers: []int64{7},
	}

	got := notifications.Rules{}.Decide(in)

	if len(got) != 1 || got[0].MemberID != 7 {
		t.Fatalf("通知 = %+v，想初筛人收到 1 条通知", got)
	}
	if got[0].Headline != "赵六 新建，等你初筛" {
		t.Fatalf("一句话 = %q，想 %q", got[0].Headline, "赵六 新建，等你初筛")
	}
}

func Test通知规则为关闭工单生成一句话(t *testing.T) {
	now := time.Date(2026, 11, 3, 16, 30, 0, 0, time.Local)
	after := tickets.Full{Ticket: tickets.Ticket{
		Seq: 410, Number: "T-0410", Kind: "task", Status: "closed",
		Title: "采购申请：示波器两台", Creator: tickets.Author{MemberID: 1, MemberName: "陈明"},
		AssigneeID: 7, AssigneeName: "李四",
	}}
	in := tickets.NotificationInput{
		After: after,
		Actor: tickets.Author{MemberID: 2, MemberName: "张三"},
		Event: tickets.Event{Seq: 4, Type: "closed"},
		Now:   now,
	}

	got := notifications.Rules{}.Decide(in)

	if len(got) != 2 {
		t.Fatalf("通知数 = %d，想创建者和指派人各 1 条：%+v", len(got), got)
	}
	for _, d := range got {
		if d.Headline != "张三 关闭了工单" {
			t.Fatalf("成员 %d 的一句话 = %q，想 %q", d.MemberID, d.Headline, "张三 关闭了工单")
		}
	}
}
