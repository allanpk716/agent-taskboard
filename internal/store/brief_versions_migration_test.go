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

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/labels"
	"github.com/allanpk716/agent-taskboard/internal/store"
	"github.com/allanpk716/agent-taskboard/internal/templates"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
	_ "modernc.org/sqlite"
)

func Test迁移回填说明当前版本且重启不重复(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", store.FileURI(filepath.Join(dir, "taskboard.db")))
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir("migrations")
	names := []string{}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "0028_") {
			names = append(names, e.Name())
		}
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
		fmt.Sscanf(name, "%d_", &version)
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (?, datetime('now'))`, version); err != nil {
			t.Fatal(err)
		}
	}
	seed := []string{
		`INSERT INTO members (id, username, name, password_hash, is_admin, created_at)
		 VALUES (1,'chen','陈工','x',1,'2026-10-16T09:00:00+08:00')`,
		`INSERT INTO guest_links (id,ticket_seq,note,key_hash,expires_at,created_by_member_id,created_at)
		 VALUES (7,3,'检验科-张三','x','2026-11-16T09:00:00+08:00',1,'2026-10-16T09:00:00+08:00')`,
		`INSERT INTO tickets (seq, number, kind, title, status, brief_md, brief_revision, creator_member_id, created_at, last_change)
		 VALUES (1,'20261016-001','task','未修订','open','第 1 版',1,1,'2026-10-16T09:00:00+08:00',1),
		        (2,'20261016-002','task','已修订','open','第 3 版',3,1,'2026-10-16T09:10:00+08:00',3),
		        (3,'20261016-003','task','访客修订','open','访客的第 2 版',2,1,'2026-10-16T09:20:00+08:00',2)`,
		`INSERT INTO timeline_events (ticket_seq,seq,change_seq,type,actor_member_id,
		 actor_link_id,actor_link_note,actor_author_name,at,revision)
		 VALUES (1,1,1,'created',1,NULL,NULL,NULL,'2026-10-16T09:00:00+08:00',NULL),
		        (2,1,2,'created',1,NULL,NULL,NULL,'2026-10-16T09:10:00+08:00',NULL),
		        (2,2,3,'brief_revised',1,NULL,NULL,NULL,'2026-10-16T09:11:00+08:00',3),
		        (3,1,4,'created',1,NULL,NULL,NULL,'2026-10-16T09:20:00+08:00',NULL),
		        (3,2,5,'brief_revised',NULL,7,'检验科-张三','检验科 张三','2026-10-16T09:21:00+08:00',2)`,
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
	m := tickets.NewManager(s.DB, clock.Real{}, labels.NewManager(s.DB), templates.NewManager(s.DB, clock.Real{}), nil)
	v1, err := m.BriefVersion(context.Background(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if v1.BriefMD != "第 1 版" || v1.Title != "未修订" {
		t.Fatalf("未修订工单回填不对：%+v", v1)
	}
	v3, err := m.BriefVersion(context.Background(), 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if v3.BriefMD != "第 3 版" || v3.At.IsZero() {
		t.Fatalf("修订工单当前版回填不对：%+v", v3)
	}
	v2, err := m.BriefVersion(context.Background(), 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if v2.Author.MemberID != 0 || v2.Author.LinkID != 7 || v2.Author.AuthorName != "检验科 张三" {
		t.Fatalf("访客修订回填作者不对：%+v", v2.Author)
	}
	if _, err := m.BriefVersion(context.Background(), 2, 1); err == nil {
		t.Fatal("旧版本应推断为上线前未存档")
	}
	var events, notes int
	if err := s.DB.QueryRow(`SELECT
		(SELECT COUNT(*) FROM timeline_events WHERE type='brief_revised'),
		(SELECT COUNT(*) FROM notifications)`).Scan(&events, &notes); err != nil || events != 2 || notes != 0 {
		t.Fatalf("迁移产生事件/通知：%d/%d (err=%v)", events, notes, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	var count int
	if err := s2.DB.QueryRow(`SELECT COUNT(*) FROM brief_versions`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("重启后版本行 = %d (err=%v), 想 3", count, err)
	}
}
