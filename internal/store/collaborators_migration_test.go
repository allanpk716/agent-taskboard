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
	"github.com/allanpk716/agent-taskboard/internal/markdownview"
	"github.com/allanpk716/agent-taskboard/internal/store"
	"github.com/allanpk716/agent-taskboard/internal/templates"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
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
		if !strings.HasPrefix(e.Name(), "0020_") {
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

func Test迁移按回复矩阵补入协作者并在时间线显示系统(t *testing.T) {
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
		if !strings.HasPrefix(e.Name(), "0020_") {
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
		`INSERT INTO members (id, username, name, password_hash, is_admin, created_at, deactivated_at)
		 VALUES (1,'creator','创建者','x',1,'2026-10-16T09:00:00+08:00',NULL),
		        (2,'current','当前指派人','x',0,'2026-10-16T09:00:00+08:00',NULL),
		        (3,'former-silent','没回复的前任','x',0,'2026-10-16T09:00:00+08:00',NULL),
		        (4,'former-replied','回复过的前任','x',0,'2026-10-16T09:00:00+08:00',NULL),
		        (5,'agent-owner','Agent主人','x',0,'2026-10-16T09:00:00+08:00',NULL),
		        (6,'deactivated','已停用回复者','x',0,'2026-10-16T09:00:00+08:00','2026-10-16T12:00:00+08:00')`,
		`INSERT INTO agent_tokens (id, member_id, name, token_hash, created_at)
		 VALUES (1,5,'值班Agent','hash','2026-10-16T09:00:00+08:00')`,
		`INSERT INTO tickets (seq, number, kind, title, status, brief_md, creator_member_id,
		 assignee_id, created_at, last_change)
		 VALUES (1,'20261016-001','task','开启旧工单','open','旧说明',1,2,
		         '2026-10-16T09:00:00+08:00',10),
		        (2,'20261016-002','task','关闭旧工单','closed','旧说明',1,2,
		         '2026-10-16T09:00:00+08:00',10),
		        (3,'20261016-003','task','未指派旧工单','open','旧说明',NULL,NULL,
		         '2026-10-16T09:00:00+08:00',10),
		        (4,'20261016-004','report','反馈旧工单','open','旧说明',NULL,2,
		         '2026-10-16T09:00:00+08:00',10)`,
		`INSERT INTO ticket_labels (ticket_seq,label) VALUES (1,'机型:UD-2300'),(2,'机型:UD-2300')`,
		`INSERT INTO timeline_events (ticket_seq,seq,change_seq,type,actor_member_id,actor_agent_id,at,body_md,reason,assign_from,assign_to)
		 VALUES (1,1,1,'created',1,NULL,'2026-10-16T09:00:00+08:00',NULL,NULL,NULL,NULL),
		        (1,2,2,'assigned',1,NULL,'2026-10-16T09:00:00+08:00',NULL,NULL,'','没回复的前任'),
		        (1,3,3,'assigned',1,NULL,'2026-10-16T09:00:30+08:00',NULL,NULL,'没回复的前任','回复过的前任'),
		        (1,4,4,'reply',4,NULL,'2026-10-16T09:01:00+08:00','我处理过这张单',NULL,NULL,NULL),
		        (1,5,5,'assigned',1,NULL,'2026-10-16T09:02:00+08:00',NULL,NULL,'回复过的前任','当前指派人'),
		        (1,6,6,'reply',5,1,'2026-10-16T09:03:00+08:00','Agent 替主人回复',NULL,NULL,NULL),
		        (1,7,7,'reply',6,NULL,'2026-10-16T09:04:00+08:00','停用前回复',NULL,NULL,NULL),
		        (2,1,8,'created',1,NULL,'2026-10-16T10:00:00+08:00',NULL,NULL,NULL,NULL),
		        (2,2,9,'reply',4,NULL,'2026-10-16T10:01:00+08:00','关闭单里也有回复',NULL,NULL,NULL),
		        (2,3,10,'closed',2,NULL,'2026-10-16T10:02:00+08:00',NULL,'done',NULL,NULL),
		        (3,1,11,'reply',4,NULL,'2026-10-16T11:00:00+08:00','未指派时回复过',NULL,NULL,NULL),
		        (4,1,12,'reply',5,1,'2026-10-16T11:01:00+08:00','反馈入口旧库里的 Agent 回复',NULL,NULL,NULL)`,
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

	var collaborators string
	if err := s.DB.QueryRowContext(context.Background(), `
		SELECT group_concat(member_id, ',') FROM (
			SELECT member_id FROM ticket_collaborators ORDER BY ticket_seq, member_id
		)`).Scan(&collaborators); err != nil {
		t.Fatal(err)
	}
	if collaborators != "4,5,6,4,4,5" {
		t.Fatalf("回填协作者 = %q, 想 4,5,6,4,4,5（前任回复过要补；没回复的前任不补；Agent 回复算主人；停用成员保留；关闭工单覆盖；NULL 创建者/指派人不落队）", collaborators)
	}

	manager := tickets.NewManager(s.DB, clock.Real{}, labels.NewManager(s.DB),
		templates.NewManager(s.DB, clock.Real{}), nil)
	full, err := manager.ByRef(context.Background(), "1")
	if err != nil {
		t.Fatal(err)
	}
	wantCollaborators := map[int64]bool{4: true, 5: true, 6: true}
	for _, collaborator := range full.Collaborators {
		if !wantCollaborators[collaborator.ID] {
			t.Fatalf("意外补入协作者：%+v", collaborator)
		}
		delete(wantCollaborators, collaborator.ID)
		if collaborator.ID == 6 && !collaborator.Deactivated {
			t.Fatalf("已停用成员应保留并标记停用：%+v", collaborator)
		}
	}
	if len(wantCollaborators) != 0 {
		t.Fatalf("缺少应补入的协作者：%v", wantCollaborators)
	}
	var rendered []string
	var formerReplied tickets.Event
	for _, event := range full.Timeline {
		if event.Type != "collaborator_added" {
			continue
		}
		if !event.Actor.System || event.Actor.DisplayName() != "系统" {
			t.Fatalf("协作者加入事件操作者不是系统：%+v", event.Actor)
		}
		rendered = append(rendered, event.Actor.DisplayName()+" 把 "+event.Collaborator+" 加入协作")
		if event.Collaborator == "回复过的前任" {
			formerReplied = event
		}
	}
	if len(rendered) != 3 {
		t.Fatalf("工单 1 的系统加入事件数 = %d, 想 3：%v", len(rendered), rendered)
	}
	md := markdownview.Ticket(markdownview.Input{
		Number: full.Number, Title: full.Title, Status: full.Status,
		WaitingOnText: "无", Timeline: []markdownview.Event{{
			At: formerReplied.At, AuthorName: formerReplied.Actor.DisplayName(),
			Type: "collaborator_added", Collaborator: formerReplied.Collaborator,
		}},
	})
	if !strings.Contains(md, "系统 把 回复过的前任 加入协作") {
		t.Fatalf("Markdown 时间线没有正常显示系统操作者：\n%s", md)
	}

	var beforeCollaborators, beforeEvents, notes int
	if err := s.DB.QueryRowContext(context.Background(), `
		SELECT (SELECT COUNT(*) FROM ticket_collaborators),
		       (SELECT COUNT(*) FROM timeline_events WHERE type='collaborator_added' AND actor_system=1),
		       (SELECT COUNT(*) FROM notifications)`).Scan(
		&beforeCollaborators, &beforeEvents, &notes); err != nil {
		t.Fatal(err)
	}
	if notes != 0 {
		t.Fatalf("迁移不应发通知：count=%d", notes)
	}
	if beforeEvents != 6 {
		t.Fatalf("系统加入事件数 = %d, 想 6", beforeEvents)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var afterCollaborators, afterEvents int
	if err := s.DB.QueryRowContext(context.Background(), `
		SELECT (SELECT COUNT(*) FROM ticket_collaborators),
		       (SELECT COUNT(*) FROM timeline_events WHERE type='collaborator_added' AND actor_system=1)`).Scan(
		&afterCollaborators, &afterEvents); err != nil {
		t.Fatal(err)
	}
	if afterCollaborators != beforeCollaborators || afterEvents != beforeEvents {
		t.Fatalf("服务重启后产生重复数据：协作者 %d→%d，事件 %d→%d",
			beforeCollaborators, afterCollaborators, beforeEvents, afterEvents)
	}
}
