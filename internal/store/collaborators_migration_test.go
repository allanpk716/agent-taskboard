package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/store"
	_ "modernc.org/sqlite"
)

func Test迁移把历史回复者补为协作者并记录系统事件(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", store.FileURI(filepath.Join(dir, "taskboard.db")))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("../store/migrations")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "0019_") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		body, err := os.ReadFile(filepath.Join("../store/migrations", name))
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
	seed := []string{
		`INSERT INTO members (id, username, name, password_hash, is_admin, created_at)
		 VALUES (1,'creator','创建者','x',1,'2026-10-16T09:00:00+08:00'),
		        (2,'current','当前指派人','x',0,'2026-10-16T09:00:00+08:00'),
		        (3,'former','前任指派人','x',0,'2026-10-16T09:00:00+08:00'),
		        (4,'other','普通回复者','x',0,'2026-10-16T09:00:00+08:00')`,
		`INSERT INTO tickets (seq, number, kind, title, status, brief_md, creator_member_id,
		 assignee_id, created_at, last_change)
		 VALUES (1,'20261016-001','task','旧工单','open','旧说明',1,2,'2026-10-16T09:00:00+08:00',4)`,
		`INSERT INTO ticket_labels (ticket_seq,label) VALUES (1,'机型:UD-2300')`,
		`INSERT INTO timeline_events (ticket_seq,seq,change_seq,type,actor_member_id,at)
		 VALUES (1,1,1,'created',1,'2026-10-16T09:00:00+08:00'),
		        (1,2,2,'assigned',1,'2026-10-16T09:00:00+08:00'),
		        (1,3,3,'reply',1,'2026-10-16T09:01:00+08:00'),
		        (1,4,4,'reply',4,'2026-10-16T09:02:00+08:00')`,
		`UPDATE timeline_events SET assign_from='', assign_to='前任指派人' WHERE seq=2`,
	}
	for _, q := range seed {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("造旧数据失败：%v\n%s", err, q)
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
	var (
		memberID int64
		events   int
		system   int
		last     int64
		notes    int
	)
	if err := s.DB.QueryRowContext(context.Background(),
		`SELECT member_id FROM ticket_collaborators WHERE ticket_seq=1`).Scan(&memberID); err != nil {
		t.Fatalf("读取回填协作者失败：%v", err)
	}
	if memberID != 4 {
		t.Fatalf("回填成员 = %d, 想 4（创建者、当前与前任指派人都不补）", memberID)
	}
	if err := s.DB.QueryRowContext(context.Background(), `
		SELECT COUNT(*), SUM(actor_system), MAX(change_seq) FROM timeline_events
		WHERE type='collaborator_added' AND collaborator_member_id=4`).Scan(&events, &system, &last); err != nil {
		t.Fatal(err)
	}
	if events != 1 || system != 1 || last != 5 {
		t.Fatalf("系统加入事件不对：count=%d system=%d change=%d", events, system, last)
	}
	if err := s.DB.QueryRowContext(context.Background(),
		`SELECT last_change FROM tickets WHERE seq=1`).Scan(&last); err != nil || last != 5 {
		t.Fatalf("迁移后 last_change = %d (err=%v), 想 5", last, err)
	}
	if err := s.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM notifications`).Scan(&notes); err != nil || notes != 0 {
		t.Fatalf("迁移不应发通知：count=%d err=%v", notes, err)
	}
}
