// Package notifications 实现成员通知的规则、存储与读取。
package notifications

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

const maxSummary = 120

const (
	KindTurn = "turn"
	KindFYI  = "fyi"

	ReasonAssigned            = "assigned"
	ReasonDelivered           = "delivered"
	ReasonAssigneeReplied     = "assignee_replied"
	ReasonReturned            = "returned"
	ReasonCreatorReplied      = "creator_replied"
	ReasonAgentReplied        = "agent_replied"
	ReasonGuestReplied        = "guest_replied"
	ReasonGuestCompleted      = "guest_completed"
	ReasonGuestRevised        = "guest_revised"
	ReasonDepartment          = "department"
	ReasonClosed              = "closed"
	ReasonReopened            = "reopened"
	ReasonUnassigned          = "unassigned"
	ReasonBriefRevised        = "brief_revised"
	ReasonThirdPartyReply     = "third_party_reply"
	ReasonCollaboratorAdded   = "collaborator_added"
	ReasonCollaboratorRemoved = "collaborator_removed"
	ReasonCollaboratorReplied = "collaborator_replied"
	ReasonBackupFailing       = "backup_failing"
	ReasonBackupRecovered     = "backup_recovered"
	ReasonDraftPending        = "draft_pending"
)

type notificationStyle struct {
	reason string
	suffix string
}

var notificationStyles = map[string]notificationStyle{
	tickets.WhyNew:            {ReasonAssigned, " 回复了"},
	tickets.WhyGuestReplied:   {ReasonGuestReplied, " 回复了"},
	tickets.WhyGuestCompleted: {ReasonGuestCompleted, " 补全了说明"},
	tickets.WhyGuestRevised:   {ReasonGuestRevised, " 修订了说明"},
	tickets.WhyDelivered:      {ReasonDelivered, " 交付了"},
	tickets.WhyAssigneeReply:  {ReasonAssigneeReplied, " 回复了"},
	tickets.WhySentBack:       {ReasonReturned, " 退回"},
	tickets.WhyCreatorReplied: {ReasonCreatorReplied, " 回复了"},
	tickets.WhyAgentReplied:   {ReasonAgentReplied, " 回复了"},
}

// Rules 是成员通知规则模块。它只依据事件前后的工单状态、操作者和有效
// 初筛人推导接收人，等待方本身复用 tickets.WaitingOnOf。
type Rules struct{}

