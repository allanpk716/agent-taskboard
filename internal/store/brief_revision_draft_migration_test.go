package store_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/store"
	_ "modernc.org/sqlite"
)

func Test迁移允许说明修订草稿且保留回复草稿(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", store.FileURI(filepath.Join(dir, "taskboard.db")))
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir("migrations")
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		body, err := os.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("旧迁移 %s 失败：%v", name, err)
		}
		var version int
		if _, err := fmt.Sscanf(name, "%d_", &version); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (?, datetime('now'))`, version); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		`INSERT INTO members (id, username, name, password_hash, is_admin, created_at)
		 VALUES (1,'wang','王工','x',1,'2026-10-16T09:00:00+08:00')`,
		`INSERT INTO tickets (seq, number, kind, title, status, brief_md, brief_revision,
		 creator_member_id, created_at, last_change)
		 VALUES (1,'20261016-001','task','旧工单','open','旧说明',1,1,'2026-10-16T09:00:00+08:00',1)`,
		`INSERT INTO drafts (kind, status, ticket_seq, member_id, request_json, version,
		 base_timeline_seq, created_at, updated_at)
		 VALUES ('reply','sent',1,1,'{}',2,1,'2026-10-16T09:01:00+08:00','2026-10-16T09:02:00+08:00')`,
		`INSERT INTO draft_versions (draft_id, version, request_json, author_member_id, at)
		 VALUES (1,1,'{}',1,'2026-10-16T09:01:00+08:00'),(1,2,'{}',1,'2026-10-16T09:02:00+08:00')`,
	} {
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
	if _, err := s.DB.Exec(`INSERT INTO drafts (kind, ticket_seq, member_id, request_json, version,
		base_timeline_seq, created_at, updated_at)
		VALUES ('brief_revision',1,1,'{}',1,1,'2026-10-16T09:03:00+08:00','2026-10-16T09:03:00+08:00')`); err != nil {
		t.Fatalf("迁移后不能写说明修订草稿：%v", err)
	}
	var versions int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM draft_versions WHERE draft_id = 1`).Scan(&versions); err != nil || versions != 2 {
		t.Fatalf("旧起草过程版本数 = %d (err=%v), 想 2", versions, err)
	}
}
