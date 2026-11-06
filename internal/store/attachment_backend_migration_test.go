package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func Test老库升级后附件后端标记为本地(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", FileURI(filepath.Join(dir, "taskboard.db")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		var version int
		if _, err := fmt.Sscanf(entry.Name(), "%d_", &version); err != nil {
			t.Fatal(err)
		}
		if version < 45 {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
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
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	opened, err := Open(dir)
	if err != nil {
		t.Fatalf("老库升级失败：%v", err)
	}
	defer opened.Close()
	var backend string
	if err := opened.DB.QueryRow(`SELECT value FROM settings WHERE key='attachments_backend'`).Scan(&backend); err != nil || backend != "local" {
		t.Fatalf("老库升级后的附件后端 = %q/%v, 想 local", backend, err)
	}
}