func (Rules) Decide(in tickets.NotificationInput) []Decision {
	out := []Decision{}
	switch in.Event.Type {
	case "collaborator_added":
		if in.Event.CollaboratorID != 0 && !selfAction(in.Actor, in.Event.CollaboratorID) {
			out = append(out, Decision{
				MemberID: in.Event.CollaboratorID, Kind: KindFYI, Reason: ReasonCollaboratorAdded,
				Text: notificationTextActor(in.Actor) + " 把你加入了工单「" + in.After.Title + "」的协作",
			})
		}
	case "collaborator_removed":
		if in.Event.CollaboratorID != 0 && !selfAction(in.Actor, in.Event.CollaboratorID) {
			out = append(out, Decision{
				MemberID: in.Event.CollaboratorID, Kind: KindFYI, Reason: ReasonCollaboratorRemoved,
				Text: notificationTextActor(in.Actor) + " 把你移出了工单「" + in.After.Title + "」的协作",
			})
		}
	case "created":
		turn := tickets.WaitingOnOf(in.After, in.GuestLinks, in.Now)
		if turn.Party != "department" {
			break
		}
		for _, id := range in.Triagers {
			if selfAction(in.Actor, id) {
				continue
			}
			out = append(out, Decision{MemberID: id, Kind: KindTurn, Reason: ReasonDepartment,
				Text: "新工单「" + in.After.Title + "」轮到本部门，待初筛"})
		}
	case ReasonAssigned:
		turn := tickets.WaitingOnOf(in.After, in.GuestLinks, in.Now)
		if turn.Party == "assignee" && turn.MemberID != 0 && !selfAction(in.Actor, turn.MemberID) {
			out = append(out, Decision{
				MemberID: turn.MemberID, Kind: KindTurn, Reason: ReasonAssigned,
				Text: notificationTextActor(in.Actor) + " 把工单「" + in.After.Title + "」指派给你",
			})
		}
		if in.Before.AssigneeID != 0 && in.Before.AssigneeID != in.After.AssigneeID &&
			!selfAction(in.Actor, in.Before.AssigneeID) {
			out = append(out, Decision{
				MemberID: in.Before.AssigneeID, Reason: ReasonUnassigned, Kind: KindFYI,
				Text: notificationTextActor(in.Actor) + unassignText(in.After, in.Before.AssigneeName),
			})
		}
	case "reply":
		turn := tickets.WaitingOnOf(in.After, in.GuestLinks, in.Now)
		if turn.Party == "department" && in.Actor.MemberID == 0 {
			for _, id := range in.Triagers {
				if !selfAction(in.Actor, id) && !hasDecisionFor(out, id) {
					out = append(out, Decision{
						MemberID: id, Reason: ReasonDepartment, Kind: KindTurn,
						Text:    "工单「" + in.After.Title + "」轮到本部门，待初筛",
						Summary: replySummary(in.Event.BodyMD),
					})
				}
			}
		} else if turn.MemberID != 0 && !selfAction(in.Actor, turn.MemberID) &&
			(oppositeActor(in.After, turn.MemberID, in.Actor) ||
				(turn.Why == tickets.WhyAgentReplied && in.Actor.AgentID != 0 && in.Actor.MemberID == turn.MemberID)) {
			summary := replySummary(in.Event.BodyMD)
			out = append(out, Decision{
				MemberID: turn.MemberID, Kind: KindTurn, Reason: notificationReason(turn.Why),
				Text:    notificationTextActor(in.Actor) + replySuffix(turn.Why) + "「" + in.After.Title + "」",
				Summary: summary,
			})
		}
		if in.Actor.MemberID != 0 && in.Actor.MemberID != in.After.Creator.MemberID &&
			in.Actor.MemberID != in.After.AssigneeID {
			out = append(out, fyiForStakeholders(in, ReasonThirdPartyReply,
				notificationTextActor(in.Actor)+" 回复了「"+in.After.Title+"」")...)
		}
		for _, c := range in.After.Collaborators {
			if selfAction(in.Actor, c.ID) || hasDecisionFor(out, c.ID) {
				continue
			}
			out = append(out, Decision{
				MemberID: c.ID, Kind: KindFYI, Reason: ReasonCollaboratorReplied,
				Text:    notificationTextActor(in.Actor) + " 回复了「" + in.After.Title + "」",
				Summary: replySummary(in.Event.BodyMD),
			})
		}
	case ReasonBriefRevised:
		if in.Actor.MemberID != 0 {
			break
		}
		turn := tickets.WaitingOnOf(in.After, in.GuestLinks, in.Now)
		if turn.Party == "department" {
			for _, id := range in.Triagers {
				if !hasDecisionFor(out, id) {
					out = append(out, Decision{
						MemberID: id,
						Kind:     KindTurn,
						Reason:   ReasonDepartment,
						Text:     "工单「" + in.After.Title + "」轮到本部门，待初筛",
					})
				}
			}
		} else if turn.MemberID != 0 && !hasDecisionFor(out, turn.MemberID) {
			out = append(out, Decision{
				MemberID: turn.MemberID, Kind: KindTurn, Reason: notificationReason(turn.Why),
				Text: notificationTextActor(in.Actor) + replySuffix(turn.Why) + "「" + in.After.Title + "」",
			})
		}
		fyiText := notificationTextActor(in.Actor) + " 修订了「" + in.After.Title + "」的说明"
		for _, id := range []int64{in.After.Creator.MemberID, in.After.AssigneeID} {
			if id == 0 || hasDecisionFor(out, id) {
				continue
			}
			out = append(out, Decision{MemberID: id, Kind: KindFYI, Reason: ReasonBriefRevised, Text: fyiText})
		}
		for i := range out {
			out[i].Summary = "第 " + strconv.FormatInt(in.Event.Revision, 10) + " 版"
		}
	case ReasonClosed:
		out = append(out, fyiForStakeholders(in, ReasonClosed,
			notificationTextActor(in.Actor)+" 关闭了「"+in.After.Title+"」")...)
	case ReasonReopened:
		out = append(out, fyiForStakeholders(in, ReasonReopened,
			notificationTextActor(in.Actor)+" 重新开启了「"+in.After.Title+"」")...)
	}
	return out
}

