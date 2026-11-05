package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/store"
	_ "modernc.org/sqlite"
)

func Test迁移把已有AgentToken改为可直接发并切换一次性提示(t *testing.T) {
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
		if strings.HasPrefix(name, "0045_") {
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
	seed := []string{
		`INSERT INTO members (id, username, name, password_hash, is_admin, created_at)
		 VALUES (1,'wang','王工','x',0,'2026-11-05T09:00:00+08:00')`,
		`INSERT INTO agent_tokens (id, member_id, name, token_hash, draft_mode, created_at, draft_mode_switched_by_launch)
		 VALUES (1,1,'笔记本','old-hash','required','2026-10-21T09:00:00+08:00',1)`,
		`INSERT INTO agent_tokens (id, member_id, name, token_hash, draft_mode, created_at, revoked_at)
		 VALUES (2,1,'旧工位','older-hash','required','2026-10-21T09:00:00+08:00','2026-10-22T09:00:00+08:00')`,
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
	rows, err := s.DB.QueryContext(context.Background(),
		`SELECT id, draft_mode, draft_mode_switched_to_direct, draft_mode_switched_by_launch
		 FROM agent_tokens ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[int64][3]any{}
	for rows.Next() {
		var id int64
		var mode string
		var directFlag, launchFlag int
		if err := rows.Scan(&id, &mode, &directFlag, &launchFlag); err != nil {
			t.Fatal(err)
		}
		got[id] = [3]any{mode, directFlag, launchFlag}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64][3]any{1: {"direct", 1, 0}, 2: {"direct", 1, 0}} {
		if got[id] != want {
			t.Fatalf("token %d 迁移后 = %v, 想 %v（direct、新标记 1、旧标记 0）", id, got[id], want)
		}
	}

	// 重启不覆盖手动设置，也不重复触发提示。
	if _, err := s.DB.Exec(`UPDATE agent_tokens SET draft_mode='required', draft_mode_switched_to_direct=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var mode string
	var directFlag int
	if err := s.DB.QueryRowContext(context.Background(),
		`SELECT draft_mode, draft_mode_switched_to_direct FROM agent_tokens WHERE id=1`).Scan(&mode, &directFlag); err != nil ||
		mode != "required" || directFlag != 0 {
		t.Fatalf("重启后 = %q/%d (err=%v), 想 required/0（不覆盖手动设置）", mode, directFlag, err)
	}
}
