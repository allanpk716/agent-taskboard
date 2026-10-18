// Package tickets 是「工单核心」模块：建单（编号与序号在同一事务内分配）、读取、
// 时间线事件与全局变更序号。等待方从时间线推导，不存会漂移的状态。
// 工单不能删除：本包不提供删除入口，编号与序号因此连续。
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

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/labels"
	"github.com/allanpk716/agent-taskboard/internal/template"
	"github.com/allanpk716/agent-taskboard/internal/templates"
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
	// ErrTemplateInactive 表示用已停用的模板建单。
	ErrTemplateInactive = errors.New("模板已停用")
	// ErrFieldsWithoutTemplate 表示非模板工单用 fields 修订说明。
	ErrFieldsWithoutTemplate = errors.New("这张工单不是用模板创建的，说明是一整块 Markdown，用 brief_md 修订")
	// ErrBriefMDWithTemplate 表示模板工单用 brief_md 修订说明。
	ErrBriefMDWithTemplate = errors.New("这张工单用模板创建，说明由字段拼成，按字段修订（fields）")
)

// UnknownFieldError 表示 fields 里出现了模板没有的字段键。
type UnknownFieldError struct {
	Field string
}

func (e *UnknownFieldError) Error() string { return "模板里没有字段：" + e.Field }

// InvalidFieldOptionError 表示单选/维度字段填了选项之外的值。
type InvalidFieldOptionError struct {
	Field   string
	Value   string
	Options []string
}

func (e *InvalidFieldOptionError) Error() string {
	return "字段「" + e.Field + "」的值 " + e.Value + " 不在选项里（可用：" + strings.Join(e.Options, "、") + "）"
}

// FieldWarning 是用模板建单时「缺必填」的逐项提醒：工单照常创建，只在响应里提醒
// （spec 用户故事 6：先建单，再交给访客补全；「不确定」算已填）。
type FieldWarning struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Author 是统一的作者结构 {member, agent}：本人操作 agent 为空（nil 语义用
// AgentID==0 表示），成员 Agent 的操作带具名 Agent（data-model「作者结构」）。
// 访客作者（MemberID == 0）带链接备注与自报信息（#32）：author_name 与
// via_agent 都是访客自报的、未经平台核实的信息（ADR 0003）。
type Author struct {
	MemberID   int64
	MemberName string
	AgentID    int64
	AgentName  string
	// AgentRevoked：这个具名 Agent 已被撤销（历史署名照实标注「已停用」，
	// #28 评审修正；只影响展示，历史事件不可变）。
	AgentRevoked bool

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
			name := a.MemberName + " 的 Agent · " + a.AgentName
			if a.AgentRevoked {
				name += "（已停用）"
			}
			return name
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
	Seq                int64
	Number             string
	Kind               string // task | report
	Title              string
	Status             string // open | closed
	CloseReason        string // "" | done | wontfix | duplicate
	DuplicateOfSeq     int64  // close_reason == duplicate 时另一张工单的序号（0 = 无）
	BriefMD            string
	BriefRevision      int64
	BriefAttachmentIDs []string
	Creator            Author
	AssigneeID         int64  // 指派人的成员 id；0 = 未指派（#36）
	AssigneeName       string // 指派人的姓名；空 = 未指派
	CreatedAt          time.Time
	LastChange         int64

	// 模板工单（#34）：保存创建时那个版本的字段定义快照（模板以后修改不影响
	// 已有工单）；说明由字段拼成（BriefMD 是渲染结果），字段值在 Fields 里。
	TemplateKey string
	TemplateDef *template.Snapshot
	Fields      map[string]string

	// 通过反馈入口提交的工单（#35）：创建者是访客（Creator.MemberID == 0，带链接
	// 备注与自报署名），经哪条链接（返回给提交者的那条）、从哪个入口提交。
	CreatorLinkID   int64
	CreatorIntakeID int64
}

