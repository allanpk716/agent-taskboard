// 收件箱（#36）：列出「现在轮到我」的开启工单。收件箱属于成员——同一成员
// 名下所有 Agent 共用一份；等待方与原因都由时间线推导（WaitingOnOf），
// 不存会漂移的状态。轮到本部门的未指派工单只进入有效初筛人的收件箱。
package tickets

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// InboxItem 是收件箱的一项：轮到我的工单 + 原因 + 增量读取地址。
type InboxItem struct {
	Number     string
	Title      string
	Role       string // assignee | creator | triager（我在这张工单里的角色）
	Reason     string // WHY 文案：新指派给你 / 已交付，待你验收 / …
	ReasonCode string // 稳定原因码；部门轮次统一 pending_triage
	SinceSeq   int64  // 推导所依据的回复事件 seq；0 = 无相关回复（新指派）
	ReadNew    string // /api/v1/tickets/<编号>/timeline?after=<我在该单最后一条事件的 seq>
	ReadFull   string // /api/v1/tickets/<编号>?format=markdown
	Kind       string
	Creator    string
	Assignee   string
	Labels     []string
	Waiting    string
	Who        string
	UpdatedAt  time.Time
	// Assignee：当前指派人摘要。账号停用时保留指派并带标记（#115）。
	AssigneeID          int64
	AssigneeName        string
	AssigneeDeactivated bool
	Unread              bool
}

// Inbox 列出 memberID 当前轮到的全部开启工单（按工单序号）。
// read_new 的 after 是我在该单最后一条任意事件（不只回复）的 seq——
// 收件箱项要读的是「上次我看过之后别人做了什么」。
func (m *Manager) Inbox(ctx context.Context, viewer identity.Viewer) ([]InboxItem, error) {
	memberID := viewer.Member.ID
	triager, err := m.isTriager(ctx, memberID)
	if err != nil {
		return nil, err
	}
	seqs, err := m.VisibleSeqs(ctx, viewer)
	if err != nil {
		return nil, err
	}
	items := []InboxItem{}
	for _, seq := range seqs {
		full, err := m.ByRef(ctx, strconv.FormatInt(seq, 10))
		if err != nil {
			return nil, err
		}
		turn, err := m.WaitingOn(ctx, full)
		if err != nil {
			return nil, err
		}
		isTurn := turn.MemberID == memberID
		isDepartmentTurn := triager && turn.Party == "department"
		if !isTurn && !isDepartmentTurn {
			continue
		}
		after := int64(0)
		for _, e := range full.Timeline {
			if e.Actor.MemberID == memberID && e.Seq > after {
				after = e.Seq
			}
		}
		role := "creator"
		if turn.Party == "assignee" {
			role = "assignee"
		} else if isDepartmentTurn {
			role = "triager"
		}
		reason := WhyText(turn.Why)
		reasonCode := turn.Why
		if isDepartmentTurn && (turn.Why == WhyNew || turn.Why == WhyGuestReplied) {
			reason = WhyText(WhyPendingTriage)
			reasonCode = WhyPendingTriage
		}
		var updated time.Time
		for _, e := range full.Timeline {
			if e.At.After(updated) {
				updated = e.At
			}
		}
		unread, err := m.IsUnread(ctx, memberID, full.Seq)
		if err != nil {
			return nil, err
		}
		items = append(items, InboxItem{
			Number:              full.Number,
			Title:               full.Title,
			Role:                role,
			Reason:              reason,
			ReasonCode:          reasonCode,
			SinceSeq:            turn.SinceSeq,
			ReadNew:             "/api/v1/tickets/" + full.Number + "/timeline?after=" + strconv.FormatInt(after, 10),
			ReadFull:            "/api/v1/tickets/" + full.Number + "?format=markdown",
			Kind:                full.Kind,
			Creator:             full.Creator.DisplayName(),
			Assignee:            full.AssigneeName,
			Labels:              full.Labels,
			Waiting:             turn.Text,
			Who:                 turn.Member,
			UpdatedAt:           updated,
			AssigneeID:          full.AssigneeID,
			AssigneeName:        full.AssigneeName,
			AssigneeDeactivated: full.AssigneeDeactivated,
			Unread:              unread,
		})
	}
	return items, nil
}

// isTriager 判定成员当前是否有效初筛人：有启用中的显式初筛人时只有这些
// 成员生效；一个都没有时所有管理员兜底（#75）。
func (m *Manager) isTriager(ctx context.Context, memberID int64) (bool, error) {
	ids, err := EffectiveTriagers(ctx, m.db)
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		if id == memberID {
			return true, nil
		}
	}
	return false, nil
}

// Queryer 是读取初筛人所需的最小 SQL 接口；db 与写事务都能提供。
type Queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// EffectiveTriagers 返回当前有效的初筛人：有启用中的显式初筛人时只有这些
// 成员；一个都没有时所有管理员兜底（#75）。
func EffectiveTriagers(ctx context.Context, q Queryer) ([]int64, error) {
	var explicit int
	if err := q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM members WHERE triager = 1 AND deactivated_at IS NULL`).Scan(&explicit); err != nil {
		return nil, err
	}
	column := "triager"
	if explicit == 0 {
		column = "is_admin"
	}
	rows, err := q.QueryContext(ctx, `SELECT id FROM members WHERE `+column+` = 1 AND deactivated_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
