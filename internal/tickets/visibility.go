package tickets

import (
	"context"
	"fmt"

	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// TicketInvisibleError 表示凭证指向的工单存在，但当前成员不可见。
// HTTP 层用它记录被拒访问；对请求方仍按「不存在」返回。
type TicketInvisibleError struct {
	Seq    int64
	Viewer identity.Viewer
}

func (e *TicketInvisibleError) Error() string {
	return fmt.Sprintf("成员不能查看工单 %d", e.Seq)
}

// GuestLinkScopeError 表示访客链接指向另一张工单。它包住 ErrNotFound，
// 对外仍是不可区分的 404，但 HTTP 层能保留访客链接的专用 hint。
type GuestLinkScopeError struct{ Seq int64 }

func (e *GuestLinkScopeError) Error() string { return ErrNotFound.Error() }

func (e *GuestLinkScopeError) Unwrap() error { return ErrNotFound }

// ForViewer 用请求凭证读取一张工单：先按 ref 找工单，再套访客链接作用域
// 与成员可见范围。查不到、访客跨工单、成员不可见分别返回对应的错误。
// handler 在写操作前调用它是刻意的：写方法内部还会按凭证加载一次，但
// 这层预检保住「不可见 404」先于请求体校验、成员权限等后续响应的顺序。
func (m *Manager) ForViewer(ctx context.Context, viewer identity.Viewer, ref string) (Full, error) {
	full, err := m.ByRef(ctx, ref)
	if err != nil {
		return Full{}, err
	}
	if viewer.Kind == identity.ViewerGuest && viewer.GuestLink.TicketSeq != full.Seq {
		return Full{}, &GuestLinkScopeError{Seq: full.Seq}
	}
	if viewer.Kind == identity.ViewerMember {
		visible, err := m.CanSee(ctx, viewer, full)
		if err != nil {
			return Full{}, err
		}
		if !visible {
			return Full{}, &TicketInvisibleError{Seq: full.Seq, Viewer: viewer}
		}
	}
	return full, nil
}

// CanSee 是成员、成员 Agent 与管理员的统一「能否看到」判断。访客不经过
// 这里：访客链接始终只指向一张工单，由 ForViewer 按 ticket_seq 判定。
func (m *Manager) CanSee(ctx context.Context, viewer identity.Viewer, full Full) (bool, error) {
	if viewer.Kind != identity.ViewerMember {
		return false, fmt.Errorf("可见范围只判断成员凭证")
	}
	return CanSeeMember(ctx, m.db, viewer.Member.ID, full)
}

// CanSeeMember 是通知等没有 Viewer 对象的调用方复用的同一可见范围判断。
func CanSeeMember(ctx context.Context, q Queryer, id int64, full Full) (bool, error) {
	isAdmin, err := memberIsAdmin(ctx, q, id)
	if err != nil {
		return false, err
	}
	if isAdmin {
		return true, nil
	}
	if creatorOrAssignee(id, full) {
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
	return effectiveTriagerContains(ctx, q, id)
}

// VisibleSeqs 是与 CanSee 同一规则的「可见工单集合」查询，供列表、搜索、
// 计数与收件箱取候选工单；调用方不得再拼接各自的可见范围条件。
func (m *Manager) VisibleSeqs(ctx context.Context, viewer identity.Viewer) ([]int64, error) {
	if viewer.Kind != identity.ViewerMember {
		return nil, fmt.Errorf("可见范围只查询成员凭证")
	}
	id := viewer.Member.ID
	// 管理员身份以数据库为准：管理员身份收回后，未过期的网页会话与
	// Agent token 也不能继续看到全量工单。
	isAdmin, err := memberIsAdmin(ctx, m.db, id)
	if err != nil {
		return nil, err
	}
	where := `creator_member_id = ? OR assignee_id = ?
		OR EXISTS (SELECT 1 FROM ticket_collaborators c WHERE c.ticket_seq = tickets.seq AND c.member_id = ?)`
	args := []any{id, id, id}
	if isAdmin {
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

func memberIsAdmin(ctx context.Context, q Queryer, id int64) (bool, error) {
	var isAdmin bool
	err := q.QueryRowContext(ctx, `SELECT is_admin FROM members WHERE id = ?`, id).Scan(&isAdmin)
	return isAdmin, err
}

func creatorOrAssignee(id int64, full Full) bool {
	return id == full.Creator.MemberID || (full.AssigneeID != 0 && id == full.AssigneeID)
}

func effectiveTriagerContains(ctx context.Context, q Queryer, id int64) (bool, error) {
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
