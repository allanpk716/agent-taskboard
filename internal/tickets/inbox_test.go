package tickets_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/labels"
	"github.com/allanpk716/agent-taskboard/internal/store"
	"github.com/allanpk716/agent-taskboard/internal/templates"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

type inboxFixture struct {
	t       *testing.T
	manager *tickets.Manager
}

func newInboxFixture(t *testing.T, seed ...string) inboxFixture {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatalf("打开数据库失败：%v", err)
	}
	t.Cleanup(func() { s.Close() })
	for _, query := range seed {
		if _, err := s.DB.Exec(query); err != nil {
			t.Fatalf("造测试数据失败：%v\n%s", err, query)
		}
	}
	clk := clock.NewFake(time.Date(2026, 11, 3, 16, 30, 0, 0, clock.Beijing))
	return inboxFixture{t: t, manager: tickets.NewManager(
		s.DB, clk, labels.NewManager(s.DB), templates.NewManager(s.DB, clk), nil)}
}

func (f inboxFixture) inbox(viewerID int64, name string) []tickets.InboxItem {
	f.t.Helper()
	items, err := f.manager.Inbox(context.Background(), identity.Viewer{
		Kind: identity.ViewerMember, Member: identity.Member{ID: viewerID, Name: name},
	})
	if err != nil {
		f.t.Fatalf("读取收件箱失败：%v", err)
	}
	return items
}

func (f inboxFixture) item(number string, items []tickets.InboxItem) tickets.InboxItem {
	f.t.Helper()
	for _, it := range items {
		if it.Number == number {
			return it
		}
	}
	f.t.Fatalf("收件箱里没有 %s：%+v", number, items)
	return tickets.InboxItem{}
}

const inboxMembers = `INSERT INTO members (id, username, name, password_hash, is_admin, triager, created_at) VALUES
	(1, 'zhang', '张三', 'x', 0, 0, '2026-11-01T09:00:00+08:00'),
	(2, 'li', '李四', 'x', 0, 0, '2026-11-01T09:00:00+08:00'),
	(3, 'chen', '陈明', 'x', 0, 0, '2026-11-01T09:00:00'),
	(4, 'zhao', '赵六', 'x', 0, 0, '2026-11-01T09:00:00+08:00'),
	(5, 'wang2', '王二', 'x', 0, 0, '2026-11-01T09:00:00+08:00')`

func Test收件箱触发者区分首次指派和改派(t *testing.T) {
	f := newInboxFixture(t, inboxMembers, `
		INSERT INTO tickets (seq, number, kind, title, status, brief_md, brief_revision,
			creator_member_id, assignee_id, created_at, last_change) VALUES
			(1, '20261103-001', 'task', '直接指派给陈明', 'open', '', 1, 1, 3,
			 '2026-11-03T10:00:00+08:00', 2),
			(2, '20261103-002', 'task', '改派给陈明', 'open', '', 1, 1, 3,
			 '2026-11-03T10:30:00+08:00', 5)`,
		`INSERT INTO timeline_events (ticket_seq, seq, change_seq, type, actor_member_id,
			at, assign_from, assign_to) VALUES
			(1, 1, 1, 'created', 1, '2026-11-03T10:00:00+08:00', NULL, NULL),
			(1, 2, 2, 'assigned', 1, '2026-11-03T10:00:00+08:00', NULL, '陈明'),
			(2, 1, 3, 'created', 1, '2026-11-03T10:30:00+08:00', NULL, NULL),
			(2, 2, 4, 'assigned', 1, '2026-11-03T10:31:00+08:00', NULL, '李四'),
			(2, 3, 5, 'assigned', 2, '2026-11-03T10:32:00+08:00', '李四', '陈明')`)

	items := f.inbox(3, "陈明")
	first := f.item("20261103-001", items)
	if first.ReasonCode != tickets.WhyNew || first.Trigger.Name != "张三" {
		t.Fatalf("首次指派 = reason %s trigger %+v，想 new/张三", first.ReasonCode, first.Trigger)
	}
	if first.Trigger.Action != "assigned" {
		t.Fatalf("首次指派 action = %q，想 assigned", first.Trigger.Action)
	}
	reassigned := f.item("20261103-002", items)
	if reassigned.ReasonCode != tickets.WhyNew || reassigned.Trigger.Name != "李四" {
		t.Fatalf("改派 = reason %s trigger %+v，想 new/李四", reassigned.ReasonCode, reassigned.Trigger)
	}
	if reassigned.Trigger.Action != "reassigned" {
		t.Fatalf("改派 action = %q，想 reassigned", reassigned.Trigger.Action)
	}
}

