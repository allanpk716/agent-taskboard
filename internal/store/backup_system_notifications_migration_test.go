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

func Test系统通知迁移保留既有数据并强制CHECK(t *testing.T) {
	dataDir := t.TempDir()
	db, err := sql.Open("sqlite", store.FileURI(filepath.Join(dataDir, "taskboard.db")))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("../store/migrations")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "0025_") && !strings.HasPrefix(e.Name(), "0032_") {
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
		 VALUES (1,'admin','管理员','x',1,'2026-10-16T09:00:00+08:00')`,
		`INSERT INTO tickets (
			seq, number, kind, title, brief_md, creator_member_id, created_at, last_change
		) VALUES (1,'20261016-001','task','旧通知','x',1,'2026-10-16T09:00:00+08:00',1)`,
		`INSERT INTO notifications (
			id, member_id, ticket_seq, ticket_number, ticket_title, kind, reason, text,
			trigger_seq, trigger_type, actor_kind, actor_name, actor_member_id, actor_agent_id,
			summary, created_at, read_at
		) VALUES (
			7,1,1,'20261016-001','旧通知','turn','assigned','旧通知正文',
			1,'created','member','王工',1,NULL,'摘要',
			'2026-10-16T09:01:00+08:00','2026-10-16T10:00:00+08:00'
		)`,
		`INSERT INTO backup_records (
			at, kind, filename, size, integrity_ok, files_added, error
		) VALUES (
			'2026-10-16T03:00:00+08:00','daily','taskboard-20261016-030000-daily.db',
			100,1,0,NULL
		)`,
	}
	for _, q := range seed {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("造旧数据失败：%v\n%s", err, q)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	var (
		id, memberID, ticketSeq         int64
		ticketNumber, ticketTitle, kind string
		reason, text, triggerType       string
		actorKind, actorName            string
		actorMemberID, actorAgentID     sql.NullInt64
		triggerSeq                      int64
		summary, url, createdAt, readAt string
	)
	err = st.DB.QueryRowContext(ctx, `
		SELECT id, member_id, ticket_seq, ticket_number, ticket_title, kind, reason, text,
		       trigger_seq, trigger_type, actor_kind, actor_name, actor_member_id,
		       actor_agent_id, summary, url, created_at, read_at
		FROM notifications WHERE id = 7`).Scan(
		&id, &memberID, &ticketSeq, &ticketNumber, &ticketTitle, &kind, &reason, &text,
		&triggerSeq, &triggerType, &actorKind, &actorName, &actorMemberID,
		&actorAgentID, &summary, &url, &createdAt, &readAt)
	if err != nil {
		t.Fatalf("读取迁移后的旧通知失败：%v", err)
	}
	want := []bool{
		id == 7, memberID == 1, ticketSeq == 1,
		ticketNumber == "20261016-001", ticketTitle == "旧通知",
		kind == "turn", reason == "assigned", text == "旧通知正文",
		triggerSeq == 1, triggerType == "created", actorKind == "member",
		actorName == "王工", actorMemberID.Int64 == 1 && actorMemberID.Valid,
		!actorAgentID.Valid, summary == "摘要", url == "/t/20261016-001",
		createdAt == "2026-10-16T09:01:00+08:00", readAt == "2026-10-16T10:00:00+08:00",
	}
	for _, ok := range want {
		if !ok {
			t.Fatalf("迁移后旧通知被改动：%+v", map[string]any{
				"id": id, "member_id": memberID, "ticket_seq": ticketSeq,
				"ticket_number": ticketNumber, "ticket_title": ticketTitle, "kind": kind,
				"reason": reason, "text": text, "trigger_seq": triggerSeq,
				"trigger_type": triggerType, "actor_kind": actorKind, "actor_name": actorName,
				"actor_member_id": actorMemberID, "actor_agent_id": actorAgentID,
				"summary": summary, "url": url, "created_at": createdAt, "read_at": readAt,
			})
		}
	}

	if _, err := st.DB.ExecContext(ctx, `INSERT INTO notifications (
		member_id, kind, reason, text, url, created_at
	) VALUES (1,'fyi','backup_failing','新系统通知','/admin#backup','2026-10-16T11:00:00+08:00')`); err != nil {
		t.Fatal(err)
	}
	var newID int64
	if err := st.DB.QueryRowContext(ctx, `SELECT MAX(id) FROM notifications`).Scan(&newID); err != nil || newID <= id {
		t.Fatalf("迁移后新通知 id = %d（旧最大 id=%d），err=%v", newID, id, err)
	}

	var ddl string
	if err := st.DB.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type='table' AND name='notifications'`).Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ddl, "AUTOINCREMENT") {
		t.Fatalf("notifications 建表语句缺少 AUTOINCREMENT：\n%s", ddl)
	}
	var location string
	if err := st.DB.QueryRowContext(ctx, `SELECT location FROM backup_records`).Scan(&location); err != nil || location != "target" {
		t.Fatalf("旧备份记录 location = %q, err=%v", location, err)
	}

	if _, err := st.DB.ExecContext(ctx, `INSERT INTO notifications (
		member_id, kind, reason, text, url, created_at
	) VALUES (1,'fyi','assigned','缺少工单','/admin#backup','2026-10-16T11:01:00+08:00')`); err == nil {
		t.Fatal("工单类 reason 不带工单应被 CHECK 拦住")
	}
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO notifications (
		member_id, ticket_seq, ticket_number, ticket_title, kind, reason, text,
		trigger_seq, trigger_type, actor_kind, actor_name, url, created_at
	) VALUES (1,1,'20261016-001','旧通知','fyi','backup_failing','系统通知不能挂工单',
		1,'created','member','王工','/admin#backup','2026-10-16T11:02:00+08:00')`); err == nil {
		t.Fatal("系统 reason 带工单应被 CHECK 拦住")
	}
}
