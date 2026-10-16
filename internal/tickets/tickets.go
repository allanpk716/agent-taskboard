// Package tickets 是「工单核心」模块：建单（编号与序号在同一事务内分配）、读取、
// 时间线事件与全局变更序号。等待方从时间线推导，不存会漂移的状态。
// 工单不能删除：本包不提供删除入口，编号与序号因此连续。
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
	"github.com/allanpk716/agent-taskboard/internal/labels"
)

// 建单的输入校验错误；HTTP 层把它们映射成 422 错误码。
var (
	// ErrTitleRequired 表示标题为空。
	ErrTitleRequired = errors.New("标题不能为空")
	// ErrInvalidKind 表示 kind 不是 task 或 report。
	ErrInvalidKind = errors.New("kind 必须是 task 或 report")
	// ErrDimensionLabelRequired 表示一个维度标签都没有。
	ErrDimensionLabelRequired = errors.New("至少要打一个维度标签")
	// ErrNotFound 表示引用的工单不存在（编号或序号都查不到）。
	ErrNotFound = errors.New("没有这张工单")
)

// Author 是统一的作者结构 {member, agent}：本人操作 agent 为空（nil 语义用
// AgentID==0 表示），成员 Agent 的操作带具名 Agent（data-model「作者结构」）。
// 访客作者（MemberID == 0）带链接备注与自报信息（#32）：author_name 与
// via_agent 都是访客自报的、未经平台核实的信息（ADR 0003）。
type Author struct {
	MemberID   int64
	MemberName string
	AgentID    int64
	AgentName  string

	// 访客作者（#32）：经哪条链接操作、链接备注（存当时的原文）、自报署名与
	// 自报「由 Agent 写成」。成员作者的这些字段为零值。
	LinkID     int64
	LinkNote   string
	AuthorName string
	ViaAgent   bool
}

// DisplayName 返回署名：成员「陈某」；成员 Agent「陈某 的 Agent · 名字」（ADR 0003）；
// 访客「张三（经「检验科-张三」链接）」；访客自报由 Agent 写成时写
// 「检验科-张三（访客）的 Agent（自报）」——与平台认证的成员 Agent 明确区分。
func (a Author) DisplayName() string {
	if a.MemberID != 0 {
		if a.AgentID != 0 {
			return a.MemberName + " 的 Agent · " + a.AgentName
		}
		return a.MemberName
	}
	if a.ViaAgent {
		return a.LinkNote + "（访客）的 Agent（自报）"
	}
	name := a.AuthorName
	if name == "" {
		name = "访客"
	}
	return name + "（经「" + a.LinkNote + "」链接）"
}

// Ticket 是一张工单的核心字段。
type Ticket struct {
	Seq            int64
	Number         string
	Kind           string // task | report
	Title          string
	Status         string // open | closed
	CloseReason    string // "" | done | wontfix | duplicate
	DuplicateOfSeq int64  // close_reason == duplicate 时另一张工单的序号（0 = 无）
	BriefMD        string
	BriefRevision  int64
	Creator        Author
	AssigneeID     int64  // 指派人的成员 id；0 = 未指派（#36）
	AssigneeName   string // 指派人的姓名；空 = 未指派
	CreatedAt      time.Time
	LastChange     int64
}

// Event 是时间线上的一条事件，只追加不修改。
type Event struct {
	Seq    int64 // 单内递增
	Change int64 // 全局变更序号（与事件写入同事务推进）
	At     time.Time
	Actor  Author
	Type   string // created | reply | brief_revised | closed | reopened | label_added | …
	Label  string // label_added / label_removed 的标签（#31）

	// 附加字段，按事件类型出现（写入端点在 #30；guest_link 在 #32）。
	BodyMD      string // reply 的正文
	ReplyNo     int64  // reply 在单内的序号
	Deliver     bool   // reply 带「交付」标记（#36 启用）
	Revision    int64  // brief_revised 修订后的版本号
	Reason      string // closed 的原因：done | wontfix | duplicate
	DuplicateOf string // closed=重复时另一张工单的编号（"" = 无）
	Note        string // guest_link_created / guest_link_revoked 的备注（#32）
	LinkExpires string // guest_link_extended 的新到期时刻（RFC3339，#32）
	AssignFrom  string // assigned 事件的改派前指派人；空 = 创建时指派（#36）
	AssignTo    string // assigned 事件的指派对象（#36）
}

