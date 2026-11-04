package notifications_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/notifications"
	"github.com/allanpk716/agent-taskboard/internal/store"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

func Test重开通知的一句话使用重新开启措辞(t *testing.T) {
	now := time.Date(2026, 11, 3, 16, 30, 0, 0, clock.Beijing)
	in := tickets.NotificationInput{
		After: tickets.Full{Ticket: tickets.Ticket{
			Seq: 408, Number: "20261103-001", Kind: "task", Status: "open", Title: "季度报表导出",
			Creator:    tickets.Author{MemberID: 1, MemberName: "陈明"},
			AssigneeID: 2, AssigneeName: "张三",
		}},
		Actor: tickets.Author{MemberID: 2, MemberName: "张三"},
		Event: tickets.Event{Seq: 4, Type: "reopened"},
		Now:   now,
	}

	got := notifications.Rules{}.Decide(in)

	if len(got) != 1 || got[0].MemberID != 1 {
		t.Fatalf("通知 = %+v，想创建者收到 1 条重开通知", got)
	}
	for _, d := range got {
		if d.Headline != "张三 重新开启了工单" {
			t.Fatalf("成员 %d 的一句话 = %q，想 %q", d.MemberID, d.Headline, "张三 重新开启了工单")
		}
		if d.Text != "张三 重新开启了「季度报表导出」" {
			t.Fatalf("成员 %d 的正文 = %q，想保持重开正文", d.MemberID, d.Text)
		}
	}
}

func Test草稿通知保存短句一句话(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatalf("打开数据库失败：%v", err)
	}
	defer st.Close()
	now := time.Date(2026, 11, 3, 16, 30, 0, 0, clock.Beijing)
	created := now.Format(time.RFC3339Nano)
	if _, err := st.DB.Exec(`INSERT INTO members
		(id, username, name, password_hash, is_admin, created_at)
		VALUES (1, 'chen', '陈明', 'x', 0, ?), (2, 'zhang', '张三', 'x', 0, ?)`,
		created, created); err != nil {
		t.Fatalf("插入成员失败：%v", err)
	}
	if _, err := st.DB.Exec(`INSERT INTO tickets
		(seq, number, kind, title, status, brief_md, brief_revision,
		 creator_member_id, assignee_id, created_at, last_change)
		VALUES (1, '20261103-001', 'task', '季度报表导出', 'open', '', 1, 2, 1, ?, 2)`,
		created); err != nil {
		t.Fatalf("插入工单失败：%v", err)
	}
	tx, err := st.DB.Begin()
	if err != nil {
		t.Fatalf("开启事务失败：%v", err)
	}
	longText := "这份草稿正文很长，包含换行和细节，不适合放在弹窗标题里。\n第二行"
	if err := notifications.NewManager(st.DB, clock.NewFake(now)).NotifyDraftTx(context.Background(), tx,
		notifications.DraftNotificationInput{
			MemberID: 1, TicketSeq: 1, TicketNumber: "20261103-001", TicketTitle: "季度报表导出",
			Text: longText, Headline: "你的 Agent 写了回复草稿", BaseTimelineSeq: 2,
			AgentID: 9, AgentName: "小舟", URL: "/t/20261103-001#draft",
		}); err != nil {
		tx.Rollback()
		t.Fatalf("写入草稿通知失败：%v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("提交事务失败：%v", err)
	}

	got, err := notifications.NewManager(st.DB, clock.NewFake(now)).List(context.Background(),
		notifications.ListQuery{MemberID: 1, IncludeSystem: true})
	if err != nil {
		t.Fatalf("读取通知失败：%v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("通知数 = %d，想 1", len(got.Items))
	}
	if got.Items[0].Headline != "你的 Agent 写了回复草稿" {
		t.Fatalf("一句话 = %q，想 %q", got.Items[0].Headline, "你的 Agent 写了回复草稿")
	}
	if got.Items[0].Text != longText {
		t.Fatalf("正文被改掉了：%q", got.Items[0].Text)
	}
}