// Event 是时间线上的一条事件，只追加不修改。
type Event struct {
	Seq    int64 // 单内递增
	Change int64 // 全局变更序号（与事件写入同事务推进）
	At     time.Time
	Actor  Author
	Type   string // created | reply | brief_revised | closed | reopened | label_added | …
	Label  string // label_added / label_removed 的标签（#31）

	// 附加字段，按事件类型出现（写入端点在 #30；assigned 在 #36）。
	BodyMD        string   // reply 的正文
	ReplyNo       int64    // reply 在单内的序号
	Deliver       bool     // reply 带「交付」标记（#36 启用）
	Revision      int64    // brief_revised 修订后的版本号
	Reason        string   // closed 的原因：done | wontfix | duplicate
	DuplicateOf   string   // closed=重复时另一张工单的编号（"" = 无）
	Note          string   // guest_link_created / guest_link_revoked 的备注（#32）
	LinkExpires   string   // guest_link_extended 的新到期时刻（RFC3339，#32）
	AttachmentIDs []string // reply 引用的附件（attachment_ids 与正文 att: 合并去重，#33）
	AssignFrom    string   // assigned 事件的改派前指派人；空 = 创建时指派（#36）
	AssignTo      string   // assigned 事件的指派对象（#36）
}

// Full 是读取一张工单时的完整投影：核心字段 + 标签 + 完整时间线。
type Full struct {
	Ticket
	Labels   []string
	Timeline []Event
}

// Manager 是工单核心的对外入口：「以某个 Viewer 建单」「读某张工单」。
type Manager struct {
	db        *sql.DB
	clk       clock.Clock
	labels    *labels.Manager
	templates *templates.Manager
	atts      *attachments.Manager // 附件认领在回复/说明修订的同一事务里（#33）
	notifier  Notifier
}

// NotificationInput 是通知规则模块的输入：事件前后的完整工单状态、操作者
// 与有效初筛人。
type NotificationInput struct {
	Before   Full
	After    Full
	Actor    Author
	Event    Event
	Triagers []int64

	// GuestLinks 与 Now 是等待方推导的输入。NotifyTx 会在工单事务里补齐，
	// 保证规则看到的链接状态与时钟和工单写入使用同一份真相。
	GuestLinks []GuestLinkInfo
	Now        time.Time
}

// Notifier 在工单事件同一事务里生成成员通知。
type Notifier interface {
	NotifyTx(context.Context, *sql.Tx, NotificationInput) ([]int64, error)
	Wake([]int64)
}

func NewManager(db *sql.DB, c clock.Clock, lm *labels.Manager, tm *templates.Manager, am *attachments.Manager) *Manager {
	return &Manager{db: db, clk: c, labels: lm, templates: tm, atts: am}
}

func (m *Manager) SetNotifier(n Notifier) { m.notifier = n }

// WaitingOn 用注入时钟和这张工单的访客链接状态推导等待方。HTTP 与查询
// 入口都从这里取值，不各自读时间或链接表。
func (m *Manager) WaitingOn(ctx context.Context, t Full) (WaitingOn, error) {
	links, err := GuestLinksForTicket(ctx, m.db, t.Seq)
	if err != nil {
		return WaitingOn{}, err
	}
	return WaitingOnOf(t, links, m.clk.Now()), nil
}

func (m *Manager) commitAndWake(tx *sql.Tx, notified []int64) error {
	if err := tx.Commit(); err != nil {
		return err
	}
	if m.notifier != nil {
		m.notifier.Wake(notified)
	}
	return nil
}

// Labels 返回标签字典模块（HTTP 层组 hint 用；完整字典接口在 #31）。
func (m *Manager) Labels() *labels.Manager { return m.labels }

// CreateResult 是建单成功的返回：工单投影（含时间线）、模板必填 warnings，
// 以及「请人反馈」一并创建的访客链接（#64；请求没带 guest_link 时为 nil，
// Key 只在这里出现一次）。
type CreateResult struct {
	Full      Full
	Warnings  []FieldWarning
	GuestLink *GuestLinkCreated
}

// CreateInput 是建单的输入。不用模板时给一整块 brief_md；用模板时给 template
// 和 fields（缺必填项只产生 warnings，不拦截）。assignee（成员 id 或姓名，
// #36）为空时不指派。guest_link（#64「请人反馈」）非空时在同一个事务里
// 一并创建访客链接。
type CreateInput struct {
	Kind        string
	Title       string
	BriefMD     string
	Labels      []string
	TemplateKey string
	Fields      map[string]string
	Assignee    string // 成员 id 或姓名；空 = 不指派（#36）
	Creator     identity.Viewer
	GuestLink   *GuestLinkSpec // 带 can_edit_brief 权限时免「至少一个维度标签」（#64）
}