// Full 是读取一张工单时的完整投影：核心字段 + 标签 + 完整时间线。
type Full struct {
	Ticket
	Labels   []string
	Timeline []Event
}

// Manager 是工单核心的对外入口：「以某个 Viewer 建单」「读某张工单」。
type Manager struct {
	db     *sql.DB
	clk    clock.Clock
	labels *labels.Manager
}

func NewManager(db *sql.DB, c clock.Clock, lm *labels.Manager) *Manager {
	return &Manager{db: db, clk: c, labels: lm}
}

// Labels 返回标签字典模块（HTTP 层组 hint 用；完整字典接口在 #31）。
func (m *Manager) Labels() *labels.Manager { return m.labels }

// CreateInput 是建单的输入。#29 是不用模板的版本：一整块 brief_md；
// assignee（成员 id 或姓名，#36）与模板 fields（#34）随后续工单加入。
type CreateInput struct {
	Kind     string
	Title    string
	BriefMD  string
	Labels   []string
	Assignee string // 成员 id 或姓名；空 = 不指派（#36）
	Creator  identity.Viewer
}

// Create 校验并创建一张工单。工单编号（北京时间每天从 001 起，超 999 自动 4 位）
// 与全局序号在同一个事务内分配，同时写入 created 事件并推进全局变更序号；
// 建单时指派（#36）会在 created 之后记一条 assigned 事件。返回完整投影
//（含时间线，等待方由它推导）。
func (m *Manager) Create(ctx context.Context, in CreateInput) (Full, error) {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return Full{}, ErrTitleRequired
	}
	switch in.Kind {
	case "":
		in.Kind = "task"
	case "task", "report":
	default:
		return Full{}, ErrInvalidKind
	}
	var (
		assigneeID   int64
		assigneeName string
	)
	if strings.TrimSpace(in.Assignee) != "" {
		var err error
		assigneeID, assigneeName, err = m.resolveAssignee(ctx, in.Assignee)
		if err != nil {
			return Full{}, err
		}
	}
	in.Labels = dedupe(in.Labels)
	hasDimension := false
	for _, l := range in.Labels {
		if strings.Contains(l, ":") {
			hasDimension = true
			break
		}
	}
	if !hasDimension {
		return Full{}, ErrDimensionLabelRequired
	}
	if err := m.labels.Validate(ctx, in.Labels); err != nil {
		return Full{}, err
	}

	now := m.clk.Now()
	date := now.Format("20060102")

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Full{}, err
	}
	defer tx.Rollback()

	// 当日序号：已有的当天工单数 + 1；超过 999 时自动用 4 位。
	var today int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tickets WHERE number LIKE ?`, date+"-%").Scan(&today); err != nil {
		return Full{}, err
	}
	daily := today + 1
	number := date + "-"
	if daily > 999 {
		number += fmt.Sprintf("%d", daily)
	} else {
		number += fmt.Sprintf("%03d", daily)
	}

	var agentID any
	if in.Creator.Agent != nil {
		agentID = in.Creator.Agent.ID
	}
	creator := Author{
		MemberID:   in.Creator.Member.ID,
		MemberName: in.Creator.Member.Name,
	}
	if in.Creator.Agent != nil {
		creator.AgentID = in.Creator.Agent.ID
		creator.AgentName = in.Creator.Agent.Name
	}
	t := Ticket{
		Number:        number,
		Kind:          in.Kind,
		Title:         in.Title,
		BriefMD:       in.BriefMD,
		BriefRevision: 1,
		Status:        "open",
		Creator:       creator,
		AssigneeID:    assigneeID,
		AssigneeName:  assigneeName,
		CreatedAt:     now,
	}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO tickets (number, kind, title, status, brief_md, brief_revision,
		                     creator_member_id, creator_agent_id, assignee_id, created_at, last_change)
		VALUES (?, ?, ?, 'open', ?, 1, ?, ?, ?, ?, 0)
		RETURNING seq`,
		number, in.Kind, in.Title, in.BriefMD, creator.MemberID, agentID,
		nullInt(assigneeID), now.Format(time.RFC3339Nano)).Scan(&t.Seq); err != nil {
		return Full{}, fmt.Errorf("写入工单失败：%w", err)
	}

	// created 事件；建单时指派随后再记一条 assigned{from:null,to}（#36）。
	// 全局变更序号与事件写入同事务推进。
	_, change, err := m.appendEvent(ctx, tx, t.Seq, now, creator, "created")
	if err != nil {
		return Full{}, err
	}
	t.LastChange = change
	if _, err := tx.ExecContext(ctx, `UPDATE tickets SET last_change = ? WHERE seq = ?`, change, t.Seq); err != nil {
		return Full{}, err
	}
	if assigneeID != 0 {
		if err := m.insertAssignedEvent(ctx, tx, t.Seq, creator, "", assigneeName, now); err != nil {
			return Full{}, err
		}
	}
	for _, l := range in.Labels {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO ticket_labels (ticket_seq, label) VALUES (?, ?)`, t.Seq, l); err != nil {
			return Full{}, fmt.Errorf("写入标签 %s 失败：%w", l, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return Full{}, err
	}
	return m.ByRef(ctx, number)
}

// ByRef 用工单编号（YYYYMMDD-NNN）或全局序号读一张工单的完整投影。
// ref 可以带 # 前缀。查不到时返回 ErrNotFound。
func (m *Manager) ByRef(ctx context.Context, ref string) (Full, error) {
	ref = strings.TrimPrefix(ref, "#")
	var (
		t              Ticket
		at             string
		closeReason    sql.NullString
		duplicateOfSeq sql.NullInt64
		memberID       sql.NullInt64
		memberName     sql.NullString
		agentID        sql.NullInt64
		agentName      sql.NullString
		assigneeID     sql.NullInt64
		assigneeName   sql.NullString
		err            error
	)
	scan := func(row *sql.Row) error {
		return row.Scan(&t.Seq, &t.Number, &t.Kind, &t.Title, &t.Status, &closeReason, &duplicateOfSeq,
			&t.BriefMD, &t.BriefRevision, &memberID, &memberName, &agentID, &agentName,
			&assigneeID, &assigneeName, &at, &t.LastChange)
	}
	const cols = `t.seq, t.number, t.kind, t.title, t.status, t.close_reason, t.duplicate_of_seq,
		t.brief_md, t.brief_revision,
		t.creator_member_id, cm.name, t.creator_agent_id, ca.name,
		t.assignee_id, am.name,
		t.created_at, t.last_change`
	from := ` FROM tickets t
		LEFT JOIN members cm ON cm.id = t.creator_member_id
		LEFT JOIN agent_tokens ca ON ca.id = t.creator_agent_id
		LEFT JOIN members am ON am.id = t.assignee_id`
	if strings.Contains(ref, "-") {
		err = scan(m.db.QueryRowContext(ctx, `SELECT `+cols+from+` WHERE t.number = ?`, ref))
	} else {
		seq, convErr := strconv.ParseInt(ref, 10, 64)
		if convErr != nil {
			return Full{}, ErrNotFound
		}
		err = scan(m.db.QueryRowContext(ctx, `SELECT `+cols+from+` WHERE t.seq = ?`, seq))
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Full{}, ErrNotFound
	}
	if err != nil {
		return Full{}, err
	}
	if closeReason.Valid {
		t.CloseReason = closeReason.String
	}
	t.DuplicateOfSeq = duplicateOfSeq.Int64
	t.AssigneeID = assigneeID.Int64
	t.AssigneeName = assigneeName.String
	t.Creator = Author{
		MemberID:   memberID.Int64,
		MemberName: memberName.String,
		AgentID:    agentID.Int64,
		AgentName:  agentName.String,
	}
	t.CreatedAt, err = time.ParseInLocation(time.RFC3339Nano, at, clock.Beijing)
	if err != nil {
		return Full{}, fmt.Errorf("解析创建时间 %q 失败：%w", at, err)
	}

	full := Full{Ticket: t}
	full.Labels, err = m.ticketLabels(ctx, t.Seq)
	if err != nil {
		return Full{}, err
	}
	full.Timeline, err = m.timeline(ctx, t.Seq)
	if err != nil {
		return Full{}, err
	}
	return full, nil
}

func (m *Manager) ticketLabels(ctx context.Context, seq int64) ([]string, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT label FROM ticket_labels WHERE ticket_seq = ? ORDER BY rowid`, seq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (m *Manager) timeline(ctx context.Context, seq int64) ([]Event, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT e.seq, e.change_seq, e.at, e.type, e.actor_member_id, mm.name, e.actor_agent_id, aa.name,
		       e.body_md, e.reply_no, e.deliver, e.revision, e.reason, e.duplicate_of, e.label,
		       e.note, e.link_expires_at, e.assign_from, e.assign_to,
		       e.actor_link_id, e.actor_link_note, e.actor_author_name, e.actor_via_agent
		FROM timeline_events e
		LEFT JOIN members mm ON mm.id = e.actor_member_id
		LEFT JOIN agent_tokens aa ON aa.id = e.actor_agent_id
		WHERE e.ticket_seq = ? ORDER BY e.seq`, seq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var (
			e               Event
			at              string
			actorMemberID   sql.NullInt64
			actorMemberName sql.NullString
			actorAgentID    sql.NullInt64
			actorAgentName  sql.NullString
			bodyMD          sql.NullString
			replyNo         sql.NullInt64
			deliver         int
			revision        sql.NullInt64
			reason          sql.NullString
			duplicateOf     sql.NullString
			label           sql.NullString
			note            sql.NullString
			linkExpiresAt   sql.NullString
			assignFrom      sql.NullString
			assignTo        sql.NullString
			actorLinkID     sql.NullInt64
			actorLinkNote   sql.NullString
			actorAuthorName sql.NullString
			actorViaAgent   int
		)
		if err := rows.Scan(&e.Seq, &e.Change, &at, &e.Type,
			&actorMemberID, &actorMemberName, &actorAgentID, &actorAgentName,
			&bodyMD, &replyNo, &deliver, &revision, &reason, &duplicateOf, &label,
			&note, &linkExpiresAt, &assignFrom, &assignTo,
			&actorLinkID, &actorLinkNote, &actorAuthorName, &actorViaAgent); err != nil {
			return nil, err
		}
		e.Actor = Author{
			MemberID:   actorMemberID.Int64,
			MemberName: actorMemberName.String,
			AgentID:    actorAgentID.Int64,
			AgentName:  actorAgentName.String,
			LinkID:     actorLinkID.Int64,
			LinkNote:   actorLinkNote.String,
			AuthorName: actorAuthorName.String,
			ViaAgent:   actorViaAgent == 1,
		}
		e.BodyMD = bodyMD.String
		e.ReplyNo = replyNo.Int64
		e.Deliver = deliver == 1
		e.Revision = revision.Int64
		e.Reason = reason.String
		e.DuplicateOf = duplicateOf.String
		e.Label = label.String
		e.Note = note.String
		e.LinkExpires = linkExpiresAt.String
		e.AssignFrom = assignFrom.String
		e.AssignTo = assignTo.String
		if e.At, err = time.ParseInLocation(time.RFC3339Nano, at, clock.Beijing); err != nil {
			return nil, fmt.Errorf("解析事件时间 %q 失败：%w", at, err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// WaitingOn 是「等待方」推导结果，由时间线推导、不单独存储。
type WaitingOn struct {
	Party    string // assignee | creator | guest | department | none
	MemberID int64  // 轮到的成员 id；party 不是 assignee/creator 时为 0
	Member   string // 成员名；同上
	Text     string // 中文描述，如「李工（指派人）」
	Reason   string // 原因文案（收件箱与 waiting_on.reason 共用）；未指派只写 尚未指派/访客回复了
	Why      string // 原因码：new|guest_replied|delivered|assignee_replied|sent_back|creator_replied
	SinceSeq int64  // 推导所依据的那条回复的事件 seq（无相关回复时为 0）
}

// 等待方推导的原因码 → 收件箱文案（原型 WHY 表）。
const (
	WhyNew            = "new"              // 新指派给你
	WhyGuestReplied   = "guest_replied"    // 访客回复了
	WhyDelivered      = "delivered"        // 已交付，待你验收
	WhyAssigneeReply  = "assignee_replied" // 指派人回复了，待你答复
	WhySentBack       = "sent_back"        // 发起人退回修改
	WhyCreatorReplied = "creator_replied"  // 发起人回复了
)

// WhyText 把原因码翻成收件箱/等待方文案。
func WhyText(why string) string {
	switch why {
	case WhyNew:
		return "新指派给你"
	case WhyGuestReplied:
		return "访客回复了"
	case WhyDelivered:
		return "已交付，待你验收"
	case WhyAssigneeReply:
		return "指派人回复了，待你答复"
	case WhySentBack:
		return "发起人退回修改"
	case WhyCreatorReplied:
		return "发起人回复了"
	}
	return ""
}

// WaitingOnOf 推导当前轮到谁。规则照抄 prototype/member-handoff 的 turnOf
//（spec「等待方与收件箱」，#37 的查询也复用这一个函数）：
//
//	已关闭                     → 无等待方
//	尚无回复 或 最后是访客回复    → 指派人（未指派 → department）
//	任务 且 创建者是成员 且 创建者≠指派人（“有对手”）：
//	    指派人一方回复            → 创建者（why = delivered 若该回复带 deliver，否则 assignee_replied）
//	    创建者一方回复            → 指派人（why = sent_back 若此前存在指派人的 deliver，否则 creator_replied）
//	    第三位成员回复            → 不改变轮次（不参与相关回复集）
//	其余情况（反馈、自派自）成员回复 → 访客
//	成员 Agent 的回复算其所属成员一方；改派后，原指派人的回复按当前指派人重新计算、不再参与推导
//
// 相关回复集 = type=reply 且（访客 或 无对手 或 actor∈{指派人, 对手}）。
// 未指派轮到本部门时原因只写「尚未指派」或「访客回复了」（prototype/ticket-query
// 的修正：不能写「新指派给你」）。
func WaitingOnOf(t Full) WaitingOn {
	if t.Status == "closed" {
		return WaitingOn{Party: "none", Text: "无（已关闭）"}
	}
	a := t.AssigneeID
	// 对手：任务、创建者是成员、且与指派人不是同一人时，创建者一方就是对手。
	// 未指派时创建者≠指派人也成立，对手仍是创建者（照抄原型 counterpartMember）。
	c := int64(0)
	if t.Kind == "task" && t.Creator.MemberID != 0 && t.Creator.MemberID != a {
		c = t.Creator.MemberID
	}
	var (
		last           *Event
		since          int64
		deliveredPrior bool // last 之前有没有指派人的带交付标记的相关回复
	)
	for i := range t.Timeline {
		e := &t.Timeline[i]
		if e.Type != "reply" {
			continue
		}
		isGuest := e.Actor.MemberID == 0
		if !(isGuest || c == 0 || e.Actor.MemberID == a || e.Actor.MemberID == c) {
			continue // 第三位成员的回复不改变轮次
		}
		if last != nil && last.Deliver && last.Actor.MemberID == a && a != 0 {
			deliveredPrior = true
		}
		last = e
		since = e.Seq
	}
	toAssignee := func(why string) WaitingOn {
		if a == 0 {
			// 未指派：原因只写 尚未指派（why=new/creator_replied）或 访客回复了。
			reason := "尚未指派"
			if why == WhyGuestReplied {
				reason = "访客回复了"
			}
			return WaitingOn{Party: "department", Text: "本部门（未指派）", Reason: reason, Why: why, SinceSeq: since}
		}
		return WaitingOn{Party: "assignee", MemberID: a, Member: t.AssigneeName,
			Text: t.AssigneeName + "（指派人）", Reason: WhyText(why), Why: why, SinceSeq: since}
	}
	if last == nil {
		return toAssignee(WhyNew)
	}
	if last.Actor.MemberID == 0 {
		return toAssignee(WhyGuestReplied)
	}
	if c == 0 {
		return WaitingOn{Party: "guest", Text: "访客", SinceSeq: since}
	}
	if last.Actor.MemberID == a {
		why := WhyAssigneeReply
		if last.Deliver {
			why = WhyDelivered
		}
		return WaitingOn{Party: "creator", MemberID: c, Member: t.Creator.MemberName,
			Text: t.Creator.MemberName + "（发起人）", Reason: WhyText(why), Why: why, SinceSeq: since}
	}
	// 最后一条相关回复来自创建者一方。
	why := WhyCreatorReplied
	if deliveredPrior {
		why = WhySentBack
	}
	return toAssignee(why)
}

// dedupe 去重并保持顺序。
func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
