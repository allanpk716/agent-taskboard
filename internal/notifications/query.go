package notifications

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Item struct {
	ID            int64
	MemberID      int64
	Kind          string
	Reason        string
	Text          string
	Summary       string
	Number        string
	Title         string
	EventSeq      int64
	EventType     string
	ActorKind     string
	ActorName     string
	ActorMemberID int64
	ActorAgentID  int64
	CreatedAt     time.Time
	ReadAt        *time.Time
}

type ListQuery struct {
	MemberID int64
	After    *int64
	Before   *int64
	Unread   bool
	Limit    int
}

type ListResult struct {
	Items       []Item
	Cursor      int64
	UnreadCount int
	Next        string
}

func (m *Manager) List(ctx context.Context, q ListQuery) (ListResult, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	where := []string{"member_id = ?"}
	args := []any{q.MemberID}
	ascending := q.After != nil
	if q.After != nil {
		where = append(where, "id > ?")
		args = append(args, *q.After)
	}
	if q.Before != nil {
		where = append(where, "id < ?")
		args = append(args, *q.Before)
	}
	if q.Unread {
		where = append(where, "read_at IS NULL")
	}
	order := "DESC"
	if ascending {
		order = "ASC"
	}
	query := `SELECT id, member_id, kind, reason, text, summary, ticket_number, ticket_title,
		trigger_seq, trigger_type, actor_kind, actor_name, actor_member_id, actor_agent_id, created_at, read_at
		FROM notifications WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY id ` + order + ` LIMIT ?`
	rows, err := m.db.QueryContext(ctx, query, append(args, q.Limit+1)...)
	if err != nil {
		return ListResult{}, err
	}
	defer rows.Close()
	items := []Item{}
	for rows.Next() {
		var it Item
		var createdAt string
		var readAt sql.NullString
		var summary sql.NullString
		var actorMemberID, actorAgentID sql.NullInt64
		if err := rows.Scan(&it.ID, &it.MemberID, &it.Kind, &it.Reason, &it.Text, &summary,
			&it.Number, &it.Title, &it.EventSeq, &it.EventType, &it.ActorKind, &it.ActorName,
			&actorMemberID, &actorAgentID,
			&createdAt, &readAt); err != nil {
			return ListResult{}, err
		}
		it.Summary = summary.String
		it.ActorMemberID = actorMemberID.Int64
		it.ActorAgentID = actorAgentID.Int64
		if it.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
			return ListResult{}, fmt.Errorf("解析通知时间 %q 失败：%w", createdAt, err)
		}
		if readAt.Valid && readAt.String != "" {
			t, err := time.Parse(time.RFC3339Nano, readAt.String)
			if err != nil {
				return ListResult{}, fmt.Errorf("解析通知已读时间 %q 失败：%w", readAt.String, err)
			}
			it.ReadAt = &t
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, err
	}
	hasMore := false
	if len(items) > q.Limit {
		hasMore = true
		items = items[:q.Limit]
	}
	cursor := int64(0)
	if q.After != nil {
		cursor = *q.After
	}
	for _, it := range items {
		if it.ID > cursor {
			cursor = it.ID
		}
	}
	next := ""
	if !ascending && hasMore && len(items) > 0 {
		next = "/api/v1/notifications?before=" + strconv.FormatInt(items[len(items)-1].ID, 10)
	}
	unread, err := m.Unread(ctx, q.MemberID)
	if err != nil {
		return ListResult{}, err
	}
	return ListResult{Items: items, Cursor: cursor, UnreadCount: unread.Count, Next: next}, nil
}

type UnreadResult struct {
	Count    int
	LatestID int64
}

func (m *Manager) Unread(ctx context.Context, memberID int64) (UnreadResult, error) {
	var out UnreadResult
	err := m.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(read_at IS NULL), 0), COALESCE(MAX(id), 0) FROM notifications WHERE member_id = ?`,
		memberID).Scan(&out.Count, &out.LatestID)
	return out, err
}

// ReadTarget 是一次标已读请求的三选一目标。
type ReadTarget struct {
	IDs    []int64
	Ticket string
	UpTo   int64
}

// MarkReadResult 返回这次真正从未读改为已读的通知 id，以及提交后的未读数。
// #79 的 read 广播应从这一个返回值取数据。
type MarkReadResult struct {
	IDs         []int64
	UnreadCount int
}

func (m *Manager) MarkRead(ctx context.Context, memberID int64, target ReadTarget) (MarkReadResult, error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return MarkReadResult{}, err
	}
	defer tx.Rollback()

	where := "member_id = ? AND read_at IS NULL"
	args := []any{memberID}
	switch {
	case target.Ticket != "":
		where += " AND ticket_number = ?"
		args = append(args, target.Ticket)
	case len(target.IDs) > 0:
		ph := make([]string, len(target.IDs))
		for i, id := range target.IDs {
			ph[i] = "?"
			args = append(args, id)
		}
		where += " AND id IN (" + strings.Join(ph, ",") + ")"
	default:
		where += " AND id <= ?"
		args = append(args, target.UpTo)
	}

	rows, err := tx.QueryContext(ctx, "SELECT id FROM notifications WHERE "+where+" ORDER BY id", args...)
	if err != nil {
		return MarkReadResult{}, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return MarkReadResult{}, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return MarkReadResult{}, err
	}
	rows.Close()
	if len(ids) > 0 {
		readAt := m.clk.Now().Format(time.RFC3339Nano)
		ph := make([]string, len(ids))
		updateArgs := make([]any, 0, len(ids)+1)
		updateArgs = append(updateArgs, readAt, memberID)
		for i, id := range ids {
			ph[i] = "?"
			updateArgs = append(updateArgs, id)
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE notifications SET read_at = ? WHERE member_id = ? AND id IN ("+strings.Join(ph, ",")+")",
			updateArgs...); err != nil {
			return MarkReadResult{}, err
		}
	}
	var unread int
	if err := tx.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(read_at IS NULL), 0) FROM notifications WHERE member_id = ?", memberID,
	).Scan(&unread); err != nil {
		return MarkReadResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return MarkReadResult{}, err
	}
	return MarkReadResult{IDs: ids, UnreadCount: unread}, nil
}
