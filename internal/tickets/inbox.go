// 收件箱（#36）：列出「现在轮到我」的开启工单。收件箱属于成员——同一成员
// 名下所有 Agent 共用一份；等待方与原因都由时间线推导（WaitingOnOf），
// 不存会漂移的状态。轮到本部门的未指派工单只进入有效初筛人的收件箱。
package tickets

import (
	"context"
	"database/sql"
	"sort"
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
	Trigger    InboxActor
	// LastReplyAt 是最后一条回复的时间；访客修订说明视作一次回复。没有回复时为 nil。
	LastReplyAt *time.Time
	CreatedAt   time.Time
	Counterpart InboxCounterpart
	// Assignee：当前指派人摘要。账号停用时保留指派并带标记（#115）。
	AssigneeID          int64
	AssigneeName        string
	AssigneeDeactivated bool
	Unread              bool
}

// InboxActor 是让工单进入收件箱的一方。member_agent 的 Name 保留
// 「成员 的 Agent · 名字」署名，Member/Agent ID 供接口结构化返回。
type InboxActor struct {
	Type    string
	Name    string
	Member  *Author
	AgentID int64
	Agent   string
	// Action 只在 reason_code=new 时出现：assigned=首次指派，reassigned=改派。
	Action string
}

// InboxCounterpart 是按 GLOSSARY「往来对象」归组的结果；pending_triage
// 不指向任何人，self 表示成员自派自。
type InboxCounterpart struct {
	Type     string // member | guest | self | pending_triage
	Name     string
	MemberID int64
}

// Inbox 列出 memberID 当前轮到的全部开启工单，按最后回复时间倒序。
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
		links, err := GuestLinksForTicket(ctx, m.db, full.Seq)
		if err != nil {
			return nil, err
		}
		turn := WaitingOnOf(full, links, m.clk.Now())
		isTurn := turn.MemberID == memberID
		isDepartmentTurn := triager && turn.Party == "department"
		if !isTurn && !isDepartmentTurn {
			continue
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
		item, err := m.inboxSummary(ctx, memberID, full, turn, links, role, reason, reasonCode)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	sortInboxItems(items)
	return items, nil
}

// WaitingCounterparts 列出当前成员作为创建者或指派人、当前轮到往来对象
// 一方的开启工单。它们只供「按人」视图参考，不属于收件箱，调用方不能把
// 数量并入 Inbox 的 count。
func (m *Manager) WaitingCounterparts(ctx context.Context, viewer identity.Viewer) ([]InboxItem, error) {
	memberID := viewer.Member.ID
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
		if full.Status != "open" {
			continue
		}
		links, err := GuestLinksForTicket(ctx, m.db, full.Seq)
		if err != nil {
			return nil, err
		}
		turn := WaitingOnOf(full, links, m.clk.Now())
		if !waitingOnCounterpart(memberID, full, turn, links) {
			continue
		}
		role := "assignee"
		if full.Creator.MemberID == memberID {
			role = "creator"
		}
		item, err := m.inboxSummary(ctx, memberID, full, turn, links, role, turn.Reason, turn.Why)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	sortInboxItems(items)
	return items, nil
}

func (m *Manager) inboxSummary(ctx context.Context, memberID int64, full Full, turn WaitingOn,
	links []GuestLinkInfo, role, reason, reasonCode string) (InboxItem, error) {
	after := int64(0)
	var updated time.Time
	var lastReplyAt *time.Time
	for _, e := range full.Timeline {
		if e.Actor.MemberID == memberID && e.Seq > after {
			after = e.Seq
		}
		if e.At.After(updated) {
			updated = e.At
		}
		if e.Type == "reply" || (e.Type == "brief_revised" && e.Actor.MemberID == 0) {
			at := e.At
			lastReplyAt = &at
		}
	}
	unread, err := m.IsUnread(ctx, memberID, full.Seq)
	if err != nil {
		return InboxItem{}, err
	}
	return InboxItem{
		Number: full.Number, Title: full.Title, Role: role,
		Reason: reason, ReasonCode: reasonCode, SinceSeq: turn.SinceSeq,
		ReadNew:  "/api/v1/tickets/" + full.Number + "/timeline?after=" + strconv.FormatInt(after, 10),
		ReadFull: "/api/v1/tickets/" + full.Number + "?format=markdown",
		Kind:     full.Kind, Creator: full.Creator.DisplayName(), Assignee: full.AssigneeName,
		Labels: full.Labels, Waiting: turn.Text, Who: turn.Member, UpdatedAt: updated,
		Trigger: inboxTriggerOf(full, turn, links, reasonCode), LastReplyAt: lastReplyAt,
		CreatedAt: full.CreatedAt, Counterpart: inboxCounterpartOf(memberID, full, links),
		AssigneeID: full.AssigneeID, AssigneeName: full.AssigneeName,
		AssigneeDeactivated: full.AssigneeDeactivated, Unread: unread,
	}, nil
}

func sortInboxItems(items []InboxItem) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i].CreatedAt, items[j].CreatedAt
		if items[i].LastReplyAt != nil {
			left = *items[i].LastReplyAt
		}
		if items[j].LastReplyAt != nil {
			right = *items[j].LastReplyAt
		}
		return left.After(right)
	})
}

func waitingOnCounterpart(memberID int64, full Full, turn WaitingOn, links []GuestLinkInfo) bool {
	isCreator := full.Creator.MemberID == memberID
	isAssignee := full.AssigneeID == memberID
	if !isCreator && !isAssignee {
		return false
	}
	counterpart := inboxCounterpartOf(memberID, full, links)
	if counterpart.Type == "guest" {
		return turn.Party == "guest"
	}
	if counterpart.Type != "member" {
		return false
	}
	if isCreator && turn.Party == "assignee" && turn.MemberID == full.AssigneeID {
		return true
	}
	return isAssignee && turn.Party == "creator" && turn.MemberID == full.Creator.MemberID
}

