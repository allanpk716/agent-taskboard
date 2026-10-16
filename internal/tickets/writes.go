// 写操作（#30）：回复、说明修订、关闭与重新开启。都在一个事务里写时间线事件
// 并推进全局变更序号；时间线只追加，工单不可删除。
package tickets

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/template"
)

// 写操作的校验与冲突错误；HTTP 层把它们映射成 4xx 错误码。
var (
	// ErrEmptyReply 表示回复既没有正文也没有附件。
	ErrEmptyReply = errors.New("回复需要正文或至少一个附件")
	// ErrNotBriefOwner 表示当前凭证不是创建者一方（本人或其 Agent）。
	ErrNotBriefOwner = errors.New("只有创建者能修订说明")
	// ErrBaseRevisionRequired 表示修订说明没有带 base_revision。
	ErrBaseRevisionRequired = errors.New("修订说明需要带上 base_revision")
	// ErrInvalidCloseReason 表示关闭原因不是 done/wontfix/duplicate。
	ErrInvalidCloseReason = errors.New("关闭原因必须是 done、wontfix 或 duplicate 之一")
	// ErrDuplicateOfRequired 表示原因 duplicate 时缺 duplicate_of。
	ErrDuplicateOfRequired = errors.New("原因为重复时需要提供 duplicate_of")
	// ErrDuplicateNotFound 表示 duplicate_of 指向的工单不存在。
	ErrDuplicateNotFound = errors.New("duplicate_of 指向的工单不存在")
	// ErrAlreadyClosed 表示工单已经关闭，不能再次关闭。
	ErrAlreadyClosed = errors.New("工单已经关闭")
	// ErrNotClosed 表示工单没有关闭，谈不上重新开启。
	ErrNotClosed = errors.New("工单没有关闭")
)

// DeliverAssigneeOnlyError 是回复带了交付标记但当前凭证不是指派人一方
//（403 deliver_assignee_only；工单已关闭时同样不能交付）。
type DeliverAssigneeOnlyError struct {
	Closed   bool
	Assignee string // 当前指派人姓名；空 = 未指派
}

func (e *DeliverAssigneeOnlyError) Error() string {
	if e.Closed {
		return "工单已关闭，不能交付"
	}
	who := e.Assignee
	if who == "" {
		who = "未指派"
	}
	return "只有指派人（" + who + "）一方能交付"
}

// RevisionConflictError 是 base_revision 乐观锁冲突（409），带当前版本号。
type RevisionConflictError struct {
	BaseRevision    int64
	CurrentRevision int64
}

func (e *RevisionConflictError) Error() string {
	return fmt.Sprintf("说明已被改到第 %d 版，你基于的是第 %d 版", e.CurrentRevision, e.BaseRevision)
}

// ReplyInput 是回复的输入。附件引用（attachment_ids 与正文里的 att:）在 #33 加入；
// AuthorName / ViaAgent 是访客的自报信息（#32，仅访客凭证采纳）；
// Deliver 是交付标记，仅指派人一方可用（#36）。
type ReplyInput struct {
	BodyMD     string
	Deliver    bool   // 交付标记，仅指派人一方可用（#36）
	AuthorName string // 访客自报的署名
	ViaAgent   bool   // 访客自报「由其 Agent 写成」
}

// ReplyResult 是回复成功后的投影。
type ReplyResult struct {
	ReplyNo     int64
	TimelineSeq int64 // 本条回复事件的 seq
	AuthorName  string
	WaitingOn   WaitingOn
}

