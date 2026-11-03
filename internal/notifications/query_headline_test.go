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

func Test通知列表读取一句话(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatalf("打开数据库失败：%v", err)
	}
	defer st.Close()
	now := time.Date(2026, 11, 3, 16, 30, 0, 0, clock.Beijing)
	if _, err := st.DB.Exec(`INSERT INTO members
		(id, username, name, password_hash, is_admin, created_at)
		VALUES (1, 'chen', '陈明', 'x', 0, ?), (2, 'zhang', '张三', 'x', 0, ?)`,
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("插入成员失败：%v", err)
	}
	if _, err := st.DB.Exec(`INSERT INTO tickets
		(seq, number, kind, title, status, brief_md, brief_revision,
		 creator_member_id, assignee_id, created_at, last_change)
		VALUES (1, 'T-0412', 'task', '生产线通讯中断', 'open', '', 1, 2, 1, ?, 2)`,
		now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("插入工单失败：%v", err)
	}
	if _, err := st.DB.Exec(`INSERT INTO notifications
		(member_id, ticket_seq, ticket_number, ticket_title, kind, reason, text,
		 trigger_seq, trigger_type, actor_kind, actor_name, actor_member_id,
		 actor_agent_id, summary, headline, url, created_at, read_at)
		VALUES (1, 1, 'T-0412', '生产线通讯中断', 'turn', 'assigned',
		 '张三 把工单「生产线通讯中断」指派给你', 2, 'assigned', 'member', '张三', 2,
		 NULL, NULL, '张三 派给你', '/t/T-0412', ?, NULL)`,
		now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("插入通知失败：%v", err)
	}

	got, err := notifications.NewManager(st.DB, clock.NewFake(now)).List(context.Background(),
		notifications.ListQuery{MemberID: 1, IncludeSystem: true})
	if err != nil {
		t.Fatalf("读取通知失败：%v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("通知数 = %d，想 1", len(got.Items))
	}
	if got.Items[0].Headline != "张三 派给你" {
		t.Fatalf("一句话 = %q，想 %q", got.Items[0].Headline, "张三 派给你")
	}
}

func Test通知写入保存一句话(t *testing.T) {
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
		VALUES (1, 'T-0412', 'task', '生产线通讯中断', 'open', '', 1, 2, 1, ?, 2)`,
		created); err != nil {
		t.Fatalf("插入工单失败：%v", err)
	}
	after := tickets.Full{Ticket: tickets.Ticket{
		Seq: 1, Number: "T-0412", Kind: "task", Status: "open", Title: "生产线通讯中断",
		Creator:    tickets.Author{MemberID: 2, MemberName: "张三"},
		AssigneeID: 1, AssigneeName: "陈明",
	}}
	tx, err := st.DB.Begin()
	if err != nil {
		t.Fatalf("开启事务失败：%v", err)
	}
	if _, err := notifications.NewManager(st.DB, clock.NewFake(now)).NotifyTx(context.Background(), tx,
		tickets.NotificationInput{
			After: after, Actor: tickets.Author{MemberID: 2, MemberName: "张三"},
			Event: tickets.Event{Seq: 2, Type: "assigned"}, Now: now,
		}); err != nil {
		tx.Rollback()
		t.Fatalf("写入通知失败：%v", err)
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
	if got.Items[0].Headline != "张三 派给你" {
		t.Fatalf("一句话 = %q，想 %q", got.Items[0].Headline, "张三 派给你")
	}
}