func Test待初筛触发者固定为工单创建者(t *testing.T) {
	f := newInboxFixture(t, inboxMembers, `
		UPDATE members SET triager = 1 WHERE id = 3`,
		`INSERT INTO tickets (seq, number, kind, title, status, brief_md, brief_revision,
			creator_member_id, assignee_id, created_at, last_change) VALUES
			(1, '20261103-001', 'report', '等待初筛的反馈', 'open', '', 1, 4, NULL,
			 '2026-11-03T10:00:00+08:00', 2)`,
		`INSERT INTO timeline_events (ticket_seq, seq, change_seq, type, actor_member_id, at, body_md) VALUES
			(1, 1, 1, 'created', 4, '2026-11-03T10:00:00+08:00', NULL),
			(1, 2, 2, 'reply', 5, '2026-11-03T10:05:00+08:00', '我补充一句。')`)

	item := f.item("20261103-001", f.inbox(3, "陈明"))
	if item.ReasonCode != tickets.WhyPendingTriage {
		t.Fatalf("原因码 = %q，想 pending_triage", item.ReasonCode)
	}
	if item.Trigger.Type != "member" || item.Trigger.Name != "赵六" {
		t.Fatalf("待初筛触发者 = %+v，想创建者赵六", item.Trigger)
	}
}

func Test访客往来对象固定为最早链接的访客(t *testing.T) {
	f := newInboxFixture(t, inboxMembers, `
		INSERT INTO tickets (seq, number, kind, title, status, brief_md, brief_revision,
			creator_member_id, assignee_id, created_at, last_change) VALUES
			(1, '20261103-001', 'report', '客户反馈设备故障', 'open', '', 1, 3, 3,
			 '2026-11-03T10:00:00+08:00', 4)`,
		`INSERT INTO guest_links (id, ticket_seq, key_hash, note, expires_at, can_edit_brief,
			claimed_at, claim_name, created_by_member_id, created_at) VALUES
			(1, 1, 'first', '王五的链接', '2999-01-01T00:00:00+08:00', 1,
			 '2026-11-03T10:10:00+08:00', '王五', 3, '2026-11-03T10:05:00+08:00'),
			(2, 1, 'second', '赵六访客的链接', '2999-01-01T00:00:00+08:00', 1,
			 '2026-11-03T11:10:00+08:00', '赵六访客', 3, '2026-11-03T11:05:00+08:00')`,
		`INSERT INTO timeline_events (ticket_seq, seq, change_seq, type, actor_member_id,
			actor_link_id, actor_author_name, at, body_md) VALUES
			(1, 1, 1, 'created', 3, NULL, NULL, '2026-11-03T10:00:00+08:00', NULL),
			(1, 2, 2, 'guest_link_created', 3, NULL, NULL, '2026-11-03T10:05:00+08:00', NULL),
			(1, 3, 3, 'guest_link_created', 3, NULL, NULL, '2026-11-03T11:05:00+08:00', NULL),
			(1, 4, 4, 'reply', NULL, 2, '赵六访客', '2026-11-03T11:20:00+08:00', '这是我看到的故障。')`)

	item := f.item("20261103-001", f.inbox(3, "陈明"))
	if item.Counterpart.Type != "guest" {
		t.Fatalf("往来对象类型 = %q，想 guest", item.Counterpart.Type)
	}
	if item.Counterpart.Name != "王五" {
		t.Fatalf("往来对象 = %q，想最早链接的访客王五", item.Counterpart.Name)
	}
	if item.Trigger.Name != "赵六访客" {
		t.Fatalf("触发者 = %q，想最后回复的赵六访客", item.Trigger.Name)
	}
}