// Reply 以 viewer 的身份在 ref 指向的工单上回复一条。成员（含成员 Agent）
// 在关闭的工单上仍可回复——那不会重开它，重开是显式动作；访客链接在
// 工单关闭后只读，重开后恢复可回复（spec「凭证与访问」）。访客的回复带
// 链接备注与自报信息；成员的自报字段不采纳（署名由凭证决定，ADR 0003）。
func (m *Manager) Reply(ctx context.Context, viewer identity.Viewer, ref string, in ReplyInput) (ReplyResult, error) {
	full, err := m.ByRef(ctx, ref)
	if err != nil {
		return ReplyResult{}, err
	}
	if err := guestScope(viewer, full.Seq); err != nil {
		return ReplyResult{}, err
	}
	if viewer.Kind == identity.ViewerGuest {
		if full.Status != "open" {
			return ReplyResult{}, ErrTicketClosed
		}
	}
	if in.Deliver {
		// 交付标记只限指派人一方（本人或其 Agent）；交付不改变工单状态，
		// 由发起人验收后关闭。
		if full.Status != "open" || full.AssigneeID == 0 || viewer.Member.ID != full.AssigneeID {
			return ReplyResult{}, &DeliverAssigneeOnlyError{
				Closed: full.Status != "open", Assignee: full.AssigneeName,
			}
		}
	}
	if strings.TrimSpace(in.BodyMD) == "" {
		return ReplyResult{}, ErrEmptyReply
	}

	now := m.clk.Now()
	author := authorOf(viewer)
	if viewer.Kind == identity.ViewerGuest {
		author = guestAuthorOf(viewer, in.AuthorName, in.ViaAgent)
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return ReplyResult{}, err
	}
	defer tx.Rollback()

	var replies int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM timeline_events WHERE ticket_seq = ? AND type = 'reply'`, full.Seq).
		Scan(&replies); err != nil {
		return ReplyResult{}, err
	}
	replyNo := int64(replies) + 1
	eventSeq, change, err := m.appendEvent(ctx, tx, full.Seq, now, author, "reply")
	if err != nil {
		return ReplyResult{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE timeline_events SET body_md = ?, reply_no = ?, deliver = ? WHERE ticket_seq = ? AND seq = ?`,
		in.BodyMD, replyNo, boolToInt(in.Deliver), full.Seq, eventSeq); err != nil {
		return ReplyResult{}, fmt.Errorf("写入回复失败：%w", err)
	}
	if err := touchLastChange(ctx, tx, full.Seq, change); err != nil {
		return ReplyResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ReplyResult{}, err
	}

	after, err := m.ByRef(ctx, ref)
	if err != nil {
		return ReplyResult{}, err
	}
	return ReplyResult{
		ReplyNo:     replyNo,
		TimelineSeq: eventSeq,
		AuthorName:  author.DisplayName(),
		WaitingOn:   WaitingOnOf(after),
	}, nil
}

// ReviseInput 是修订说明的输入。只需传改动的部分：title、brief_md（非模板工单）
// 与 fields（模板工单，只传改动的字段）都可省（空修订也推进版本，行为与
// title-only 修订一致）。修订不做必填校验；AuthorName / ViaAgent 是访客的
// 自报署名（#32，仅访客凭证采纳）。
type ReviseInput struct {
	Title        *string
	BriefMD      *string
	Fields       map[string]string // 模板工单：只需传改动的字段
	BaseRevision int64             // 缺省时为 0，视为没带
	AuthorName   string            // 访客自报的署名（仅访客凭证采纳）
	ViaAgent     bool              // 访客自报「由其 Agent 写成」
}

