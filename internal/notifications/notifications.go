// Package notifications 实现成员通知的规则、存储与读取。
package notifications

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

const maxSummary = 120

// Decision 是一条事件应生成的接收人与原因。
type Decision struct {
	MemberID int64
	Reason   string
	Text     string
	Summary  string
}

// TurnRules 是「轮到我」通知规则模块。它只依据事件前后的工单状态和操作者
// 推导接收人，等待方本身复用 tickets.WaitingOnOf。
type TurnRules struct{}

func (TurnRules) Decide(in tickets.TurnNotificationInput) []Decision {
	switch in.Event.Type {
	case "assigned":
		turn := tickets.WaitingOnOf(in.After)
		if turn.Party != "assignee" || turn.MemberID == 0 {
			return nil
		}
		if selfAction(in.Actor, turn.MemberID) {
			return nil
		}
		return []Decision{{
			MemberID: turn.MemberID, Reason: "assigned",
			Text: notificationTextActor(in.Actor) + " 把工单「" + in.After.Title + "」指派给你",
		}}
	case "reply":
		turn := tickets.WaitingOnOf(in.After)
		if turn.MemberID == 0 || selfAction(in.Actor, turn.MemberID) {
			return nil
		}
		if !oppositeActor(in.After, turn.MemberID, in.Actor) {
			return nil
		}
		summary := strings.TrimSpace(in.Event.BodyMD)
		if runes := []rune(summary); len(runes) > maxSummary {
			summary = string(runes[:maxSummary])
		}
		return []Decision{{
			MemberID: turn.MemberID, Reason: notificationReason(turn.Why),
			Text:    notificationTextActor(in.Actor) + replySuffix(turn.Why) + "「" + in.After.Title + "」",
			Summary: summary,
		}}
	}
	return nil
}

func selfAction(actor tickets.Author, memberID int64) bool {
	return actor.AgentID == 0 && actor.MemberID == memberID
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
	switch why {
	case tickets.WhyNew:
		return "assigned"
	case tickets.WhyGuestReplied:
		return "guest_replied"
	case tickets.WhyDelivered:
		return "delivered"
	case tickets.WhyAssigneeReply:
		return "assignee_replied"
	case tickets.WhySentBack:
		return "returned"
	case tickets.WhyCreatorReplied:
		return "creator_replied"
	}
	return ""
}

func replySuffix(why string) string {
	switch notificationReason(why) {
	case "delivered":
		return " 交付了"
	case "returned":
		return " 退回"
	default:
		return " 回复了"
	}
}

// Manager 提供通知写入（由工单事务调用）与读取。
type Manager struct {
	db   *sql.DB
	clk  clock.Clock
	rule TurnRules
}

func NewManager(db *sql.DB, c clock.Clock) *Manager {
	return &Manager{db: db, clk: c, rule: TurnRules{}}
}

func (m *Manager) NotifyTx(ctx context.Context, tx *sql.Tx, in tickets.TurnNotificationInput) error {
	created := m.clk.Now()
	for _, d := range m.rule.Decide(in) {
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
				summary, created_at, read_at
			) VALUES (?, ?, ?, ?, 'turn', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
			d.MemberID, in.After.Seq, in.After.Number, in.After.Title, d.Reason, d.Text,
			in.Event.Seq, in.Event.Type, actorKind, actorName,
			nullInt(in.Actor.MemberID), nullInt(in.Actor.AgentID), d.Summary,
			created.Format(time.RFC3339Nano)); err != nil {
			return err
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
