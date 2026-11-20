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

func Test通知出口迁移建立每成员一行配置表(t *testing.T) {
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
		var version int
		if _, err := fmt.Sscanf(entry.Name(), "%d_", &version); err != nil || version >= 51 {
			continue
		}
		body, err := os.ReadFile(filepath.Join("migrations", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("执行旧迁移 %s 失败：%v", entry.Name(), err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (?, datetime('now'))`, version); err != nil {
			t.Fatal(err)
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
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO members (username,name,password_hash,is_admin,created_at)
		VALUES ('wang','王工','x',0,'2026-11-20T09:00:00+08:00')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO outlet (
			member_id,bot_id,key_cipher,last_pushed_id,generation,status,failure_count,pause_reason,created_at,updated_at
		) VALUES (1,'bot01','v1:cipher',7,2,'paused',3,'test','2026-11-20T09:01:00+08:00','2026-11-20T09:02:00+08:00')`); err != nil {
		t.Fatalf("合法 outlet 行应能写入：%v", err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO outlet (member_id,bot_id,key_cipher,created_at,updated_at)
		VALUES (1,'bot02','v1:cipher','2026-11-20T09:03:00+08:00','2026-11-20T09:03:00+08:00')`); err == nil {
		t.Fatal("同一成员应只能有一条 outlet")
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO outlet (member_id,bot_id,key_cipher,created_at,updated_at)
		VALUES (99,'bot03','v1:cipher','2026-11-20T09:03:00+08:00','2026-11-20T09:03:00+08:00')`); err == nil {
		t.Fatal("outlet 必须引用真实成员")
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE outlet SET status='disabled'`); err == nil {
		t.Fatal("outlet 状态只允许 active/paused")
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE outlet SET failure_count=-1`); err == nil {
		t.Fatal("outlet 失败计数不能为负")
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE outlet SET retry_state='unknown'`); err == nil {
		t.Fatal("outlet 重试状态只允许 none/retryable/permanent")
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE outlet SET attempt_count=-1`); err == nil {
		t.Fatal("outlet 重试次数不能为负")
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE outlet SET delivery_state='broken'`); err == nil {
		t.Fatal("outlet 投递状态只允许 ok/transient/ignored/permanent")
	}
	var deliveryState string
	if err := s.DB.QueryRowContext(ctx, `SELECT delivery_state FROM outlet WHERE member_id=1`).
		Scan(&deliveryState); err != nil || deliveryState != "ok" {
		t.Fatalf("存量 outlet 的投递状态应缺省 ok，实际 %q err=%v", deliveryState, err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT delivery_state FROM outlet WHERE member_id=1`).
		Scan(new(string)); err != nil || err == nil {
		_ = err
	}

	rows, err := s.DB.QueryContext(ctx, `PRAGMA table_info(outlet)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatal(err)
		}
		got[name] = columnType
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"member_id", "bot_id", "key_cipher", "last_pushed_id", "generation",
		"status", "failure_count", "pause_reason", "created_at", "updated_at",
		"pending_latest_id", "retry_state", "attempt_count", "next_attempt_at", "last_pushed_at",
		"delivery_state", "delivery_state_at", "transient_since", "catchup_after",
	}
	for _, name := range want {
		if _, ok := got[name]; !ok {
			t.Fatalf("outlet 表缺少字段 %s：%v", name, got)
		}
	}
}
