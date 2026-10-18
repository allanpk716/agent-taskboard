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

const (
	KindTurn = "turn"
	KindFYI  = "fyi"

	ReasonAssigned        = "assigned"
	ReasonDelivered       = "delivered"
	ReasonAssigneeReplied = "assignee_replied"
	ReasonReturned        = "returned"
	ReasonCreatorReplied  = "creator_replied"
	ReasonGuestReplied    = "guest_replied"
	ReasonDepartment      = "department"
	ReasonClosed          = "closed"
	ReasonReopened        = "reopened"
	ReasonUnassigned      = "unassigned"
	ReasonBriefRevised    = "brief_revised"
	ReasonThirdPartyReply = "third_party_reply"
)

type notificationStyle struct {
	reason string
	suffix string
}

var notificationStyles = map[string]notificationStyle{
	tickets.WhyNew:            {ReasonAssigned, " 回复了"},
	tickets.WhyGuestReplied:   {ReasonGuestReplied, " 回复了"},
	tickets.WhyDelivered:      {ReasonDelivered, " 交付了"},
	tickets.WhyAssigneeReply:  {ReasonAssigneeReplied, " 回复了"},
	tickets.WhySentBack:       {ReasonReturned, " 退回"},
	tickets.WhyCreatorReplied: {ReasonCreatorReplied, " 回复了"},
}

// Rules 是成员通知规则模块。它只依据事件前后的工单状态、操作者和有效
// 初筛人推导接收人，等待方本身复用 tickets.WaitingOnOf。
type Rules struct{}

func (Rules) Decide(in tickets.NotificationInput) []Decision {
	out := []Decision{}
	switch in.Event.Type {
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
		} else if turn.MemberID != 0 && !selfAction(in.Actor, turn.MemberID) && oppositeActor(in.After, turn.MemberID, in.Actor) {
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
	case ReasonBriefRevised:
		if in.Actor.MemberID != 0 {
			break
		}
		out = append(out, fyiForStakeholders(in, ReasonBriefRevised,
			notificationTextActor(in.Actor)+" 修订了「"+in.After.Title+"」的说明")...)
		for i := range out {
			if out[i].Reason == ReasonBriefRevised {
				out[i].Summary = "第 " + strconv.FormatInt(in.Event.Revision, 10) + " 版"
			}
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
			now.Format(time.RFC3339Nano)); err != nil {
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
