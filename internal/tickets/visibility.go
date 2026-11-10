package tickets

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
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
	if viewer.Kind == identity.ViewerGuest {
		if viewer.Guest != nil {
			// 长期访客的作用域是名下未撤销、未过期的链接集合：同一张工单
			// 有多条有效链接时按权限最大者处理；不在集合内对外仍返回 404。
			// 解析结果写回 Viewer.GuestLink（同一请求内的共享指针），
			// 后续读写沿用单条链接上下文的权限与留痕字段。
			link, statusErr, err := m.guestLinkInScope(ctx, viewer.Guest.ID, full.Seq)
			if err != nil {
				return Full{}, err
			}
			if statusErr != nil {
				return Full{}, statusErr
			}
			if link == nil {
				return Full{}, &GuestLinkScopeError{Seq: full.Seq}
			}
			if viewer.GuestLink != nil {
				viewer.GuestLink.ID = link.ID
				viewer.GuestLink.TicketSeq = link.TicketSeq
				viewer.GuestLink.Note = link.Note
				viewer.GuestLink.GuestID = link.GuestID
				viewer.GuestLink.GuestName = link.GuestName
				viewer.GuestLink.ExpiresAt = link.ExpiresAt
				viewer.GuestLink.CanEditBrief = link.CanEditBrief
				viewer.GuestLink.CredentialHolderID = link.CredentialHolderID
				viewer.GuestLink.ClaimName = viewer.Guest.ClaimName
				viewer.GuestLink.EntryMethod = viewer.Guest.EntryMethod
			}
		} else if viewer.GuestLink.TicketSeq != full.Seq {
			return Full{}, &GuestLinkScopeError{Seq: full.Seq}
		}
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

// guestLinkInScope 找长期访客在这张工单上的最优有效链接。名下只有已失效
// 链接时返回对应的链接状态错误（撤销/过期），从未有链接才交给调用方按 404 处理。
func (m *Manager) guestLinkInScope(ctx context.Context, guestID, ticketSeq int64) (
	*identity.GuestLink, *identity.LinkStatusError, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT g.id, g.ticket_seq, g.note, g.expires_at, g.revoked_at, g.can_edit_brief,
		       g.guest_id, gu.name, g.credential_holder_id
		FROM guest_links g
		JOIN guests gu ON gu.id = g.guest_id
		WHERE g.guest_id = ? AND g.ticket_seq = ? AND gu.disabled_at IS NULL
		ORDER BY g.can_edit_brief DESC, g.id`, guestID, ticketSeq)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	now := m.clk.Now()
	var best *identity.GuestLink
	var dead identity.GuestLink
	revoked, expired := false, false
	for rows.Next() {
		var (
			link      identity.GuestLink
			expiresAt string
			revokedAt sql.NullString
			canEdit   int
			guestName string
		)
		if err := rows.Scan(&link.ID, &link.TicketSeq, &link.Note, &expiresAt, &revokedAt, &canEdit,
			&link.GuestID, &guestName, &link.CredentialHolderID); err != nil {
			return nil, nil, err
		}
		expiry, err := time.ParseInLocation(time.RFC3339Nano, expiresAt, clock.Beijing)
		if err != nil {
			return nil, nil, err
		}
		link.ExpiresAt = expiry
		link.CanEditBrief = canEdit == 1
		link.GuestName = guestName
		if revokedAt.Valid {
			revoked = true
			dead = link
			continue
		}
		if !now.Before(expiry) {
			expired = true
			dead = link
			continue
		}
		best = &link
		break
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if best != nil {
		return best, nil, nil
	}
	if revoked {
		return nil, &identity.LinkStatusError{Link: dead,
			Status: identity.GuestLinkRevoked, Err: identity.ErrLinkRevoked}, nil
	}
	if expired {
		return nil, &identity.LinkStatusError{Link: dead,
			Status: identity.GuestLinkExpired, Err: identity.ErrLinkExpired}, nil
	}
	return nil, nil, nil
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