type Decision struct {
	MemberID int64
	Kind     string
	Reason   string
	Text     string
	Summary  string
}

func selfAction(actor tickets.Author, memberID int64) bool {
	return actor.AgentID == 0 && actor.MemberID == memberID
}

func unassignText(after tickets.Full, from string) string {
	if after.AssigneeID == 0 {
		return " 取消了「" + after.Title + "」给你的指派"
	}
	return " 把工单「" + after.Title + "」从 " + from + " 改派给 " + after.AssigneeName
}

func fyiForStakeholders(in tickets.NotificationInput, reason, text string) []Decision {
	ids := []int64{in.After.Creator.MemberID, in.After.AssigneeID}
	for _, c := range in.After.Collaborators {
		ids = append(ids, c.ID)
	}
	out := make([]Decision, 0, len(ids))
	for _, id := range ids {
		if id == 0 || selfAction(in.Actor, id) || hasDecisionFor(out, id) {
			continue
		}
		out = append(out, Decision{MemberID: id, Kind: "fyi", Reason: reason, Text: text,
			Summary: replySummary(in.Event.BodyMD)})
	}
	return out
}

func hasDecisionFor(in []Decision, memberID int64) bool {
	for _, d := range in {
		if d.MemberID == memberID {
			return true
		}
	}
	return false
}

func replySummary(body string) string {
	summary := strings.TrimSpace(body)
	if runes := []rune(summary); len(runes) > maxSummary {
		return string(runes[:maxSummary])
	}
	return summary
}

func oppositeActor(t tickets.Full, recipient int64, actor tickets.Author) bool {
	if actor.MemberID == 0 {
		return true
	}
	other := t.Creator.MemberID
	if recipient == t.Creator.MemberID {
		other = t.AssigneeID
	}
	return actor.MemberID == other
}

func notificationReason(why string) string {
	return notificationStyles[why].reason
}

func replySuffix(why string) string {
	return notificationStyles[why].suffix
}

// Manager 提供通知写入（由工单事务调用）与读取。
type Manager struct {
	db      *sql.DB
	clk     clock.Clock
	rule    Rules
	streams *streamBroker
}

func NewManager(db *sql.DB, c clock.Clock) *Manager {
	return &Manager{db: db, clk: c, rule: Rules{}, streams: newStreamBroker()}
}

func (m *Manager) NotifyTx(ctx context.Context, tx *sql.Tx, in tickets.NotificationInput) ([]int64, error) {
	now := m.clk.Now()
	in.Now = now
	if in.After.Seq != 0 {
		links, err := tickets.GuestLinksForTicket(ctx, tx, in.After.Seq)
		if err != nil {
			return nil, err
		}
		in.GuestLinks = links
	}
	if in.Event.Type == "created" || in.Event.Type == "assigned" || in.Event.Type == "reply" ||
		in.Event.Type == ReasonBriefRevised {
		triagers, err := tickets.EffectiveTriagers(ctx, tx)
		if err != nil {
			return nil, err
		}
		in.Triagers = triagers
	}
	decisions := m.rule.Decide(in)
	memberIDs := make([]int64, 0, len(decisions))
	for _, d := range decisions {
		if d.Reason != ReasonUnassigned && d.Reason != ReasonCollaboratorRemoved {
			visible, err := tickets.CanSeeMember(ctx, tx, d.MemberID, in.After)
			if err != nil {
				return nil, err
			}
			if !visible {
				continue
			}
		}
		actorKind := "member"
		if in.Actor.MemberID == 0 {
			actorKind = "guest"
		} else if in.Actor.AgentID != 0 {
			actorKind = "agent"
		}
		actorName := notificationActorName(in.Actor)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO notifications (
				member_id, ticket_seq, ticket_number, ticket_title, kind, reason, text,
				trigger_seq, trigger_type, actor_kind, actor_name, actor_member_id, actor_agent_id,
				summary, url, created_at, read_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
			d.MemberID, in.After.Seq, in.After.Number, in.After.Title, d.Kind, d.Reason, d.Text,
			in.Event.Seq, in.Event.Type, actorKind, actorName,
			nullInt(in.Actor.MemberID), nullInt(in.Actor.AgentID), d.Summary,
			"/t/"+in.After.Number, now.Format(time.RFC3339Nano)); err != nil {
			return nil, err
		}
		memberIDs = append(memberIDs, d.MemberID)
	}
	return memberIDs, nil
}

