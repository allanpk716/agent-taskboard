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
	"github.com/allanpk716/agent-taskboard/internal/store"
	_ "modernc.org/sqlite"
)

// #348 访客身份迁移：上线前的老链接保持无主（不自动聚合），原有的设备
// 凭证与单条链接认领语义继续可用。
func Test迁移后老访客链接保持无主且设备凭证可用(t *testing.T) {
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
		if strings.HasPrefix(name, "0048_") {
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

	const oldDeviceToken = "tbgd_old-guest-device"
	seed := []string{
		`INSERT INTO members (id, username, name, password_hash, is_admin, created_at)
		 VALUES (1,'wang','王工','x',1,'2026-11-01T09:00:00+08:00')`,
		`INSERT INTO tickets (seq, number, kind, title, status, brief_md, creator_member_id, assignee_id, created_at, last_change)
		 VALUES (1,'20261101-001','task','旧工单','open','旧说明',1,1,'2026-11-01T09:00:00+08:00',1)`,
		`INSERT INTO guest_links (id, ticket_seq, key_hash, note, expires_at, can_edit_brief,
		                         created_by_member_id, created_at)
		 VALUES (1,1,'old-key-hash','检验科-张三','2099-01-01T00:00:00+08:00',1,1,
		         '2026-11-01T09:00:00+08:00')`,
		`INSERT INTO guest_credential_holders (id, owner_type, owner_id, access_code_failures, created_at)
		 VALUES (1,'guest_link',1,0,'2026-11-01T09:00:00+08:00')`,
		`UPDATE guest_links SET credential_holder_id = 1 WHERE id = 1`,
		`INSERT INTO guest_credential_devices (credential_holder_id, token_hash, entry_method, created_at)
		 VALUES (1,'` + identity.CredentialHash(oldDeviceToken) + `','device','2026-11-01T10:00:00+08:00')`,
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
	var guestID sql.NullInt64
	if err := s.DB.QueryRowContext(ctx, `SELECT guest_id FROM guest_links WHERE id = 1`).Scan(&guestID); err != nil {
		t.Fatal(err)
	}
	if guestID.Valid {
		t.Fatalf("老链接迁移后 guest_id = %d，想保持无主", guestID.Int64)
	}
	var guests int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM guests`).Scan(&guests); err != nil || guests != 0 {
		t.Fatalf("迁移不应自动建访客：guests=%d (err=%v)", guests, err)
	}
	var holderType string
	if err := s.DB.QueryRowContext(ctx,
		`SELECT owner_type FROM guest_credential_holders WHERE id = 1`).Scan(&holderType); err != nil || holderType != "guest_link" {
		t.Fatalf("老链接的凭证持有者应保持 guest_link：%s (err=%v)", holderType, err)
	}

	idm := identity.NewManager(s.DB, clock.NewFake(time.Date(2026, 11, 2, 9, 0, 0, 0, clock.Beijing)))
	viewer, err := idm.ViewerFromGuestDevice(ctx, oldDeviceToken)
	if err != nil || viewer.Kind != identity.ViewerGuest || viewer.GuestLink == nil || viewer.GuestLink.ID != 1 {
		t.Fatalf("老设备凭证迁移后不可用：viewer=%+v err=%v", viewer, err)
	}
	if viewer.Guest != nil {
		t.Fatalf("无主老链接不应解析成长期访客身份：%+v", viewer.Guest)
	}
}
