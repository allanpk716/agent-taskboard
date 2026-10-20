package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func Test入口附件迁移保留旧附件(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", FileURI(filepath.Join(dir, "taskboard.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entries, _ := os.ReadDir("migrations")
	names := []string{}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "0024_") {
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
		if _, err := db.Exec(`INSERT INTO schema_migrations VALUES (?, datetime('now'))`, version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO members (id,username,name,password_hash,is_admin,created_at,deactivated_at)
		VALUES (1,'admin','管理员','x',1,'2026-10-16T09:00:00+08:00',NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tickets (seq,number,kind,title,status,brief_md,creator_member_id,created_at,last_change)
		VALUES (1,'20261016-001','report','旧反馈','open','旧说明',1,'2026-10-16T09:00:00+08:00',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO attachments (ticket_seq,filename,size,state,uploaded_at,claimed_at)
		VALUES (1,'旧附件.png',3,'attached','2026-10-16T09:00:00+08:00','2026-10-16T09:01:00+08:00')`); err != nil {
		t.Fatal(err)
	}
	// 子表 access_records 引用 attachments(id)：重建父表时这些行不能让迁移失败。
	if _, err := db.Exec(`INSERT INTO access_records (ticket_seq,actor_kind,actor_name,action,attachment_id,first_at,last_at)
		VALUES (1,'member','管理员','download',1,'2026-10-16T09:02:00+08:00','2026-10-16T09:02:00+08:00')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	var ticket int64
	if err := opened.DB.QueryRow(`SELECT ticket_seq FROM attachments WHERE id = 1`).Scan(&ticket); err != nil || ticket != 1 {
		t.Fatalf("旧附件迁移后 ticket_seq = %d/%v, 想 1", ticket, err)
	}
	var ref int64
	if err := opened.DB.QueryRow(`SELECT attachment_id FROM access_records WHERE action = 'download'`).Scan(&ref); err != nil || ref != 1 {
		t.Fatalf("访问记录的 attachment_id 迁移后 = %d/%v, 想 1", ref, err)
	}
}