// Revise 以 viewer 的身份修订标题/说明：校验 base_revision 乐观锁、推进版本号、
// 记一条 brief_revised 事件。可编辑的人：创建者一方（本人或其任意 Agent）；
// 持「可填写说明」链接的访客（工单需开启；无权限的链接 403 link_cannot_edit_brief）。
// 反馈入口提交者（#35）也视同创建者，届时在这里加入。
func (m *Manager) Revise(ctx context.Context, viewer identity.Viewer, ref string, in ReviseInput) (Ticket, error) {
	full, err := m.ByRef(ctx, ref)
	if err != nil {
		return Ticket{}, err
	}
	if err := guestScope(viewer, full.Seq); err != nil {
		return Ticket{}, err
	}
	author := authorOf(viewer)
	if viewer.Kind == identity.ViewerGuest {
		if full.Status != "open" {
			return Ticket{}, ErrTicketClosed
		}
		if !viewer.GuestLink.CanEditBrief {
			return Ticket{}, ErrLinkCannotEditBrief
		}
		author = guestAuthorOf(viewer, in.AuthorName, in.ViaAgent)
	} else if viewer.Member.ID != full.Creator.MemberID {
		return Ticket{}, ErrNotBriefOwner
	}
	if in.BaseRevision == 0 {
		return Ticket{}, ErrBaseRevisionRequired
	}
	if in.BaseRevision != full.BriefRevision {
		return Ticket{}, &RevisionConflictError{BaseRevision: in.BaseRevision, CurrentRevision: full.BriefRevision}
	}
	title := full.Title
	if in.Title != nil {
		title = strings.TrimSpace(*in.Title)
		if title == "" {
			return Ticket{}, ErrTitleRequired
		}
	}
	brief := full.BriefMD
	if in.BriefMD != nil {
		// 模板工单的说明由字段拼成，不接受整块 brief_md（反过来非模板工单
		// 不接受 fields），两类修订按工单类型互斥。
		if full.TemplateDef != nil {
			return Ticket{}, ErrBriefMDWithTemplate
		}
		brief = *in.BriefMD
	}
	fields := full.Fields
	if in.Fields != nil {
		if full.TemplateDef == nil {
			return Ticket{}, ErrFieldsWithoutTemplate
		}
		// 按创建时版本的字段定义校验（模板改版不影响已有工单）；
		// 维度字段的值仍须是字典里在用的值（「不确定」除外）。
		if err := validateFieldsAgainst(full.TemplateDef.Fields, in.Fields); err != nil {
			return Ticket{}, err
		}
		for _, f := range full.TemplateDef.Fields {
			if f.Type != template.TypeDimension {
				continue
			}
			v := strings.TrimSpace(in.Fields[f.Key])
			if v == "" || v == template.UnsureOption {
				continue
			}
			if err := m.labels.Validate(ctx, []string{f.Dimension + ":" + v}); err != nil {
				return Ticket{}, err
			}
		}
		fields = make(map[string]string, len(full.Fields)+len(in.Fields))
		for k, v := range full.Fields {
			fields[k] = v
		}
		for k, v := range in.Fields {
			fields[k] = v
		}
		brief = template.RenderBrief(fields, full.TemplateDef.Fields)
	}

	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Ticket{}, err
	}
	defer tx.Rollback()

	revision := full.BriefRevision + 1
	var fieldsJSON any
	if in.Fields != nil {
		if buf, err := json.Marshal(fields); err != nil {
			return Ticket{}, fmt.Errorf("序列化字段值失败：%w", err)
		} else {
			fieldsJSON = string(buf)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE tickets SET title = ?, brief_md = ?, fields_json = ?, brief_revision = ? WHERE seq = ?`,
			title, brief, fieldsJSON, revision, full.Seq); err != nil {
			return Ticket{}, fmt.Errorf("写入修订失败：%w", err)
		}
	} else if _, err := tx.ExecContext(ctx,
		`UPDATE tickets SET title = ?, brief_md = ?, brief_revision = ? WHERE seq = ?`,
		title, brief, revision, full.Seq); err != nil {
		return Ticket{}, fmt.Errorf("写入修订失败：%w", err)
	}
	eventSeq, change, err := m.appendEvent(ctx, tx, full.Seq, now, author, "brief_revised")
	if err != nil {
		return Ticket{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE timeline_events SET revision = ? WHERE ticket_seq = ? AND seq = ?`,
		revision, full.Seq, eventSeq); err != nil {
		return Ticket{}, fmt.Errorf("写入修订事件失败：%w", err)
	}
	if err := touchLastChange(ctx, tx, full.Seq, change); err != nil {
		return Ticket{}, err
	}
	if err := tx.Commit(); err != nil {
		return Ticket{}, err
	}

	full.Title = title
	full.BriefMD = brief
	full.Fields = fields
	full.BriefRevision = revision
	full.LastChange = change
	return full.Ticket, nil
}