func inboxTriggerOf(full Full, turn WaitingOn, links []GuestLinkInfo, reasonCode string) InboxActor {
	var assigned *Event
	var causal *Event
	var created *Event
	for i := range full.Timeline {
		e := &full.Timeline[i]
		if e.Type == "created" && created == nil {
			created = e
		}
		if e.Type == "assigned" {
			assigned = e
		}
		if e.Seq == turn.SinceSeq && (e.Type == "reply" ||
			(e.Type == "brief_revised" && e.Actor.MemberID == 0)) {
			causal = e
		}
	}
	switch {
	// 待初筛的一句话固定写「创建者 新建，等你初筛」；即使推导依据落在
	// 某条回复上（SinceSeq），触发者也不换成回复人。
	case reasonCode == WhyPendingTriage:
		if created != nil {
			return inboxActorOf(created.Actor)
		}
		return inboxActorOf(full.Creator)
	case turn.Why == WhyGuestNotCompleted:
		return inboxGuestActor(full, links)
	case assigned != nil && (turn.SinceSeq == 0 || assigned.Seq > turn.SinceSeq):
		actor := inboxActorOf(assigned.Actor)
		if reasonCode == WhyNew {
			// assigned 事件的 AssignFrom 记录改派前指派人；为空表示此前没有
			// 指派人（建单时指派，或从取消指派状态再次指派）。
			if assigned.AssignFrom != "" {
				actor.Action = "reassigned"
			} else {
				actor.Action = "assigned"
			}
		}
		return actor
	case causal != nil:
		return inboxActorOf(causal.Actor)
	case assigned != nil:
		return inboxActorOf(assigned.Actor)
	default:
		for i := range full.Timeline {
			if full.Timeline[i].Type == "created" {
				return inboxActorOf(full.Timeline[i].Actor)
			}
		}
		return inboxActorOf(full.Creator)
	}
}

func inboxActorOf(a Author) InboxActor {
	out := InboxActor{}
	switch {
	case a.MemberID != 0 && a.AgentID != 0:
		out.Type, out.Name, out.AgentID, out.Agent = "member_agent", a.DisplayName(), a.AgentID, a.AgentName
		member := a
		member.AgentID, member.AgentName, member.AgentRevoked = 0, "", false
		out.Member = &member
	case a.MemberID != 0:
		out.Type, out.Name = "member", a.MemberName
		member := a
		out.Member = &member
	default:
		out.Type = "guest"
		out.Name = guestDisplayName(a)
	}
	return out
}

func inboxGuestActor(full Full, links []GuestLinkInfo) InboxActor {
	if full.Creator.MemberID == 0 {
		return inboxActorOf(full.Creator)
	}
	for _, link := range links {
		if !link.CanEditBrief {
			continue
		}
		name := link.ClaimName
		if name == "" {
			name = link.Note
		}
		if name != "" {
			return InboxActor{Type: "guest", Name: name}
		}
	}
	return InboxActor{Type: "guest", Name: "访客"}
}

func guestDisplayName(a Author) string {
	if a.ViaAgent {
		return a.DisplayName()
	}
	if a.AuthorName != "" {
		return a.AuthorName
	}
	if a.LinkNote != "" {
		return a.LinkNote
	}
	return "访客"
}

func inboxCounterpartOf(memberID int64, full Full, links []GuestLinkInfo) InboxCounterpart {
	if full.AssigneeID == 0 {
		return InboxCounterpart{Type: "pending_triage"}
	}
	if full.Kind == "report" && (full.Creator.MemberID == 0 || full.CreatorLinkID != 0 || len(links) > 0) {
		return InboxCounterpart{Type: "guest", Name: guestCounterpartName(full, links)}
	}
	if full.Creator.MemberID == memberID && full.AssigneeID == memberID {
		return InboxCounterpart{Type: "self", Name: full.Creator.MemberName, MemberID: memberID}
	}
	if full.AssigneeID == memberID {
		return InboxCounterpart{Type: "member", Name: full.Creator.MemberName, MemberID: full.Creator.MemberID}
	}
	if full.Creator.MemberID == memberID {
		return InboxCounterpart{Type: "member", Name: full.AssigneeName, MemberID: full.AssigneeID}
	}
	return InboxCounterpart{Type: "pending_triage"}
}

func guestCounterpartName(full Full, links []GuestLinkInfo) string {
	// 归属只看身份：固定取最早发出的那条访客链接（GuestLinksForTicket 按 id
	// 升序返回），不随后来的访客回复跳动。展示名优先用这位访客最近一次自报
	// 的称呼；没有就退回认领称呼/链接备注，再没有才是「访客」。
	if len(links) > 0 {
		link := links[0]
		for i := len(full.Timeline) - 1; i >= 0; i-- {
			e := full.Timeline[i]
			if e.Actor.MemberID != 0 || e.Actor.LinkID != link.ID {
				continue
			}
			if e.Type == "reply" || e.Type == "brief_revised" {
				if name := guestDisplayName(e.Actor); name != "访客" {
					return name
				}
			}
		}
		name := link.ClaimName
		if name == "" {
			name = link.Note
		}
		if name != "" {
			return name
		}
		return "访客"
	}
	// 没有访客链接时（老数据/访客直接创建），才按创建者署名识别。
	if full.Creator.MemberID == 0 {
		if name := guestDisplayName(full.Creator); name != "访客" {
			return name
		}
	}
	return "访客"
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