type DraftNotificationInput struct {
	MemberID        int64
	TicketSeq       int64
	TicketNumber    string
	TicketTitle     string
	Text            string
	BaseTimelineSeq int64
	AgentID         int64
	AgentName       string
	URL             string
}

// NotifyDraftTx 随草稿首次写成插入一条通知。草稿通知挂工单，但不进入未读。
func (m *Manager) NotifyDraftTx(ctx context.Context, tx *sql.Tx, in DraftNotificationInput) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO notifications (
			member_id, ticket_seq, ticket_number, ticket_title, kind, reason, text,
			trigger_seq, trigger_type, actor_kind, actor_name, actor_member_id, actor_agent_id,
			url, created_at, read_at
		) VALUES (?, ?, ?, ?, 'draft', ?, ?, ?, 'draft', 'agent', ?, ?, ?, ?, ?, NULL)`,
		in.MemberID, in.TicketSeq, in.TicketNumber, in.TicketTitle, ReasonDraftPending, in.Text,
		in.BaseTimelineSeq, in.AgentName, in.MemberID, in.AgentID, in.URL,
		m.clk.Now().Format(time.RFC3339Nano))
	return err
}

func (m *Manager) DraftNotifier() DraftSink { return draftSink{m} }

type DraftSink interface {
	NotifyDraftTx(ctx context.Context, tx *sql.Tx, memberID, ticketSeq int64, number, title, text string,
		baseTimelineSeq, agentID int64, agentName, url string) error
	Wake(memberIDs []int64)
}

type draftSink struct{ m *Manager }

func (s draftSink) NotifyDraftTx(ctx context.Context, tx *sql.Tx, memberID, ticketSeq int64,
	number, title, text string, baseTimelineSeq, agentID int64, agentName, url string) error {
	return s.m.NotifyDraftTx(ctx, tx, DraftNotificationInput{
		MemberID: memberID, TicketSeq: ticketSeq, TicketNumber: number, TicketTitle: title,
		Text: text, BaseTimelineSeq: baseTimelineSeq, AgentID: agentID, AgentName: agentName, URL: url,
	})
}

func (s draftSink) Wake(memberIDs []int64) { s.m.Wake(memberIDs) }

// NotifySystem 把一条不挂工单的系统通知发给全部在职管理员，并唤醒桌面通知流。
func (m *Manager) NotifySystem(ctx context.Context, reason, text string) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM members WHERE is_admin = 1 AND deactivated_at IS NULL ORDER BY id`)
	if err != nil {
		return err
	}
	var memberIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		memberIDs = append(memberIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	now := m.clk.Now().Format(time.RFC3339Nano)
	for _, id := range memberIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO notifications (
				member_id, kind, reason, text, url, created_at, read_at
			) VALUES (?, 'fyi', ?, ?, '/admin#backup', ?, NULL)`,
			id, reason, text, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	m.Wake(memberIDs)
	return nil
}

// LastBackupAlertAt 实现 ops.BackupNotifier 的持久化告警节奏查询。
func (m *Manager) LastBackupAlertAt(ctx context.Context, since time.Time) (time.Time, bool, error) {
	return m.LastSystemAlertAt(ctx, ReasonBackupFailing, since)
}

// NotifyBackupFailing 发第一次失败告警，文案包含最近成功时间和是否已兜底。
func (m *Manager) NotifyBackupFailing(ctx context.Context, startedAt time.Time, lastSuccessAt *time.Time, fallback bool, cause string) error {
	last := "从未"
	if lastSuccessAt != nil {
		last = lastSuccessAt.Format("2006-01-02 15:04")
	}
	yes := "否"
	if fallback {
		yes = "是"
	}
	return m.NotifySystem(ctx, ReasonBackupFailing,
		"备份失败："+summarizeBackupCause(cause)+"。最近一次成功备到目标："+last+"；已在本机兜底："+yes)
}

// NotifyBackupStillFailing 发 24 小时周期提醒。
func (m *Manager) NotifyBackupStillFailing(ctx context.Context, startedAt time.Time, hours int) error {
	return m.NotifySystem(ctx, ReasonBackupFailing,
		"备份仍未恢复，已持续 "+strconv.Itoa(hours)+" 小时")
}

// NotifyBackupRecovered 发恢复通知并收掉旧告警。
func (m *Manager) NotifyBackupRecovered(ctx context.Context, startedAt time.Time, text string) error {
	return m.RecoverBackupAlerts(ctx, startedAt, text)
}

func summarizeBackupCause(cause string) string {
	cause = strings.TrimSpace(cause)
	if runes := []rune(cause); len(runes) > 160 {
		return string(runes[:160]) + "…"
	}
	return cause
}

// LastSystemAlertAt 返回 reason 系统通知的最新时间；用于服务重启后继续备份告警节奏。
func (m *Manager) LastSystemAlertAt(ctx context.Context, reason string, since time.Time) (time.Time, bool, error) {
	var at string
	err := m.db.QueryRowContext(ctx, `
		SELECT created_at FROM notifications
		WHERE reason = ? AND created_at >= ?
		ORDER BY created_at DESC, id DESC LIMIT 1`,
		reason, since.Format(time.RFC3339Nano)).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	t, err := time.ParseInLocation(time.RFC3339Nano, at, clock.Beijing)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("解析系统通知时间 %q 失败：%w", at, err)
	}
	return t, true, nil
}

// RecoverBackupAlerts 发送恢复通知，并把本轮失败告警标为已读。
func (m *Manager) RecoverBackupAlerts(ctx context.Context, since time.Time, text string) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM members WHERE is_admin = 1 AND deactivated_at IS NULL ORDER BY id`)
	if err != nil {
		return err
	}
	var memberIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		memberIDs = append(memberIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	now := m.clk.Now()
	for _, id := range memberIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO notifications (
				member_id, kind, reason, text, url, created_at, read_at
			) VALUES (?, 'fyi', ?, ?, '/admin#backup', ?, NULL)`,
			id, ReasonBackupRecovered, text, now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	type readResult struct {
		ids    []int64
		unread int
	}
	reads := make(map[int64]readResult, len(memberIDs))
	for _, id := range memberIDs {
		var result readResult
		markRows, err := tx.QueryContext(ctx, `
			SELECT id FROM notifications
			WHERE member_id = ? AND reason = ? AND read_at IS NULL AND created_at >= ? ORDER BY id`,
			id, ReasonBackupFailing, since.Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		for markRows.Next() {
			var notificationID int64
			if err := markRows.Scan(&notificationID); err != nil {
				markRows.Close()
				return err
			}
			result.ids = append(result.ids, notificationID)
		}
		if err := markRows.Err(); err != nil {
			markRows.Close()
			return err
		}
		markRows.Close()
		if len(result.ids) > 0 {
			if _, err := tx.ExecContext(ctx, `
				UPDATE notifications SET read_at = ? WHERE member_id = ? AND reason = ? AND read_at IS NULL AND created_at >= ?`,
				now.Format(time.RFC3339Nano), id, ReasonBackupFailing, since.Format(time.RFC3339Nano)); err != nil {
				return err
			}
		}
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM notifications WHERE member_id = ? AND read_at IS NULL`, id,
		).Scan(&result.unread); err != nil {
			return err
		}
		reads[id] = result
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	m.Wake(memberIDs)
	for id, result := range reads {
		if len(result.ids) > 0 {
			m.WakeRead(id, MarkReadResult{IDs: result.ids, UnreadCount: result.unread})
		}
	}
	return nil
}

func nullInt(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

func notificationActorName(actor tickets.Author) string {
	if actor.MemberID != 0 {
		if actor.AgentID != 0 {
			return actor.AgentName
		}
		return actor.MemberName
	}
	if actor.AuthorName != "" {
		return actor.AuthorName
	}
	return actor.LinkNote
}

func notificationTextActor(actor tickets.Author) string {
	if actor.MemberID != 0 && actor.AgentID != 0 {
		return actor.MemberName + " 的 Agent · " + actor.AgentName
	}
	return notificationActorName(actor)
}
