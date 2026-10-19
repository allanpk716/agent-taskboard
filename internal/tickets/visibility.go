package tickets

import (
	"context"
	"fmt"

	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// CanSee 是成员、成员 Agent 与管理员的统一「能否看到」判断。访客不经过
// 这里：访客链接始终只指向一张工单，由 HTTP 层按 ticket_seq 判定。
func (m *Manager) CanSee(ctx context.Context, viewer identity.Viewer, full Full) (bool, error) {
	if viewer.Kind != identity.ViewerMember {
		return false, fmt.Errorf("可见范围只判断成员凭证")
	}
	return CanSeeMember(ctx, m.db, viewer.Member.ID, full)
}

// CanSeeMember 是通知等没有 Viewer 对象的调用方复用的同一可见范围判断。
func CanSeeMember(ctx context.Context, q Queryer, id int64, full Full) (bool, error) {
	var isAdmin bool
	if err := q.QueryRowContext(ctx, `SELECT is_admin FROM members WHERE id = ?`, id).
		Scan(&isAdmin); err != nil {
		return false, err
	}
	if isAdmin {
		return true, nil
	}
	if id == full.Creator.MemberID || (full.AssigneeID != 0 && id == full.AssigneeID) {
		return true, nil
	}
	for _, c := range full.Collaborators {
		if c.ID == id {
			return true, nil
		}
	}
	if full.AssigneeID != 0 {
		return false, nil
	}
	triagers, err := EffectiveTriagers(ctx, q)
	if err != nil {
		return false, err
	}
	for _, triager := range triagers {
		if triager == id {
			return true, nil
		}
	}
	return false, nil
}

// VisibleSeqs 是与 CanSee 同一规则的「可见工单集合」查询，供列表、搜索、
// 计数与收件箱取候选工单；调用方不得再拼接各自的可见范围条件。
func (m *Manager) VisibleSeqs(ctx context.Context, viewer identity.Viewer) ([]int64, error) {
	if viewer.Kind != identity.ViewerMember {
		return nil, fmt.Errorf("可见范围只查询成员凭证")
	}
	id := viewer.Member.ID
	where := `creator_member_id = ? OR assignee_id = ?
		OR EXISTS (SELECT 1 FROM ticket_collaborators c WHERE c.ticket_seq = tickets.seq AND c.member_id = ?)`
	args := []any{id, id, id}
	if viewer.Member.IsAdmin {
		where = "1 = 1"
		args = nil
	} else {
		triager, err := m.isTriager(ctx, id)
		if err != nil {
			return nil, err
		}
		if triager {
			where += " OR assignee_id IS NULL"
		}
	}
	rows, err := m.db.QueryContext(ctx, `SELECT seq FROM tickets WHERE `+where+` ORDER BY seq`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seqs := []int64{}
	for rows.Next() {
		var seq int64
		if err := rows.Scan(&seq); err != nil {
			return nil, err
		}
		seqs = append(seqs, seq)
	}
	return seqs, rows.Err()
}
