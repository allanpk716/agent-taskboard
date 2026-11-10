package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/store"
	_ "modernc.org/sqlite"
)

// #329 给已有访客增加主页密钥字段：迁移保持空值，不凭空生成地址；
// 之后第一次复制主页文案时再生成可恢复密钥。
func Test迁移后已有访客暂无主页密钥(t *testing.T) {
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
		var version int
		if _, err := fmt.Sscanf(name, "%d_", &version); err != nil || version >= 49 {
			continue
		}
		body, err := os.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("执行旧迁移 %s 失败：%v", name, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (?, datetime('now'))`,
			version); err != nil {
			t.Fatal(err)
		}
	}
	seed := []string{
		`INSERT INTO members (id, username, name, password_hash, is_admin, created_at)
		 VALUES (1,'wang','王工','x',1,'2026-11-01T09:00:00+08:00')`,
		`INSERT INTO guests (id, name, created_by_member_id, created_at)
		 VALUES (1,'检验科-张三',1,'2026-11-01T09:00:00+08:00')`,
		`INSERT INTO guest_credential_holders (id, owner_type, owner_id, access_code_failures, created_at)
		 VALUES (1,'guest',1,0,'2026-11-01T09:00:00+08:00')`,
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
	var hash, ciphertext sql.NullString
	if err := s.DB.QueryRowContext(context.Background(),
		`SELECT home_key_hash, home_key_ciphertext FROM guests WHERE id = 1`).Scan(&hash, &ciphertext); err != nil {
		t.Fatal(err)
	}
	if hash.Valid || ciphertext.Valid {
		t.Fatalf("迁移不应自动生成主页密钥：hash=%v ciphertext=%v", hash, ciphertext)
	}
}
