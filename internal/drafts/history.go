package drafts

import (
	"context"
	"encoding/json"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
)

type HistoryVersion struct {
	Version
	Request Request
}

func (m *Manager) ReplyHistory(ctx context.Context, ticketSeq, replyNo int64) ([]HistoryVersion, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT v.version, v.request_json, v.author_member_id, m.name,
		       COALESCE(v.author_agent_id, 0), COALESCE(a.name, ''), v.at
		FROM drafts d
		JOIN timeline_events e ON e.seq = d.sent_timeline_seq AND e.ticket_seq = d.ticket_seq
		JOIN draft_versions v ON v.draft_id = d.id
		JOIN members m ON m.id = v.author_member_id
		LEFT JOIN agent_tokens a ON a.id = v.author_agent_id
		WHERE d.ticket_seq = ? AND e.reply_no = ? AND d.status = 'sent' AND e.type = 'reply'
		ORDER BY v.version`, ticketSeq, replyNo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryVersion{}
	for rows.Next() {
		var item HistoryVersion
		var request, at string
		if err = rows.Scan(&item.Version.Version, &request, &item.MemberID, &item.MemberName,
			&item.AgentID, &item.AgentName, &at); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(request), &item.Request); err != nil {
			return nil, err
		}
		if item.At, err = time.ParseInLocation(time.RFC3339Nano, at, clock.Beijing); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, nil
}

func (m *Manager) Counts(ctx context.Context, ticketSeq int64) (map[int64]int64, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT sent_timeline_seq, version FROM drafts
		WHERE ticket_seq = ? AND status = 'sent' AND sent_timeline_seq IS NOT NULL`, ticketSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var seq, count int64
		if err = rows.Scan(&seq, &count); err != nil {
			return nil, err
		}
		out[seq] = count
	}
	return out, rows.Err()
}
