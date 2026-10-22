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

func Test迁移把已有AgentToken改为必须走草稿且重启不改回手动设置(t *testing.T) {
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
		if strings.HasPrefix(name, "0037_") {
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
		 VALUES (1,'wang','王工','x',0,'2026-10-21T09:00:00+08:00')`,
		`INSERT INTO agent_tokens (id, member_id, name, token_hash, draft_mode, created_at)
		 VALUES (1,1,'笔记本','old-hash','direct','2026-10-21T09:00:00+08:00')`,
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
	var mode string
	if err := s.DB.QueryRowContext(context.Background(),
		`SELECT draft_mode FROM agent_tokens WHERE id=1`).Scan(&mode); err != nil || mode != "required" {
		t.Fatalf("迁移后 draft_mode = %q (err=%v), 想 required", mode, err)
	}

	if _, err := s.DB.Exec(`UPDATE agent_tokens SET draft_mode='direct' WHERE id=1`); err != nil {
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
	if err := s.DB.QueryRowContext(context.Background(),
		`SELECT draft_mode FROM agent_tokens WHERE id=1`).Scan(&mode); err != nil || mode != "direct" {
		t.Fatalf("重启后 draft_mode = %q (err=%v), 想 direct（不覆盖手动设置）", mode, err)
	}
}
