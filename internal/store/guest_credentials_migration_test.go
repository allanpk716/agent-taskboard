package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/labels"
	"github.com/allanpk716/agent-taskboard/internal/store"
	"github.com/allanpk716/agent-taskboard/internal/templates"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
	_ "modernc.org/sqlite"
)

func Test迁移保留老访客链接的设备凭证访问码和锁定状态(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", store.FileURI(filepath.Join(dir, "taskboard.db")))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "0047_") {
			continue
		}
		body, err := os.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("执行旧迁移 %s 失败：%v", name, err)
		}
		var version int
		if _, err := fmt.Sscanf(name, "%d_", &version); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (?, datetime('now'))`, version); err != nil {
			t.Fatal(err)
		}
	}

	const (
		oldDeviceToken = "tbgd_old-device-token"
		oldAccessCode  = "654321"
	)
	seed := []string{
		`INSERT INTO members (id, username, name, password_hash, is_admin, created_at)
		 VALUES (1,'wang','王工','x',1,'2026-11-01T09:00:00+08:00')`,
		`INSERT INTO tickets (seq, number, kind, title, status, brief_md, creator_member_id, assignee_id, created_at, last_change)
		 VALUES (1,'20261101-001','task','旧工单','open','旧说明',1,1,'2026-11-01T09:00:00+08:00',1)`,
		`INSERT INTO guest_links (id, ticket_seq, key_hash, note, expires_at, can_edit_brief,
		                         created_by_member_id, created_at, claimed_at, claim_name,
		                         access_code_hash, access_code_failures, access_code_locked_at)
		 VALUES (1,1,'old-key-hash','检验科-张三','2099-01-01T00:00:00+08:00',1,1,
		         '2026-11-01T09:00:00+08:00','2026-11-01T10:00:00+08:00','张三',
		         '` + identity.CredentialHash(oldAccessCode) + `',0,NULL),
		        (2,1,'locked-key-hash','检验科-李四','2099-01-01T00:00:00+08:00',0,1,
		         '2026-11-01T09:00:00+08:00','2026-11-01T10:00:00+08:00','李四',
		         'wrong-code-hash',5,'2026-11-01T11:00:00+08:00')`,
		`INSERT INTO guest_devices (guest_link_id, token_hash, created_at, entry_method)
		 VALUES (1,'` + identity.CredentialHash(oldDeviceToken) + `','2026-11-01T10:00:00+08:00','device')`,
	}
	for _, query := range seed {
		if _, err := db.Exec(query); err != nil {
			t.Fatalf("造旧数据失败：%v\n%s", err, query)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 11, 2, 9, 0, 0, 0, clock.Beijing))
	idm := identity.NewManager(s.DB, clk)
	viewer, err := idm.ViewerFromGuestDevice(ctx, oldDeviceToken)
	if err != nil || viewer.Kind != identity.ViewerGuest || viewer.GuestLink == nil || viewer.GuestLink.ID != 1 {
		t.Fatalf("老设备凭证迁移后不可用：viewer=%+v err=%v", viewer, err)
	}

	var holders int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM guest_credential_holders WHERE owner_type='guest_link'`).Scan(&holders); err != nil || holders != 2 {
		t.Fatalf("迁移后的无主链接凭证持有者数量 = %d (err=%v)，想 2", holders, err)
	}
	var devices int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM guest_credential_devices`).Scan(&devices); err != nil || devices != 1 {
		t.Fatalf("迁移后的设备凭证数量 = %d (err=%v)，想 1", devices, err)
	}

	tm := tickets.NewManager(s.DB, clk,
		labels.NewManager(s.DB), templates.NewManager(s.DB, clk), nil)
	entered, err := tm.EnterGuestLink(ctx, identity.GuestLink{
		ID: 1, TicketSeq: 1, Note: "检验科-张三", CredentialHolderID: viewer.GuestLink.CredentialHolderID,
		ExpiresAt: time.Date(2099, 1, 1, 0, 0, 0, 0, clock.Beijing), CanEditBrief: true,
	}, oldAccessCode)
	if err != nil {
		t.Fatalf("老访问码迁移后不应失效：%v", err)
	}
	if _, err := idm.ViewerFromGuestDevice(ctx, entered.DeviceToken); err != nil {
		t.Fatalf("老访问码换新设备失败：%v", err)
	}
	state, _, _, err := tm.GuestLinkStatus(ctx, identity.GuestLink{ID: 2, TicketSeq: 1})
	if err != nil || state != identity.GuestLinkAccessLocked {
		t.Fatalf("锁定链接迁移后状态 = %q (err=%v)，想 %q", state, err, identity.GuestLinkAccessLocked)
	}
}
