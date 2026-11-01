package tickets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
)

var (
	// ErrCollaboratorNotAllowed 表示当前凭证不能管理这张工单的协作者。
	ErrCollaboratorNotAllowed = errors.New("你不能管理这张工单的协作者")
	// ErrAlreadyCollaborator 表示成员已在协作者名单里。
	ErrAlreadyCollaborator = errors.New("这位成员已在协作者名单里")
	// ErrNotCollaborator 表示成员不在协作者名单里。
	ErrNotCollaborator = errors.New("这位成员不在协作者名单里")
)

// CollaboratorDeactivatedError 表示不能把停用成员加入协作。
type CollaboratorDeactivatedError struct{ Name string }

func (e *CollaboratorDeactivatedError) Error() string {
	return "「" + e.Name + "」的账号已停用，不能被加入协作"
}

// CollaboratorResult 是协作者操作成功后的完整工单投影。
type CollaboratorResult struct {
	Full Full
}

func (m *Manager) collaborators(ctx context.Context, seq int64) ([]Collaborator, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT c.member_id, m.name, m.deactivated_at, c.added_at
		FROM ticket_collaborators c
		JOIN members m ON m.id = c.member_id
		WHERE c.ticket_seq = ? ORDER BY c.added_at, c.member_id`, seq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Collaborator{}
	for rows.Next() {
		var (
			c           Collaborator
			deactivated sql.NullString
			at          string
		)
		if err := rows.Scan(&c.ID, &c.Name, &deactivated, &at); err != nil {
			return nil, err
		}
		c.Deactivated = nullStringSet(deactivated)
		if c.AddedAt, err = time.ParseInLocation(time.RFC3339Nano, at, clock.Beijing); err != nil {
			return nil, fmt.Errorf("解析协作者加入时间 %q 失败：%w", at, err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (m *Manager) resolveCollaborator(ctx context.Context, ref string) (Collaborator, error) {
	id, name, err := m.ResolveMemberRef(ctx, ref)
	if err != nil {
		return Collaborator{}, err
	}
	var deactivated sql.NullString
	if err := m.db.QueryRowContext(ctx,
		`SELECT deactivated_at FROM members WHERE id = ?`, id).Scan(&deactivated); err != nil {
		return Collaborator{}, err
	}
	return Collaborator{ID: id, Name: name, Deactivated: nullStringSet(deactivated)}, nil
}

// resolveCreateCollaborators 解析建单请求里的协作者（抄送）引用：只允许在职
// 成员；按 id 去重并保持请求顺序；创建者与指派人按现有规则不算协作者，
// 直接跳过（指派人被改派走后也不会自动回到协作者名单）。
func (m *Manager) resolveCreateCollaborators(ctx context.Context, refs []string, creatorID, assigneeID int64) ([]Collaborator, error) {
	out := make([]Collaborator, 0, len(refs))
	seen := make(map[int64]bool, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref) == "" {
			continue
		}
		target, err := m.resolveCollaborator(ctx, strings.TrimSpace(ref))
		if err != nil {
			return nil, err
		}
		if target.Deactivated {
			return nil, &CollaboratorDeactivatedError{Name: target.Name}
		}
		if target.ID == creatorID || target.ID == assigneeID || seen[target.ID] {
			continue
		}
		seen[target.ID] = true
		out = append(out, Collaborator{ID: target.ID, Name: target.Name})
	}
	return out, nil
}

// insertInitialCollaboratorsTx 在建单的同一个事务里把初始协作者写入名单，
// 逐个记录 collaborator_added 事件并发出「加入协作」通知（#274）。任何一步
// 失败都随建单一起回滚，不留下建了一半的工单。
func (m *Manager) insertInitialCollaboratorsTx(ctx context.Context, tx *sql.Tx, t Ticket,
	actor Author, targets []Collaborator, at time.Time) ([]int64, error) {
	if len(targets) == 0 {
		return nil, nil
	}
	full := Full{Ticket: t}
	full.Collaborators = []Collaborator{}
	full.Timeline = []Event{}
	var notified []int64
	for _, target := range targets {
		before := full
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO ticket_collaborators (ticket_seq, member_id, added_at) VALUES (?, ?, ?)`,
			t.Seq, target.ID, at.Format(time.RFC3339Nano)); err != nil {
			return nil, fmt.Errorf("加入协作者失败：%w", err)
		}
		event, err := m.insertCollaboratorEvent(ctx, tx, before, actor, target, "collaborator_added", at)
		if err != nil {
			return nil, err
		}
		full.Collaborators = append(full.Collaborators, target)
		full.Timeline = append(append([]Event{}, full.Timeline...), event)
		ids, err := m.notifyCollaboratorEvent(ctx, tx, before, full, actor, event)
		if err != nil {
			return nil, err
		}
		notified = append(notified, ids...)
	}
	return notified, nil
}

