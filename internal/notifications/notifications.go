// Package notifications 实现成员通知的规则、存储与读取。
package notifications

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

const maxSummary = 120

// Rules 是成员通知规则模块。它只依据事件前后的工单状态、操作者和有效
// 初筛人推导接收人，等待方本身复用 tickets.WaitingOnOf。
type Rules struct{}

func (Rules) Decide(in tickets.NotificationInput) []Decision {
	out := []Decision{}
	switch in.Event.Type {
	case "created":
		turn := tickets.WaitingOnOf(in.After)
		if turn.Party != "department" {
			break
		}
		for _, id := range in.Triagers {
			if selfAction(in.Actor, id) {
				continue
			}
			out = append(out, Decision{MemberID: id, Kind: "turn", Reason: "department",
				Text: "新工单「" + in.After.Title + "」轮到本部门，待初筛"})
		}
	case "assigned":
		turn := tickets.WaitingOnOf(in.After)
		if turn.Party == "assignee" && turn.MemberID != 0 && !selfAction(in.Actor, turn.MemberID) {
			out = append(out, Decision{
				MemberID: turn.MemberID, Kind: "turn", Reason: "assigned",
				Text: notificationTextActor(in.Actor) + " 把工单「" + in.After.Title + "」指派给你",
			})
		}
		if in.Before.AssigneeID != 0 && in.Before.AssigneeID != in.After.AssigneeID &&
			!selfAction(in.Actor, in.Before.AssigneeID) {
			out = append(out, Decision{
				MemberID: in.Before.AssigneeID, Reason: "unassigned", Kind: "fyi",
				Text: notificationTextActor(in.Actor) + unassignText(in.After, in.Before.AssigneeName),
			})
		}
	case "reply":
		turn := tickets.WaitingOnOf(in.After)
		if turn.Party == "department" && in.Actor.MemberID == 0 {
			for _, id := range in.Triagers {
				if !selfAction(in.Actor, id) && !hasDecisionFor(out, id) {
					out = append(out, Decision{
						MemberID: id, Reason: "department", Kind: "turn",
						Text:    "工单「" + in.After.Title + "」轮到本部门，待初筛",
						Summary: replySummary(in.Event.BodyMD),
					})
				}
			}
		} else if turn.MemberID != 0 && !selfAction(in.Actor, turn.MemberID) && oppositeActor(in.After, turn.MemberID, in.Actor) {
			summary := replySummary(in.Event.BodyMD)
			out = append(out, Decision{
				MemberID: turn.MemberID, Kind: "turn", Reason: notificationReason(turn.Why),
				Text:    notificationTextActor(in.Actor) + replySuffix(turn.Why) + "「" + in.After.Title + "」",
				Summary: summary,
			})
		}
		if in.Actor.MemberID != 0 && in.Actor.MemberID != in.After.Creator.MemberID &&
			in.Actor.MemberID != in.After.AssigneeID {
			out = append(out, fyiForStakeholders(in, "third_party_reply",
				notificationTextActor(in.Actor)+" 回复了「"+in.After.Title+"」")...)
		}
	case "brief_revised":
		if in.Actor.MemberID != 0 {
			break
		}
		out = append(out, fyiForStakeholders(in, "brief_revised",
			notificationTextActor(in.Actor)+" 修订了「"+in.After.Title+"」的说明")...)
		for i := range out {
			if out[i].Reason == "brief_revised" {
				out[i].Summary = "第 " + strconv.FormatInt(in.Event.Revision, 10) + " 版"
			}
		}
	case "closed":
		out = append(out, fyiForStakeholders(in, "closed",
			notificationTextActor(in.Actor)+" 关闭了「"+in.After.Title+"」")...)
	case "reopened":
		out = append(out, fyiForStakeholders(in, "reopened",
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
	out := make([]Decision, 0, 2)
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
	db      *sql.DB
	clk     clock.Clock
	rule    Rules
	streams *streamBroker
}

func NewManager(db *sql.DB, c clock.Clock) *Manager {
	return &Manager{db: db, clk: c, rule: Rules{}, streams: newStreamBroker()}
}

func (m *Manager) NotifyTx(ctx context.Context, tx *sql.Tx, in tickets.NotificationInput) ([]int64, error) {
	created := m.clk.Now()
	if in.Event.Type == "created" || in.Event.Type == "assigned" || in.Event.Type == "reply" {
		triagers, err := tickets.EffectiveTriagers(ctx, tx)
		if err != nil {
			return nil, err
		}
		in.Triagers = triagers
	}
	decisions := m.rule.Decide(in)
	memberIDs := make([]int64, 0, len(decisions))
	for _, d := range decisions {
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
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
			d.MemberID, in.After.Seq, in.After.Number, in.After.Title, d.Kind, d.Reason, d.Text,
			in.Event.Seq, in.Event.Type, actorKind, actorName,
			nullInt(in.Actor.MemberID), nullInt(in.Actor.AgentID), d.Summary,
			created.Format(time.RFC3339Nano)); err != nil {
			return nil, err
		}
		memberIDs = append(memberIDs, d.MemberID)
	}
	return memberIDs, nil
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
