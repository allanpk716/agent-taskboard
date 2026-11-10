package tickets

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/labels"
	"github.com/allanpk716/agent-taskboard/internal/store"
	"github.com/allanpk716/agent-taskboard/internal/templates"
)

func Test访客级重置只接受网页成员凭证(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, query := range []string{
		`INSERT INTO members (id, username, name, password_hash, created_at)
		 VALUES (1, 'chen', '陈工', 'x', '2026-11-01T09:00:00+08:00')`,
		`INSERT INTO agent_tokens (id, member_id, name, token_hash, created_at)
		 VALUES (1, 1, '笔记本', 'x', '2026-11-01T09:01:00+08:00')`,
		`INSERT INTO tickets (seq, number, kind, title, status, brief_md, brief_revision,
			 creator_member_id, created_at, last_change)
		 VALUES (1, '20261101-001', 'task', '重置边界', 'open', '', 1, 1,
			 '2026-11-01T09:02:00+08:00', 1)`,
		`INSERT INTO guests (id, name, created_by_member_id, created_by_agent_id, created_at)
		 VALUES (1, '检验科-张三', 1, 1, '2026-11-01T09:03:00+08:00')`,
		`INSERT INTO guest_credential_holders
			 (id, owner_type, owner_id, claimed_at, claim_name, access_code_hash,
				 access_code_failures, created_at)
		 VALUES (1, 'guest', 1, '2026-11-01T10:00:00+08:00', '张三', 'code',
			 0, '2026-11-01T09:03:00+08:00')`,
		`INSERT INTO guest_links (id, ticket_seq, key_hash, note, expires_at,
			 credential_holder_id, created_by_member_id, created_at, guest_id)
		 VALUES (1, 1, 'key', '补全说明', '2999-01-01T00:00:00+08:00', 1, 1,
			 '2026-11-01T09:03:00+08:00', 1)`,
	} {
		if _, err := s.DB.Exec(query); err != nil {
			t.Fatalf("造测试数据失败：%v\n%s", err, query)
		}
	}
	clk := clock.NewFake(time.Date(2026, 11, 2, 9, 0, 0, 0, clock.Beijing))
	manager := NewManager(s.DB, clk, labels.NewManager(s.DB),
		templates.NewManager(s.DB, clk), nil)
	viewer := identity.Viewer{Kind: identity.ViewerMember,
		Member: identity.Member{ID: 1, Name: "陈工"}, Agent: &identity.Agent{ID: 1, Name: "笔记本"}}

	if _, err := manager.ResetGuestAccessCodeForGuest(context.Background(), viewer, 1); !errors.Is(err, ErrGuestManagementNotAllowed) {
		t.Fatalf("成员 Agent 重置访问码错误 = %v, 想 %v", err, ErrGuestManagementNotAllowed)
	}
	if _, err := manager.ResetGuestHomeKeyForViewer(context.Background(), viewer, 1); !errors.Is(err, ErrGuestManagementNotAllowed) {
		t.Fatalf("成员 Agent 重置主页错误 = %v, 想 %v", err, ErrGuestManagementNotAllowed)
	}
}
