// 指派（#36）：建单时指派与改派共用这里的成员解析；改派记一条 assigned
// 时间线事件，工单从原指派人手中移到新指派人手中（收件箱随推导变化）。
package tickets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// 指派的校验错误；HTTP 层把它们映射成 4xx 错误码。
// ErrAssigneeMustBeMember：指派对象写成了成员 Agent（成员 Agent 不能被指派）。
type AssigneeMustBeMemberError struct{ Ref string }

func (e *AssigneeMustBeMemberError) Error() string {
	return "「" + e.Ref + "」是成员 Agent，不能被指派"
}

// UnknownMemberError：指派对象不是成员（成员 id 和姓名都查不到）。
type UnknownMemberError struct{ Ref string }

func (e *UnknownMemberError) Error() string { return "没有成员「" + e.Ref + "」" }

// AlreadyAssignedError：改派给了当前的指派人。
type AlreadyAssignedError struct{ Name string }

func (e *AlreadyAssignedError) Error() string { return "已经指派给 " + e.Name }

// AssigneeDeactivatedError：指派对象的账号已停用（#42；停用即时生效，
// 停用成员不能再接新工单，历史署名与事件不受影响）。
type AssigneeDeactivatedError struct{ Name string }

func (e *AssigneeDeactivatedError) Error() string {
	return "「" + e.Name + "」的账号已停用，不能被指派"
}

// AssignResult 是改派成功后的投影。
type AssignResult struct {
	Ticket
	WaitingOn WaitingOn
}

// resolveAssignee 把请求里的 assignee（成员 id 或姓名）解析成成员；
// 写成成员 Agent（署名写法或 Agent 名字）返回 ErrAssigneeMustBeMember，
// 查无此人返回 ErrUnknownMember，账号已停用返回 AssigneeDeactivatedError
// （评审修正 #42：停用成员不能再被指派）。空串同样按查无此人处理（创建时
// 不带 assignee 不会走到这里）。
func (m *Manager) resolveAssignee(ctx context.Context, ref string) (int64, string, error) {
	ref = strings.TrimSpace(ref)
	var (
		id          int64
		name        string
		deactivated sql.NullString
	)
	query := `SELECT id, name, deactivated_at FROM members WHERE name = ? OR username = ?`
	args := []any{ref, ref}
	if n, convErr := strconv.ParseInt(ref, 10, 64); convErr == nil {
		query = `SELECT id, name, deactivated_at FROM members WHERE id = ?`
		args = []any{n}
	}
	err := m.db.QueryRowContext(ctx, query, args...).Scan(&id, &name, &deactivated)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return m.rejectAgentRef(ctx, ref)
	case err != nil:
		return 0, "", err
	case deactivated.Valid && deactivated.String != "":
		return 0, "", &AssigneeDeactivatedError{Name: name}
	}
	return id, name, nil
}

// rejectAgentRef 处理 resolveAssignee 查无此人的情形：写成了成员 Agent
// （署名写法「某成员 的 Agent · 名字」或恰好是某个 Agent 的名字）返回
// ErrAssigneeMustBeMember，否则 ErrUnknownMember。
func (m *Manager) rejectAgentRef(ctx context.Context, ref string) (int64, string, error) {
	if strings.Contains(ref, " 的 Agent") {
		return 0, "", &AssigneeMustBeMemberError{Ref: ref}
	}
	var agents int
	if err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_tokens WHERE name = ?`, ref).Scan(&agents); err != nil {
		return 0, "", err
	}
	if agents > 0 {
		return 0, "", &AssigneeMustBeMemberError{Ref: ref}
	}
	return 0, "", &UnknownMemberError{Ref: ref}
}

// MemberNames 列出全部成员的姓名（unknown_member 的 hint 用）。
func (m *Manager) MemberNames(ctx context.Context) []string {
	rows, err := m.db.QueryContext(ctx, `SELECT name FROM members ORDER BY id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil
		}
		names = append(names, n)
	}
	return names
}

// insertAssignedEvent 记一条 assigned 事件：from 为空表示创建时指派
// （文案「指派给 X」），否则是改派（文案「改派：X → Y」）。
func (m *Manager) insertAssignedEvent(ctx context.Context, tx *sql.Tx, ticketSeq int64,
	actor Author, from, to string, at time.Time) (int64, int64, error) {
	eventSeq, change, err := m.appendEvent(ctx, tx, ticketSeq, at, actor, "assigned")
	if err != nil {
		return 0, 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE timeline_events SET assign_from = ?, assign_to = ? WHERE ticket_seq = ? AND seq = ?`,
		nullStr(from), to, ticketSeq, eventSeq); err != nil {
		return 0, 0, fmt.Errorf("写入 assigned 事件的附加字段失败：%w", err)
	}
	return eventSeq, change, touchLastChange(ctx, tx, ticketSeq, change)
}

// Assign 以 viewer 的身份把 ref 指向的工单改派给 assignee（成员 id 或姓名），
// 记一条 assigned{from,to} 事件；改派后原指派人的回复不再参与等待方推导。
func (m *Manager) Assign(ctx context.Context, viewer identity.Viewer, ref, assigneeRef string) (AssignResult, error) {
	full, err := m.ByRef(ctx, ref)
	if err != nil {
		return AssignResult{}, err
	}
	assigneeRef = strings.TrimSpace(assigneeRef)
	var (
		toID   int64
		toName string
	)
	if assigneeRef != "" {
		var err error
		toID, toName, err = m.resolveAssignee(ctx, assigneeRef)
		if err != nil {
			return AssignResult{}, err
		}
	}
	if full.AssigneeID == 0 && toID == 0 {
		return AssignResult{}, &UnknownMemberError{Ref: assigneeRef}
	}
	if toID == full.AssigneeID {
		return AssignResult{}, &AlreadyAssignedError{Name: toName}
	}
	fromName := full.AssigneeName

	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return AssignResult{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE tickets SET assignee_id = ? WHERE seq = ?`, nullInt(toID), full.Seq); err != nil {
		return AssignResult{}, fmt.Errorf("改派失败：%w", err)
	}
	eventSeq, _, err := m.insertAssignedEvent(ctx, tx, full.Seq, authorOf(viewer), fromName, toName, now)
	if err != nil {
		return AssignResult{}, err
	}
	if m.notifier != nil {
		after := full
		after.AssigneeID = toID
		after.AssigneeName = toName
		after.Timeline = append(append([]Event{}, full.Timeline...), Event{
			Seq: eventSeq, Change: full.LastChange + 1, At: now, Actor: authorOf(viewer), Type: "assigned",
			AssignFrom: fromName, AssignTo: toName,
		})
		if err := m.notifier.NotifyTx(ctx, tx, NotificationInput{Before: full, After: after,
			Actor: authorOf(viewer), Event: after.Timeline[len(after.Timeline)-1]}); err != nil {
			return AssignResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return AssignResult{}, err
	}

	after, err := m.ByRef(ctx, ref)
	if err != nil {
		return AssignResult{}, err
	}
	return AssignResult{Ticket: after.Ticket, WaitingOn: WaitingOnOf(after)}, nil
}
