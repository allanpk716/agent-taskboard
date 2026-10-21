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

type viewerAccessFixture struct {
	manager  *Manager
	creator  identity.Viewer
	member   identity.Viewer
	outsider identity.Viewer
}

func newViewerAccessFixture(t *testing.T) viewerAccessFixture {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	seed := []string{
		`INSERT INTO members (id, username, name, password_hash, is_admin, created_at) VALUES
			(1, 'creator', '创建者', 'x', 0, '2026-10-20T09:00:00+08:00'),
			(2, 'member', '协作者', 'x', 0, '2026-10-20T09:00:00+08:00'),
			(3, 'outsider', '局外成员', 'x', 0, '2026-10-20T09:00:00+08:00'),
			(4, 'helper', '可退出成员', 'x', 0, '2026-10-20T09:00:00+08:00')`,
		`INSERT INTO tickets (seq, number, kind, title, status, brief_md, brief_revision,
			creator_member_id, assignee_id, created_at, last_change) VALUES
			(1, '20261020-001', 'task', '开启工单', 'open', '说明', 1, 1, 2,
			 '2026-10-20T09:00:00+08:00', 2),
			(2, '20261020-002', 'task', '关闭工单', 'closed', '说明', 1, 1, 2,
			 '2026-10-20T09:01:00+08:00', 4)`,
		`INSERT INTO ticket_collaborators (ticket_seq, member_id, added_at) VALUES
			(1, 2, '2026-10-20T09:02:00+08:00'),
			(1, 4, '2026-10-20T09:02:00+08:00'),
			(2, 2, '2026-10-20T09:02:00+08:00')`,
		`INSERT INTO guest_links (id, ticket_seq, key_hash, note, expires_at, can_edit_brief,
			claimed_at, claim_name, created_by_member_id, created_at) VALUES
			(1, 1, 'one', '开启链接', '2999-01-01T00:00:00+08:00', 1,
			 '2026-10-20T10:00:00+08:00', '张三', 1, '2026-10-20T09:03:00+08:00'),
			(2, 2, 'two', '关闭链接', '2999-01-01T00:00:00+08:00', 1,
			 '2026-10-20T10:00:00+08:00', '李四', 1, '2026-10-20T09:03:00+08:00')`,
		`INSERT INTO timeline_events (ticket_seq, seq, change_seq, type, actor_member_id, at) VALUES
			(1, 1, 1, 'created', 1, '2026-10-20T09:00:00+08:00'),
			(1, 2, 2, 'assigned', 1, '2026-10-20T09:00:00+08:00'),
			(2, 1, 3, 'created', 1, '2026-10-20T09:01:00+08:00'),
			(2, 2, 4, 'closed', 2, '2026-10-20T09:01:30+08:00')`,
	}
	for _, query := range seed {
		if _, err := s.DB.Exec(query); err != nil {
			t.Fatalf("造测试数据失败：%v\n%s", err, query)
		}
	}
	clk := clock.NewFake(time.Date(2026, 10, 21, 9, 0, 0, 0, clock.Beijing))
	m := NewManager(s.DB, clk, labels.NewManager(s.DB), templates.NewManager(s.DB, clk), nil)
	return viewerAccessFixture{
		manager:  m,
		creator:  memberViewer(1, "创建者"),
		member:   memberViewer(2, "协作者"),
		outsider: memberViewer(3, "局外成员"),
	}
}

func memberViewer(id int64, name string) identity.Viewer {
	return identity.Viewer{Kind: identity.ViewerMember, Member: identity.Member{ID: id, Name: name}}
}

func guestViewer(seq int64) identity.Viewer {
	return identity.Viewer{Kind: identity.ViewerGuest, GuestLink: &identity.GuestLink{
		ID: seq, TicketSeq: seq, Note: "链接", ExpiresAt: time.Date(2999, 1, 1, 0, 0, 0, 0, clock.Beijing),
		CanEditBrief: true, ClaimName: "访客",
	}}
}

func assertAccessResult(t *testing.T, err error, want string, seq int64, viewer identity.Viewer) {
	t.Helper()
	var invisible *TicketInvisibleError
	var crossTicket *GuestLinkScopeError
	switch want {
	case "visible":
		var invisible *TicketInvisibleError
		var crossTicket *GuestLinkScopeError
		if errors.As(err, &invisible) || errors.As(err, &crossTicket) || errors.Is(err, ErrNotFound) {
			t.Fatalf("可见凭证被访问检查拒绝：%v", err)
		}
	case "invisible":
		if !errors.As(err, &invisible) || invisible.Seq != seq {
			t.Fatalf("错误 = %v, 想成员不可见工单 %d", err, seq)
		}
		if invisible.Viewer != viewer {
			t.Fatalf("成员不可见错误未带回请求凭证：%+v, 想 %+v", invisible.Viewer, viewer)
		}
		if errors.Is(err, ErrNotFound) {
			t.Fatalf("成员不可见不应包住 ErrNotFound：%v", err)
		}
	case "cross-ticket":
		if !errors.As(err, &crossTicket) || crossTicket.Seq != seq {
			t.Fatalf("错误 = %v, 想访客跨工单 %d", err, seq)
		}
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("访客跨工单应对外保持 ErrNotFound：%v", err)
		}
	case "not-found":
		if !errors.Is(err, ErrNotFound) || errors.As(err, &invisible) || errors.As(err, &crossTicket) {
			t.Fatalf("错误 = %v, 想普通不存在", err)
		}
	default:
		t.Fatalf("未知期望 %q", want)
	}
}

