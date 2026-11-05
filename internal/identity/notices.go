package identity

import (
	"context"
	"time"
)

// UnseenWebNotices 返回网页会话应展示的一次性提示。
// 当前只有 drafts_direct_intro（ADR 0017 上线把名下 Agent 改为可直接发）：
// 有未撤销的 Agent 被迁移标记且尚未确认时返回。
func (m *Manager) UnseenWebNotices(ctx context.Context, member Member) ([]string, error) {
	var (
		hasAgent bool
		seen     bool
	)
	err := m.db.QueryRowContext(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM agent_tokens
			        WHERE member_id = ? AND revoked_at IS NULL AND draft_mode_switched_to_direct = 1),
			EXISTS (SELECT 1 FROM member_notice_seen WHERE member_id = ? AND notice = 'drafts_direct_intro')`,
		member.ID, member.ID).Scan(&hasAgent, &seen)
	if err != nil {
		return nil, err
	}
	if !hasAgent || seen {
		return []string{}, nil
	}
	return []string{"drafts_direct_intro"}, nil
}

// MarkWebNoticeSeen 幂等记录成员已看过某个一次性提示。
func (m *Manager) MarkWebNoticeSeen(ctx context.Context, member Member, notice string) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO member_notice_seen (member_id, notice, seen_at)
		VALUES (?, ?, ?)
		ON CONFLICT (member_id, notice) DO NOTHING`,
		member.ID, notice, m.clock.Now().Format(time.RFC3339Nano))
	return err
}