// Create 校验并创建一张工单。工单编号（北京时间每天从 001 起，超 999 自动 4 位）
// 与全局序号在同一个事务内分配，同时写入 created 事件并推进全局变更序号；
// 建单时指派（#36）会在 created 之后记一条 assigned{from:null,to} 事件。
// 返回完整投影（含时间线，等待方由它推导）与模板必填项 warnings。
//
// 用模板创建时：类型由模板决定（忽略请求里的 kind）；预设标签自动打上；
// 维度字段选中的值同时成为标签并写进说明；缺必填项只产生 warnings
// （「不确定」算已填、不打该维度的标签）；工单保存创建时版本的字段定义快照。
//
// 带 guest_link（#64「请人反馈」）时：链接与工单在同一个事务里创建，时间线
// 依次是 created、assigned（有指派人时）、guest_link_created；链接带
// 「可填写说明」权限时免「至少一个维度标签」的校验，其余校验照常。
func (m *Manager) Create(ctx context.Context, in CreateInput) (CreateResult, error) {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return CreateResult{}, ErrTitleRequired
	}
	// 「请人反馈」的访客链接先校验（备注、有效期，validateCreate），任何校验
	// 失败都不动工单；写入事务里的另一半校验是同一份逻辑。
	if in.GuestLink != nil {
		if _, err := in.GuestLink.validateCreate(m.clk.Now()); err != nil {
			return CreateResult{}, err
		}
	}
	warnings := []FieldWarning{}
	var snapshot *template.Snapshot
	if in.TemplateKey != "" {
		tmpl, err := m.templates.Get(ctx, in.TemplateKey)
		if err != nil {
			return CreateResult{}, err
		}
		if !tmpl.Active {
			return CreateResult{}, ErrTemplateInactive
		}
		if err := validateFieldsAgainst(tmpl.Fields, in.Fields); err != nil {
			return CreateResult{}, err
		}
		in.Kind = tmpl.Kind
		snapshot = &template.Snapshot{Key: tmpl.Key, Version: tmpl.Version, Definition: tmpl.Definition}
		for _, f := range tmpl.Fields {
			v := strings.TrimSpace(in.Fields[f.Key])
			// 维度字段选中的值同时成为标签；「不确定」不打标签（决议 6）。
			if f.Type == template.TypeDimension && v != "" && v != template.UnsureOption {
				in.Labels = append(in.Labels, f.Dimension+":"+v)
			}
		}
		// 必填校验是模板模块的纯逻辑（「不确定」非空、算已填）。
		for _, key := range template.MissingRequired(in.Fields, tmpl.Fields) {
			warnings = append(warnings, FieldWarning{Field: key, Message: "必填项未填写"})
		}
		// 模板的预设标签自动打上。
		in.Labels = append(in.Labels, tmpl.PresetLabels...)
	} else {
		switch in.Kind {
		case "":
			in.Kind = "task"
		case "task", "report":
		default:
			return CreateResult{}, ErrInvalidKind
		}
	}
	// 指派（#36）：成员 id 或姓名；写成成员 Agent → 422 assignee_must_be_member。
	var (
		assigneeID   int64
		assigneeName string
	)
	if strings.TrimSpace(in.Assignee) != "" {
		var err error
		assigneeID, assigneeName, err = m.resolveAssignee(ctx, in.Assignee)
		if err != nil {
			return CreateResult{}, err
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
	// 免维度标签（#64）：只有带 guest_link 且 can_edit_brief 为真时才免
	//（成员这时不知道机型，等访客补全）；其余建单规则不变。
	if !hasDimension && !(in.GuestLink != nil && in.GuestLink.CanEditBrief) {
		return CreateResult{}, ErrDimensionLabelRequired
	}
	if err := m.labels.Validate(ctx, in.Labels); err != nil {
		return CreateResult{}, err
	}

	now := m.clk.Now()
	date := now.Format("20060102")
	var notified []int64

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return CreateResult{}, err
	}
	defer tx.Rollback()

	// 当日序号：已有的当天工单数 + 1；超过 999 时自动用 4 位。
	var today int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tickets WHERE number LIKE ?`, date+"-%").Scan(&today); err != nil {
		return CreateResult{}, err
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
		TemplateKey:   in.TemplateKey,
		TemplateDef:   snapshot,
		Fields:        in.Fields,
	}
	var templateKey, snapshotJSON, fieldsJSON any
	if snapshot != nil {
		t.BriefMD = template.RenderBrief(in.Fields, snapshot.Fields)
		templateKey = snapshot.Key
		if buf, err := json.Marshal(snapshot); err != nil {
			return CreateResult{}, fmt.Errorf("序列化模板快照失败：%w", err)
		} else {
			snapshotJSON = string(buf)
		}
		if buf, err := json.Marshal(in.Fields); err != nil {
			return CreateResult{}, fmt.Errorf("序列化字段值失败：%w", err)
		} else {
			fieldsJSON = string(buf)
		}
	}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO tickets (number, kind, title, status, brief_md, brief_revision,
		                     creator_member_id, creator_agent_id, assignee_id, created_at, last_change,
		                     template_key, template_version, template_snapshot, fields_json)
		VALUES (?, ?, ?, 'open', ?, 1, ?, ?, ?, ?, 0, ?, ?, ?, ?)
		RETURNING seq`,
		number, in.Kind, in.Title, t.BriefMD, creator.MemberID, agentID,
		nullInt(assigneeID), now.Format(time.RFC3339Nano),
		templateKey, snapshotVersion(snapshot), snapshotJSON, fieldsJSON).Scan(&t.Seq); err != nil {
		return CreateResult{}, fmt.Errorf("写入工单失败：%w", err)
	}

	// created 事件：单内 seq 从 1 起；全局变更序号与事件写入同事务推进。
	// 建单时指派（#36）随后再记一条 assigned{from:null,to}；「请人反馈」的
	// 链接（#64）最后记 guest_link_created。
	_, change, err := m.appendEvent(ctx, tx, t.Seq, now, creator, "created")
	if err != nil {
		return CreateResult{}, err
	}
	t.LastChange = change
	if _, err := tx.ExecContext(ctx, `UPDATE tickets SET last_change = ? WHERE seq = ?`, change, t.Seq); err != nil {
		return CreateResult{}, err
	}
	assignedSeq := int64(0)
	if assigneeID != 0 {
		eventSeq, _, err := m.insertAssignedEvent(ctx, tx, t.Seq, creator, "", assigneeName, now)
		if err != nil {
			return CreateResult{}, err
		}
		assignedSeq = eventSeq
	}
	for _, l := range in.Labels {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO ticket_labels (ticket_seq, label) VALUES (?, ?)`, t.Seq, l); err != nil {
			return CreateResult{}, fmt.Errorf("写入标签 %s 失败：%w", l, err)

		}
	}
	// 「请人反馈」（#64）：访客链接与工单同一个事务创建，任何一步失败都回滚
	//（不留下半张工单）；时间线在 created/assigned 之后记 guest_link_created。
	var linkCreated *GuestLinkCreated
	if in.GuestLink != nil {
		link, err := m.createGuestLinkTx(ctx, tx, t.Seq, authorOf(in.Creator), *in.GuestLink, now)
		if err != nil {
			return CreateResult{}, err
		}
		linkCreated = link
	}
	if m.notifier != nil {
		after := Full{Ticket: t}
		after.Timeline = []Event{{Seq: 1, At: now, Actor: creator, Type: "created", Change: t.LastChange}}
		event := after.Timeline[0]
		if assignedSeq != 0 {
			assigned := Event{Seq: assignedSeq, At: now, Actor: creator, Type: "assigned",
				AssignTo: assigneeName, Change: t.LastChange}
			after.Timeline = append(after.Timeline, assigned)
			event = assigned
		}
		var err error
		notified, err = m.notifier.NotifyTx(ctx, tx, NotificationInput{After: after, Actor: creator, Event: event})
		if err != nil {
			return CreateResult{}, err
		}
	}

	if err := m.commitAndWake(tx, notified); err != nil {
		return CreateResult{}, err
	}
	full, err := m.ByRef(ctx, number)
	if err != nil {
		return CreateResult{GuestLink: linkCreated}, err
	}
	return CreateResult{Full: full, Warnings: warnings, GuestLink: linkCreated}, nil
}

// validateFieldsAgainst 校验字段值：键必须是模板里的字段（问句原文），
// 单选字段的值必须在选项里。维度字段的值随标签一起严格校验（须是字典值）。
func validateFieldsAgainst(defs []template.Field, fields map[string]string) error {
	byKey := make(map[string]template.Field, len(defs))
	for _, f := range defs {
		byKey[f.Key] = f
	}
	for k := range fields {
		if _, ok := byKey[k]; !ok {
			return &UnknownFieldError{Field: k}
		}
	}
	for _, f := range defs {
		v := strings.TrimSpace(fields[f.Key])
		if v == "" || f.Type != template.TypeRadio {
			continue
		}
		found := false
		for _, opt := range f.Options {
			if opt == v {
				found = true
				break
			}
		}
		if !found {
			return &InvalidFieldOptionError{Field: f.Key, Value: v, Options: f.Options}
		}
	}
	return nil
}

// snapshotVersion 取快照里的版本号（无快照时存 NULL）。
func snapshotVersion(s *template.Snapshot) any {
	if s == nil {
		return nil
	}
	return s.Version

}

// ByRef 用工单编号（YYYYMMDD-NNN）或全局序号读一张工单的完整投影。
// ref 可以带 # 前缀。查不到时返回 ErrNotFound。
func (m *Manager) ByRef(ctx context.Context, ref string) (Full, error) {
	ref = strings.TrimPrefix(ref, "#")
	var (
		t                  Ticket
		at                 string
		closeReason        sql.NullString
		duplicateOfSeq     sql.NullInt64
		memberID           sql.NullInt64
		memberName         sql.NullString
		agentID            sql.NullInt64
		agentName          sql.NullString
		agentRevoked       sql.NullString
		assigneeID         sql.NullInt64
		assigneeName       sql.NullString
		creatorLinkID      sql.NullInt64
		creatorIntakeID    sql.NullInt64
		creatorName        sql.NullString
		templateKey        sql.NullString
		snapshotJSON       sql.NullString
		fieldsJSON         sql.NullString
		briefAttachmentIDs sql.NullString
		err                error
	)
	scan := func(row *sql.Row) error {
		return row.Scan(&t.Seq, &t.Number, &t.Kind, &t.Title, &t.Status, &closeReason, &duplicateOfSeq,
			&t.BriefMD, &t.BriefRevision, &memberID, &memberName, &agentID, &agentName, &agentRevoked,
			&assigneeID, &assigneeName,
			&creatorLinkID, &creatorIntakeID, &creatorName,
			&at, &t.LastChange, &templateKey, &snapshotJSON, &fieldsJSON, &briefAttachmentIDs)
	}
	const cols = `t.seq, t.number, t.kind, t.title, t.status, t.close_reason, t.duplicate_of_seq,
		t.brief_md, t.brief_revision,
		t.creator_member_id, cm.name, t.creator_agent_id, ca.name, ca.revoked_at,
		t.assignee_id, am.name,
		t.creator_link_id, t.creator_intake_id, t.creator_author_name,
		t.created_at, t.last_change, t.template_key, t.template_snapshot, t.fields_json,
		t.brief_attachment_ids`

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
	t.BriefAttachmentIDs = []string{}
	if briefAttachmentIDs.Valid && briefAttachmentIDs.String != "" {
		if err := json.Unmarshal([]byte(briefAttachmentIDs.String), &t.BriefAttachmentIDs); err != nil {
			return Full{}, fmt.Errorf("解析工单 %s 的说明附件失败：%w", t.Number, err)
		}
	}
	t.DuplicateOfSeq = duplicateOfSeq.Int64
	t.AssigneeID = assigneeID.Int64
	t.AssigneeName = assigneeName.String
	t.Creator = Author{
		MemberID:     memberID.Int64,
		MemberName:   memberName.String,
		AgentID:      agentID.Int64,
		AgentName:    agentName.String,
		AgentRevoked: agentRevoked.Valid && agentRevoked.String != "",
	}
	// 入口提交的工单：创建者是访客，带返回链接的备注与提交者的自报署名（#35）。
	t.CreatorLinkID = creatorLinkID.Int64
	t.CreatorIntakeID = creatorIntakeID.Int64
	if t.CreatorLinkID != 0 {
		note, err := m.creatorLinkNote(ctx, t.CreatorLinkID)
		if err != nil {
			return Full{}, err
		}
		t.Creator.LinkID = t.CreatorLinkID
		t.Creator.LinkNote = note
		t.Creator.AuthorName = creatorName.String
	}
	t.CreatedAt, err = time.ParseInLocation(time.RFC3339Nano, at, clock.Beijing)
	if err != nil {
		return Full{}, fmt.Errorf("解析创建时间 %q 失败：%w", at, err)
	}

	// 模板工单：读回创建时版本的字段快照和字段值，说明按快照的字段定义拼出
	//（模板以后修改不影响已有工单）。
	if snapshotJSON.Valid && snapshotJSON.String != "" {
		snap := &template.Snapshot{}
		if err := json.Unmarshal([]byte(snapshotJSON.String), snap); err != nil {
			return Full{}, fmt.Errorf("解析工单 %s 的模板快照失败：%w", t.Number, err)
		}
		t.TemplateDef = snap
		t.TemplateKey = snap.Key
		if fieldsJSON.Valid && fieldsJSON.String != "" {
			if err := json.Unmarshal([]byte(fieldsJSON.String), &t.Fields); err != nil {
				return Full{}, fmt.Errorf("解析工单 %s 的字段值失败：%w", t.Number, err)
			}
		}
		if t.Fields == nil {
			t.Fields = map[string]string{}
		}
		t.BriefMD = template.RenderBrief(t.Fields, snap.Fields)
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

// creatorLinkNote 读创建者链接的备注（备注在链接创建时写下、不变更）。
func (m *Manager) creatorLinkNote(ctx context.Context, linkID int64) (string, error) {
	var note string
	err := m.db.QueryRowContext(ctx,
		`SELECT note FROM guest_links WHERE id = ?`, linkID).Scan(&note)
	if err != nil {
		return "", fmt.Errorf("读创建者链接 %d 失败：%w", linkID, err)
	}
	return note, nil
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
		SELECT e.seq, e.change_seq, e.at, e.type, e.actor_member_id, mm.name, e.actor_agent_id, aa.name, aa.revoked_at,
		       e.body_md, e.reply_no, e.deliver, e.revision, e.reason, e.duplicate_of, e.label,
		       e.note, e.link_expires_at, e.attachment_ids, e.assign_from, e.assign_to,
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
			e                 Event
			at                string
			actorMemberID     sql.NullInt64
			actorMemberName   sql.NullString
			actorAgentID      sql.NullInt64
			actorAgentName    sql.NullString
			actorAgentRevoked sql.NullString
			bodyMD            sql.NullString
			replyNo           sql.NullInt64
			deliver           int
			revision          sql.NullInt64
			reason            sql.NullString
			duplicateOf       sql.NullString
			label             sql.NullString
			note              sql.NullString
			linkExpiresAt     sql.NullString
			attachmentIDs     sql.NullString
			assignFrom        sql.NullString
			assignTo          sql.NullString
			actorLinkID       sql.NullInt64
			actorLinkNote     sql.NullString
			actorAuthorName   sql.NullString
			actorViaAgent     int
		)
		if err := rows.Scan(&e.Seq, &e.Change, &at, &e.Type,
			&actorMemberID, &actorMemberName, &actorAgentID, &actorAgentName, &actorAgentRevoked,
			&bodyMD, &replyNo, &deliver, &revision, &reason, &duplicateOf, &label,
			&note, &linkExpiresAt, &attachmentIDs, &assignFrom, &assignTo,
			&actorLinkID, &actorLinkNote, &actorAuthorName, &actorViaAgent); err != nil {
			return nil, err
		}
		e.Actor = Author{
			MemberID:     actorMemberID.Int64,
			MemberName:   actorMemberName.String,
			AgentID:      actorAgentID.Int64,
			AgentName:    actorAgentName.String,
			AgentRevoked: actorAgentRevoked.Valid && actorAgentRevoked.String != "",
			LinkID:       actorLinkID.Int64,
			LinkNote:     actorLinkNote.String,
			AuthorName:   actorAuthorName.String,
			ViaAgent:     actorViaAgent == 1,
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
		if attachmentIDs.Valid && attachmentIDs.String != "" {
			_ = json.Unmarshal([]byte(attachmentIDs.String), &e.AttachmentIDs)
		}
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
// （spec「等待方与收件箱」，#37 的查询也复用这一个函数）：
// guestLinks 与 now 是轮次规则的输入；#126 只打通输入，不改变下面的规则。
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
func WaitingOnOf(t Full, guestLinks []GuestLinkInfo, now time.Time) WaitingOn {
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