func TestForViewer按凭证读工单(t *testing.T) {
	f := newViewerAccessFixture(t)
	ctx := context.Background()
	assertAccessResult(t, accessErr(f.manager.ForViewer(ctx, f.creator, "1")), "visible", 1, f.creator)
	assertAccessResult(t, accessErr(f.manager.ForViewer(ctx, f.outsider, "1")), "invisible", 1, f.outsider)
	assertAccessResult(t, accessErr(f.manager.ForViewer(ctx, guestViewer(1), "1")), "visible", 1, guestViewer(1))
	assertAccessResult(t, accessErr(f.manager.ForViewer(ctx, guestViewer(2), "1")), "cross-ticket", 1, guestViewer(2))
	assertAccessResult(t, accessErr(f.manager.ForViewer(ctx, f.creator, "404")), "not-found", 0, f.creator)
}

func accessErr(_ any, err error) error { return err }

func Test写方法按凭证加载工单(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, f viewerAccessFixture, viewer identity.Viewer, ref string) error
	}{
		{"Reply", func(t *testing.T, f viewerAccessFixture, viewer identity.Viewer, ref string) error {
			_, err := f.manager.Reply(context.Background(), viewer, ref, ReplyInput{BodyMD: "回复"})
			return err
		}},
		{"Revise", func(t *testing.T, f viewerAccessFixture, viewer identity.Viewer, ref string) error {
			_, err := f.manager.Revise(context.Background(), viewer, ref, ReviseInput{BaseRevision: 1})
			return err
		}},
		{"Close", func(t *testing.T, f viewerAccessFixture, viewer identity.Viewer, ref string) error {
			_, err := f.manager.Close(context.Background(), viewer, ref, CloseInput{Reason: "done"})
			return err
		}},
		{"Reopen", func(t *testing.T, f viewerAccessFixture, viewer identity.Viewer, ref string) error {
			_, err := f.manager.Reopen(context.Background(), viewer, ref)
			return err
		}},
		{"Assign", func(t *testing.T, f viewerAccessFixture, viewer identity.Viewer, ref string) error {
			_, err := f.manager.Assign(context.Background(), viewer, ref, "")
			return err
		}},
		{"AddCollaborator", func(t *testing.T, f viewerAccessFixture, viewer identity.Viewer, ref string) error {
			_, err := f.manager.AddCollaborator(context.Background(), viewer, ref, "局外成员")
			return err
		}},
		{"RemoveCollaborator", func(t *testing.T, f viewerAccessFixture, viewer identity.Viewer, ref string) error {
			_, err := f.manager.RemoveCollaborator(context.Background(), viewer, ref, 4)
			return err
		}},
		{"LeaveCollaborators", func(t *testing.T, f viewerAccessFixture, viewer identity.Viewer, ref string) error {
			_, err := f.manager.LeaveCollaborators(context.Background(), viewer, ref)
			return err
		}},
		{"CreateGuestLink", func(t *testing.T, f viewerAccessFixture, viewer identity.Viewer, ref string) error {
			_, err := f.manager.CreateGuestLink(context.Background(), viewer, ref,
				GuestLinkSpec{Note: "新链接", ValidDays: 7})
			return err
		}},
		{"RevokeGuestLink", func(t *testing.T, f viewerAccessFixture, viewer identity.Viewer, ref string) error {
			return f.manager.RevokeGuestLink(context.Background(), viewer, ref, 1)
		}},
		{"ExtendGuestLink", func(t *testing.T, f viewerAccessFixture, viewer identity.Viewer, ref string) error {
			_, err := f.manager.ExtendGuestLink(context.Background(), viewer, ref, 1,
				GuestLinkSpec{ValidDays: 30})
			return err
		}},
		{"ResetGuestAccessCode", func(t *testing.T, f viewerAccessFixture, viewer identity.Viewer, ref string) error {
			_, err := f.manager.ResetGuestAccessCode(context.Background(), viewer, ref, 1)
			return err
		}},
		{"SetLabels", func(t *testing.T, f viewerAccessFixture, viewer identity.Viewer, ref string) error {
			_, err := f.manager.SetLabels(context.Background(), SetLabelsInput{Ref: ref, Actor: viewer})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			credentials := []struct {
				name   string
				ref    string
				seq    int64
				viewer identity.Viewer
				want   string
			}{
				{"member visible", "1", 1, memberViewer(1, "创建者"), "visible"},
				{"member invisible", "1", 1, memberViewer(3, "局外成员"), "invisible"},
				{"guest own ticket", "1", 1, guestViewer(1), "visible"},
				{"guest other ticket", "1", 1, guestViewer(2), "cross-ticket"},
				{"missing ticket", "404", 0, memberViewer(1, "创建者"), "not-found"},
			}
			for _, credential := range credentials {
				t.Run(credential.name, func(t *testing.T) {
					fixture := newViewerAccessFixture(t)
					viewer := credential.viewer
					ref := credential.ref
					guestOwn, guestCross := guestViewer(1), guestViewer(2)
					if tc.name == "Reopen" && credential.name != "missing ticket" {
						ref = "2"
						credential.seq = 2
						guestOwn, guestCross = guestViewer(2), guestViewer(1)
					}
					if credential.name == "guest own ticket" {
						viewer = guestOwn
					}
					if credential.name == "guest other ticket" {
						viewer = guestCross
					}
					if tc.name == "LeaveCollaborators" && credential.name == "member visible" {
						viewer = memberViewer(4, "可退出成员")
					}
					err := tc.run(t, fixture, viewer, ref)
					assertAccessResult(t, err, credential.want, credential.seq, viewer)
				})
			}
		})
	}
}
