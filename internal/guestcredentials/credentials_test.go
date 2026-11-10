package guestcredentials_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/guestcredentials"
	"github.com/allanpk716/agent-taskboard/internal/store"
)

func TestHolder支持认领验码设备签发解析和重置(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seed := []string{
		`INSERT INTO members (id, username, name, password_hash, is_admin, created_at)
		 VALUES (1,'wang','王工','x',1,'2026-11-02T09:00:00+08:00')`,
		`INSERT INTO tickets (seq, number, kind, title, status, brief_md, creator_member_id, assignee_id, created_at, last_change)
		 VALUES (1,'20261102-001','task','工单','open','说明',1,1,'2026-11-02T09:00:00+08:00',1)`,
		`INSERT INTO guest_links (id, ticket_seq, key_hash, note, expires_at, can_edit_brief,
		                         created_by_member_id, created_at)
		 VALUES (1,1,'key-hash','检验科-张三','2099-01-01T00:00:00+08:00',1,1,
		         '2026-11-02T09:00:00+08:00')`,
	}
	for _, query := range seed {
		if _, err := s.DB.Exec(query); err != nil {
			t.Fatalf("造数据失败：%v\n%s", err, query)
		}
	}

	ctx := context.Background()
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, clock.Beijing)
	holder, err := guestcredentials.CreateLinkHolder(ctx, s.DB, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := guestcredentials.Claim(ctx, s.DB, holder.ID, "张三", now)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := guestcredentials.ResolveDevice(ctx, s.DB, claim.DeviceToken)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.HolderID != holder.ID || resolved.OwnerType != guestcredentials.OwnerTypeGuestLink ||
		resolved.OwnerID != 1 || resolved.Link == nil || resolved.Link.TicketSeq != 1 || resolved.Link.ClaimName != "张三" {
		t.Fatalf("按设备解析结果不对：%+v link=%+v", resolved, *resolved.Link)
	}
	if _, err := guestcredentials.VerifyAccessCode(ctx, s.DB, holder.ID, claim.AccessCode, now); err != nil {
		t.Fatalf("正确访问码不应失败：%v", err)
	}

	var becameLocked bool
	for i := 0; i < 5; i++ {
		becameLocked, err = guestcredentials.VerifyAccessCode(ctx, s.DB, holder.ID, "000000", now)
		if err != nil && !errors.Is(err, guestcredentials.ErrAccessCodeInvalid) &&
			!errors.Is(err, guestcredentials.ErrAccessCodeLocked) {
			t.Fatalf("第 %d 次错码返回意外错误：%v", i+1, err)
		}
	}
	if !becameLocked || !errors.Is(err, guestcredentials.ErrAccessCodeLocked) {
		t.Fatalf("第 5 次错码应刚锁定：becameLocked=%v err=%v", becameLocked, err)
	}

	later := now.Add(time.Hour)
	reset, err := guestcredentials.Reset(ctx, s.DB, holder.ID, later)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guestcredentials.ResolveDevice(ctx, s.DB, claim.DeviceToken); !errors.Is(err, guestcredentials.ErrDeviceNotFound) {
		t.Fatalf("重置后旧设备应失效：err=%v", err)
	}
	if _, err := guestcredentials.VerifyAccessCode(ctx, s.DB, holder.ID, reset.AccessCode, later); err != nil {
		t.Fatalf("新访问码不应失效：%v", err)
	}
	issued, err := guestcredentials.IssueDevice(ctx, s.DB, holder.ID, "access_code", later)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guestcredentials.ResolveDevice(ctx, s.DB, issued); err != nil {
		t.Fatalf("访问码换新设备失败：%v", err)
	}
}