// CloseInput 是关闭工单的输入。DuplicateOf 是原工单的引用（编号或序号，可带 #）。
type CloseInput struct {
	Reason      string // done | wontfix | duplicate
	DuplicateOf string // reason == duplicate 时必填
}

// Close 以 viewer 的身份关闭工单。任何成员都可关闭；duplicate 必须指出与哪张重复。
func (m *Manager) Close(ctx context.Context, viewer identity.Viewer, ref string, in CloseInput) (Ticket, error) {
	full, err := m.ByRef(ctx, ref)
	if err != nil {
		return Ticket{}, err
	}
	if full.Status != "open" {
		return Ticket{}, ErrAlreadyClosed
	}
	switch in.Reason {
	case "done", "wontfix":
	case "duplicate":
		if strings.TrimSpace(in.DuplicateOf) == "" {
			return Ticket{}, ErrDuplicateOfRequired
		}
	default:
		return Ticket{}, ErrInvalidCloseReason
	}
	var duplicateSeq int64
	duplicateNumber := ""
	if in.Reason == "duplicate" {
		duplicateSeq, duplicateNumber, err = m.seqByRef(ctx, in.DuplicateOf)
		if err != nil {
			return Ticket{}, err
		}
	}

	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Ticket{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE tickets SET status = 'closed', close_reason = ?, duplicate_of_seq = ? WHERE seq = ?`,
		in.Reason, nullInt(duplicateSeq), full.Seq); err != nil {
		return Ticket{}, fmt.Errorf("关闭工单失败：%w", err)
	}
	eventSeq, change, err := m.appendEvent(ctx, tx, full.Seq, now, authorOf(viewer), "closed")
	if err != nil {
		return Ticket{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE timeline_events SET reason = ?, duplicate_of = ? WHERE ticket_seq = ? AND seq = ?`,
		in.Reason, duplicateNumber, full.Seq, eventSeq); err != nil {
		return Ticket{}, fmt.Errorf("写入关闭事件失败：%w", err)
	}
	if err := touchLastChange(ctx, tx, full.Seq, change); err != nil {
		return Ticket{}, err
	}
	if err := tx.Commit(); err != nil {
		return Ticket{}, err
	}

	full.Status = "closed"
	full.CloseReason = in.Reason
	full.LastChange = change
	full.DuplicateOfSeq = duplicateSeq
	return full.Ticket, nil
}