// CanManageCollaborators 判断当前成员凭证能否维护这张工单的协作者：
// 管理员、创建者、当前指派人，以及未指派时的有效初筛人。
func (m *Manager) CanManageCollaborators(ctx context.Context, viewer identity.Viewer, full Full) (bool, error) {
	if viewer.Kind != identity.ViewerMember {
		return false, nil
	}
	id := viewer.Member.ID
	isAdmin, err := memberIsAdmin(ctx, m.db, id)
	if err != nil {
		return false, err
	}
	if isAdmin || creatorOrAssignee(id, full) {
		return true, nil
	}
	if full.AssigneeID != 0 {
		return false, nil
	}
	return effectiveTriagerContains(ctx, m.db, id)
}

func (m *Manager) insertCollaboratorEvent(ctx context.Context, tx *sql.Tx, full Full,
	actor Author, target Collaborator, eventType string, at time.Time) (Event, error) {
	seq, change, err := m.appendEvent(ctx, tx, full.Seq, at, actor, eventType)
	if err != nil {
		return Event{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE timeline_events SET collaborator_member_id = ?, collaborator_name = ?
		WHERE ticket_seq = ? AND seq = ?`, target.ID, target.Name, full.Seq, seq); err != nil {
		return Event{}, fmt.Errorf("写入 %s 事件失败：%w", eventType, err)
	}
	if err := touchLastChange(ctx, tx, full.Seq, change); err != nil {
		return Event{}, err
	}
	return Event{Seq: seq, Change: change, At: at, Actor: actor, Type: eventType,
		CollaboratorID: target.ID, Collaborator: target.Name}, nil
}

// AddCollaborator 把成员加入工单协作者名单，并记录 collaborator_added 事件。
func (m *Manager) AddCollaborator(ctx context.Context, viewer identity.Viewer, ref, memberRef string) (CollaboratorResult, error) {
	full, err := m.ForViewer(ctx, viewer, ref)
	if err != nil {
		return CollaboratorResult{}, err
	}
	ok, err := m.CanManageCollaborators(ctx, viewer, full)
	if err != nil {
		return CollaboratorResult{}, err
	}
	if !ok {
		return CollaboratorResult{}, ErrCollaboratorNotAllowed
	}
	target, err := m.resolveCollaborator(ctx, strings.TrimSpace(memberRef))
	if err != nil {
		return CollaboratorResult{}, err
	}
	if target.Deactivated {
		return CollaboratorResult{}, &CollaboratorDeactivatedError{Name: target.Name}
	}
	for _, c := range full.Collaborators {
		if c.ID == target.ID {
			return CollaboratorResult{}, ErrAlreadyCollaborator
		}
	}

	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return CollaboratorResult{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ticket_collaborators (ticket_seq, member_id, added_at) VALUES (?, ?, ?)`,
		full.Seq, target.ID, now.Format(time.RFC3339Nano)); err != nil {
		return CollaboratorResult{}, fmt.Errorf("加入协作者失败：%w", err)
	}
	actor := authorOf(viewer)
	event, err := m.insertCollaboratorEvent(ctx, tx, full, actor, target, "collaborator_added", now)
	if err != nil {
		return CollaboratorResult{}, err
	}
	after := full
	after.Collaborators = append(append([]Collaborator{}, full.Collaborators...), target)
	after.Timeline = append(append([]Event{}, full.Timeline...), event)
	notified, err := m.notifyCollaboratorEvent(ctx, tx, full, after, actor, event)
	if err != nil {
		return CollaboratorResult{}, err
	}
	if err := m.commitAndWake(tx, notified); err != nil {
		return CollaboratorResult{}, err
	}
	if after, err = m.ByRef(ctx, ref); err != nil {
		return CollaboratorResult{}, err
	}
	return CollaboratorResult{Full: after}, nil
}

func (m *Manager) notifyCollaboratorEvent(ctx context.Context, tx *sql.Tx, before, after Full,
	actor Author, event Event) ([]int64, error) {
	if m.notifier == nil {
		return nil, nil
	}
	return m.notifier.NotifyTx(ctx, tx, NotificationInput{Before: before, After: after, Actor: actor, Event: event})
}

func (m *Manager) removeCollaborator(ctx context.Context, viewer identity.Viewer, ref string,
	target Collaborator, eventType string) (CollaboratorResult, error) {
	full, err := m.ForViewer(ctx, viewer, ref)
	if err != nil {
		return CollaboratorResult{}, err
	}
	if target.ID == full.Creator.MemberID || target.ID == full.AssigneeID {
		return CollaboratorResult{}, ErrNotCollaborator
	}
	found := false
	for _, c := range full.Collaborators {
		if c.ID == target.ID {
			found = true
			break
		}
	}
	if !found {
		return CollaboratorResult{}, ErrNotCollaborator
	}

	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return CollaboratorResult{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM ticket_collaborators WHERE ticket_seq = ? AND member_id = ?`, full.Seq, target.ID); err != nil {
		return CollaboratorResult{}, fmt.Errorf("移除协作者失败：%w", err)
	}
	actor := authorOf(viewer)
	event, err := m.insertCollaboratorEvent(ctx, tx, full, actor, target, eventType, now)
	if err != nil {
		return CollaboratorResult{}, err
	}
	after := full
	after.Collaborators = []Collaborator{}
	for _, c := range full.Collaborators {
		if c.ID != target.ID {
			after.Collaborators = append(after.Collaborators, c)
		}
	}
	after.Timeline = append(append([]Event{}, full.Timeline...), event)
	notified, err := m.notifyCollaboratorEvent(ctx, tx, full, after, actor, event)
	if err != nil {
		return CollaboratorResult{}, err
	}
	if err := m.commitAndWake(tx, notified); err != nil {
		return CollaboratorResult{}, err
	}
	if after, err = m.ByRef(ctx, ref); err != nil {
		return CollaboratorResult{}, err
	}
	return CollaboratorResult{Full: after}, nil
}

// RemoveCollaborator 由有管理权限的人移除协作者，并记录 collaborator_removed 事件。
func (m *Manager) RemoveCollaborator(ctx context.Context, viewer identity.Viewer, ref string, memberID int64) (CollaboratorResult, error) {
	full, err := m.ForViewer(ctx, viewer, ref)
	if err != nil {
		return CollaboratorResult{}, err
	}
	ok, err := m.CanManageCollaborators(ctx, viewer, full)
	if err != nil {
		return CollaboratorResult{}, err
	}
	if !ok {
		return CollaboratorResult{}, ErrCollaboratorNotAllowed
	}
	target, err := m.resolveCollaborator(ctx, strconv.FormatInt(memberID, 10))
	if err != nil {
		return CollaboratorResult{}, err
	}
	return m.removeCollaborator(ctx, viewer, ref, target, "collaborator_removed")
}

// LeaveCollaborators 让当前成员退出协作者名单，并记录 collaborator_left 事件。
func (m *Manager) LeaveCollaborators(ctx context.Context, viewer identity.Viewer, ref string) (CollaboratorResult, error) {
	target := Collaborator{ID: viewer.Member.ID, Name: viewer.Member.Name}
	return m.removeCollaborator(ctx, viewer, ref, target, "collaborator_left")
}
