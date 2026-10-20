package tickets

import "context"

// unreadEventTypes 是会产生工单未读点的事件；标签增删与通知规则一致，不算未读。
const unreadEventTypes = `('reply', 'brief_revised', 'assigned', 'closed', 'reopened',
	'collaborator_added', 'collaborator_removed', 'collaborator_left')`

// IsUnread 报告成员在这张可见工单上是否有需要看的新事件。没有个人记录时，
// 从 0026 迁移写入的上线基线开始算。
func (m *Manager) IsUnread(ctx context.Context, memberID, ticketSeq int64) (bool, error) {
	query := `
	SELECT EXISTS (
		SELECT 1
		FROM timeline_events e
		LEFT JOIN ticket_reads r
		  ON r.member_id = ? AND r.ticket_seq = e.ticket_seq
		WHERE e.ticket_seq = ?
		  AND e.change_seq > COALESCE(r.last_read_seq,
		                             (SELECT last_read_seq FROM ticket_read_baseline WHERE id = 1), 0)
		  AND e.type IN ` + unreadEventTypes + `
		  AND (e.actor_member_id IS NULL OR e.actor_member_id <> ? OR e.actor_agent_id IS NOT NULL)
	)`
	var unread bool
	if err := m.db.QueryRowContext(ctx, query, memberID, ticketSeq, memberID, memberID).Scan(&unread); err != nil {
		return false, err
	}
	return unread, nil
}

// MarkRead 把成员在这张工单上的已读游标推进到当前最后一条事件。游标只前进；
// Agent token 调用方不进入这里，因此不会替主人改已读状态。
func (m *Manager) MarkRead(ctx context.Context, memberID, ticketSeq int64) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO ticket_reads (member_id, ticket_seq, last_read_seq)
		VALUES (?, ?, (SELECT COALESCE(MAX(change_seq), 0) FROM timeline_events WHERE ticket_seq = ?))
		ON CONFLICT(member_id, ticket_seq) DO UPDATE SET last_read_seq = MAX(
			last_read_seq,
			excluded.last_read_seq
		)`, memberID, ticketSeq, ticketSeq)
	return err
}