// Reopen 以 viewer 的身份重新开启已关闭的工单；任何成员都可重开。
func (m *Manager) Reopen(ctx context.Context, viewer identity.Viewer, ref string) (Full, error) {
	full, err := m.ByRef(ctx, ref)
	if err != nil {
		return Full{}, err
	}
	if full.Status != "closed" {
		return Full{}, ErrNotClosed
	}

	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Full{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE tickets SET status = 'open', close_reason = NULL, duplicate_of_seq = NULL WHERE seq = ?`,
		full.Seq); err != nil {
		return Full{}, fmt.Errorf("重新开启工单失败：%w", err)
	}
	if _, change, err := m.appendEvent(ctx, tx, full.Seq, now, authorOf(viewer), "reopened"); err != nil {
		return Full{}, err
	} else if err := touchLastChange(ctx, tx, full.Seq, change); err != nil {
		return Full{}, err
	}
	if err := tx.Commit(); err != nil {
		return Full{}, err
	}

	// 重开后的等待方按最后一条相关回复推导（spec：重开后照常推导）。
	return m.ByRef(ctx, ref)
}

// seqByRef 把工单引用（编号或序号，可带 # 前缀）解析成（序号, 编号）。
func (m *Manager) seqByRef(ctx context.Context, ref string) (int64, string, error) {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "#")
	if ref == "" {
		return 0, "", ErrNotFound
	}
	var (
		seq    int64
		number string
		err    error
	)
	if strings.Contains(ref, "-") {
		err = m.db.QueryRowContext(ctx, `SELECT seq, number FROM tickets WHERE number = ?`, ref).Scan(&seq, &number)
	} else if n, convErr := strconv.ParseInt(ref, 10, 64); convErr == nil {
		err = m.db.QueryRowContext(ctx, `SELECT seq, number FROM tickets WHERE seq = ?`, n).Scan(&seq, &number)
	} else {
		return 0, "", ErrNotFound
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", ErrNotFound
	}
	return seq, number, err
}

// BySeq 按全局序号读一张工单的核心字段（关闭响应回显 duplicate_of 用）。
func (m *Manager) BySeq(ctx context.Context, seq int64) (Ticket, error) {
	full, err := m.ByRef(ctx, strconv.FormatInt(seq, 10))
	if err != nil {
		return Ticket{}, err
	}
	return full.Ticket, nil
}

// appendEvent 追加一条时间线事件：分配单内 seq 与全局变更序号（同事务）。
// 附加字段随后用 UPDATE 补上（各类型列不同，避免拼 SQL）；访客作者的
// 链接备注与自报信息在这里直接写入（#32）。
func (m *Manager) appendEvent(ctx context.Context, tx *sql.Tx, ticketSeq int64, at time.Time, actor Author, typ string) (seq, change int64, err error) {
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), 0) + 1 FROM timeline_events WHERE ticket_seq = ?`, ticketSeq).Scan(&seq); err != nil {
		return 0, 0, err
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(change_seq), 0) + 1 FROM timeline_events`).Scan(&change); err != nil {
		return 0, 0, err
	}
	var agentID any
	if actor.AgentID != 0 {
		agentID = actor.AgentID
	}
	var memberID any
	if actor.MemberID != 0 {
		memberID = actor.MemberID
	}
	var linkID any
	if actor.LinkID != 0 {
		linkID = actor.LinkID
	}
	var authorName any
	if actor.AuthorName != "" {
		authorName = actor.AuthorName
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO timeline_events (ticket_seq, seq, change_seq, type, actor_member_id, actor_agent_id, at,
		                            actor_link_id, actor_link_note, actor_author_name, actor_via_agent)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ticketSeq, seq, change, typ, memberID, agentID, at.Format(time.RFC3339Nano),
		linkID, actor.LinkNote, authorName, boolToInt(actor.ViaAgent)); err != nil {
		return 0, 0, fmt.Errorf("写入 %s 事件失败：%w", typ, err)
	}
	return seq, change, nil
}

// touchLastChange 在同一事务里推进工单的 last_change（变更序号与事件同事务）。
func touchLastChange(ctx context.Context, tx *sql.Tx, ticketSeq, change int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE tickets SET last_change = ? WHERE seq = ?`, change, ticketSeq)
	return err
}

// authorOf 把 Viewer 转成事件的作者结构 {member, agent}。
func authorOf(v identity.Viewer) Author {
	a := Author{MemberID: v.Member.ID, MemberName: v.Member.Name}
	if v.Agent != nil {
		a.AgentID = v.Agent.ID
		a.AgentName = v.Agent.Name
	}
	return a
}

// guestAuthorOf 把访客链接凭证转成作者结构：带链接备注与自报信息（ADR 0003：
// 自报信息未经平台核实，展示时与成员 Agent 的平台认证署名明确区分）。
func guestAuthorOf(v identity.Viewer, authorName string, viaAgent bool) Author {
	a := Author{AuthorName: strings.TrimSpace(authorName), ViaAgent: viaAgent}
	if v.GuestLink != nil {
		a.LinkID = v.GuestLink.ID
		a.LinkNote = v.GuestLink.Note
	}
	return a
}

func nullInt(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// guestScope 套上访客链接的作用域：访客只能操作链接指向的那张工单，
// 访问其他工单一律 ErrNotFound（与「不存在」不可区分，防枚举）。
func guestScope(viewer identity.Viewer, ticketSeq int64) error {
	if viewer.Kind == identity.ViewerGuest && viewer.GuestLink.TicketSeq != ticketSeq {
		return ErrNotFound
	}
	return nil
}
