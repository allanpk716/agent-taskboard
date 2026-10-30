// 反馈入口（#35）：部门级的长期地址。成员创建、停用、轮换 slug；入口绑定一个
// 反馈类型的模板，始终使用最新版本。匿名提交严格校验（缺必填 422 missing_fields，
// 逐项提示），成功时建一张反馈工单——创建者是访客（视同说明的创建者）——并把一条
// 带「可填写说明」权限的 30 天访客链接交给提交者（复用 #32 的访客链接）。
package tickets

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/template"
)

// 反馈入口的校验错误；HTTP 层把它们映射成 4xx 错误码。
var (
	// ErrIntakeNotFound 表示入口不存在或已停用（旧 slug 轮换后同样报这个错）。
	ErrIntakeNotFound = errors.New("没有这个反馈入口")
	// ErrSlugRequired 表示创建/轮换时没写 slug。
	ErrSlugRequired = errors.New("反馈入口的 slug 不能为空")
	// ErrInvalidSlug 表示 slug 里有不适合放进 URL 的字符。
	ErrInvalidSlug = errors.New("slug 只能用小写字母、数字和连字符，如 bug-report")
	// ErrSlugTaken 表示 slug 已被另一个入口占用。
	ErrSlugTaken = errors.New("这个 slug 已被另一个反馈入口占用")
	// ErrNameRequired 表示创建入口时没写名称。
	ErrNameRequired = errors.New("反馈入口需要一个名称，如「部门 Bug 反馈」")
	// ErrTemplateNotReport 表示绑定的模板不是反馈类型（spec：绑定的模板必须是反馈类型）。
	ErrTemplateNotReport = errors.New("反馈入口只能绑定反馈类型的模板（类型：反馈）")
	// ErrIntakeDailyLimit 表示入口当日提交数达到配置上限，北京时间次日自动恢复。
	ErrIntakeDailyLimit = errors.New("这个反馈入口今天提交数已达上限")
)

// MissingFieldHint 是严格校验（422 missing_fields）里每一项的提示：
// 缺什么、怎么填。引导与示例取自模板字段定义（spec：hint 逐项说明）。
type MissingFieldHint struct {
	Field   string `json:"field"`
	Hint    string `json:"hint"`
	Example string `json:"example,omitempty"`
}

// MissingFieldsError 是入口提交缺必填项（422，与成员建单的 warnings 相反）。
type MissingFieldsError struct {
	Fields []MissingFieldHint
}

func (e *MissingFieldsError) Error() string {
	keys := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		keys = append(keys, f.Field)
	}
	return "还有 " + fmt.Sprint(len(e.Fields)) + " 项必填未填：" + strings.Join(keys, "、")
}

// IntakeSpec 是创建反馈入口的输入。
type IntakeSpec struct {
	Slug              string
	Name              string
	TemplateKey       string
	DepartmentContact string
}

// Intake 是一个反馈入口的投影（模板始终读当前版本，不缓存）。
type Intake struct {
	ID                int64
	Slug              string
	Name              string
	TemplateKey       string
	DepartmentContact string
	Active            bool
	CreatedBy         Author
	CreatedAt         time.Time
	DeactivatedAt     *time.Time
}

// CreateIntake 以 viewer 的身份创建一个反馈入口。绑定的模板必须存在、在用且
// 是反馈类型（非反馈被拒）。
func (m *Manager) CreateIntake(ctx context.Context, viewer identity.Viewer, spec IntakeSpec) (Intake, error) {
	spec.Slug = strings.TrimSpace(spec.Slug)
	if spec.Slug == "" {
		return Intake{}, ErrSlugRequired
	}
	if !validSlug(spec.Slug) {
		return Intake{}, ErrInvalidSlug
	}
	spec.Name = strings.TrimSpace(spec.Name)
	if spec.Name == "" {
		return Intake{}, ErrNameRequired
	}
	tmpl, err := m.templates.Get(ctx, spec.TemplateKey)
	if err != nil {
		return Intake{}, err
	}
	if !tmpl.Active {
		return Intake{}, ErrTemplateInactive
	}
	if tmpl.Kind != "report" {
		return Intake{}, ErrTemplateNotReport
	}
	now := m.clk.Now()
	var agentID any
	if viewer.Agent != nil {
		agentID = viewer.Agent.ID
	}
	var id int64
	if err := m.db.QueryRowContext(ctx, `
		INSERT INTO intakes (slug, name, template_key, department_contact, active,
		                     created_by_member_id, created_by_agent_id, created_at)
		VALUES (?, ?, ?, ?, 1, ?, ?, ?)
		RETURNING id`,
		spec.Slug, spec.Name, spec.TemplateKey, strings.TrimSpace(spec.DepartmentContact), viewer.Member.ID, agentID,
		now.Format(time.RFC3339Nano)).Scan(&id); err != nil {
		return Intake{}, ErrSlugTaken // UNIQUE 冲突只有 slug
	}
	return Intake{ID: id, Slug: spec.Slug, Name: spec.Name, TemplateKey: spec.TemplateKey,
		DepartmentContact: strings.TrimSpace(spec.DepartmentContact),
		Active:            true, CreatedBy: authorOf(viewer), CreatedAt: now}, nil
}

// Intakes 列出全部反馈入口（含已停用，带 active 标记），按 slug 排序。
func (m *Manager) Intakes(ctx context.Context) ([]Intake, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT i.id, i.slug, i.name, i.template_key, i.department_contact, i.active, i.created_at, i.deactivated_at,
		       m.id, m.name, a.id, a.name, a.revoked_at
		FROM intakes i
		LEFT JOIN members m ON m.id = i.created_by_member_id
		LEFT JOIN agent_tokens a ON a.id = i.created_by_agent_id
		ORDER BY i.slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Intake{}
	for rows.Next() {
		in, err := scanIntake(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// ActiveIntakeBySlug 取一个活跃的入口（不存在或已停用 → ErrIntakeNotFound，
// 轮换后的旧 slug 同样查不到）。入口始终使用绑定模板的最新版本。
func (m *Manager) ActiveIntakeBySlug(ctx context.Context, slug string) (Intake, error) {
	in, err := m.intakeRow(ctx, slug)
	if err != nil {
		return Intake{}, err
	}
	if !in.Active {
		return Intake{}, ErrIntakeNotFound
	}
	return in, nil
}

// DeactivateIntake 停用一个反馈入口（只能停用；幂等）。停用后匿名读写都按
// 不存在处理；返回是否发生了变化。
func (m *Manager) DeactivateIntake(ctx context.Context, slug string) (bool, error) {
	now := m.clk.Now()
	res, err := m.db.ExecContext(ctx,
		`UPDATE intakes SET active = 0, deactivated_at = ? WHERE slug = ? AND active = 1`,
		now.Format(time.RFC3339Nano), slug)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		var known int
		if err := m.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM intakes WHERE slug = ?`, slug).Scan(&known); err != nil {
			return false, err
		}
		if known == 0 {
			return false, ErrIntakeNotFound
		}
		return false, nil
	}
	return true, nil
}

// RotateIntakeSlug 轮换入口的 slug：旧 slug 立即失效（404），新 slug 立即可用。
func (m *Manager) RotateIntakeSlug(ctx context.Context, slug, newSlug string) (Intake, error) {
	newSlug = strings.TrimSpace(newSlug)
	if newSlug == "" {
		return Intake{}, ErrSlugRequired
	}
	if !validSlug(newSlug) {
		return Intake{}, ErrInvalidSlug
	}
	if newSlug == slug {
		return Intake{}, ErrSlugTaken
	}
	res, err := m.db.ExecContext(ctx,
		`UPDATE intakes SET slug = ? WHERE slug = ?`, newSlug, slug)
	if err != nil {
		return Intake{}, ErrSlugTaken
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Intake{}, err
	}
	if n == 0 {
		return Intake{}, ErrIntakeNotFound
	}
	return m.intakeRow(ctx, newSlug)
}

// intakeRow 按 slug 取入口（无论活跃与否）。
func (m *Manager) intakeRow(ctx context.Context, slug string) (Intake, error) {
	row := m.db.QueryRowContext(ctx, `
		SELECT i.id, i.slug, i.name, i.template_key, i.department_contact, i.active, i.created_at, i.deactivated_at,
		       m.id, m.name, a.id, a.name, a.revoked_at
		FROM intakes i
		LEFT JOIN members m ON m.id = i.created_by_member_id
		LEFT JOIN agent_tokens a ON a.id = i.created_by_agent_id
		WHERE i.slug = ?`, slug)
	in, err := scanIntake(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Intake{}, ErrIntakeNotFound
	}
	return in, err
}

// UpdateIntakeDepartmentContact lets an administrator change how intake submitters
// should reach the department after losing their access code.
func (m *Manager) UpdateIntakeDepartmentContact(ctx context.Context, slug, contact string) (Intake, error) {
	res, err := m.db.ExecContext(ctx,
		`UPDATE intakes SET department_contact = ? WHERE slug = ?`,
		strings.TrimSpace(contact), slug)
	if err != nil {
		return Intake{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Intake{}, err
	}
	if n == 0 {
		return Intake{}, ErrIntakeNotFound
	}
	return m.intakeRow(ctx, slug)
}

type intakeScanner interface{ Scan(dest ...any) error }

func scanIntake(row intakeScanner) (Intake, error) {
	var (
		in            Intake
		active        int
		createdAt     string
		deactivatedAt sql.NullString
		memberID      sql.NullInt64
		memberName    sql.NullString
		agentID       sql.NullInt64
		agentName     sql.NullString
		agentRevoked  sql.NullString
	)
	if err := row.Scan(&in.ID, &in.Slug, &in.Name, &in.TemplateKey, &in.DepartmentContact, &active,
		&createdAt, &deactivatedAt, &memberID, &memberName, &agentID, &agentName, &agentRevoked); err != nil {
		return Intake{}, err
	}
	in.Active = active == 1
	in.CreatedBy = Author{
		MemberID: memberID.Int64, MemberName: memberName.String,
		AgentID: agentID.Int64, AgentName: agentName.String,
		AgentRevoked: agentRevoked.Valid && agentRevoked.String != "",
	}
	var err error
	if in.CreatedAt, err = parseTime(createdAt); err != nil {
		return Intake{}, err
	}
	if deactivatedAt.Valid {
		t, err := parseTime(deactivatedAt.String)
		if err != nil {
			return Intake{}, err
		}
		in.DeactivatedAt = &t
	}
	return in, nil
}

// validSlug 校验 slug：小写字母、数字、连字符（入口地址 /i/<slug> 的一部分）。
func validSlug(slug string) bool {
	if slug == "" || slug[0] == '-' || slug[len(slug)-1] == '-' {
		return false
	}
	for _, r := range slug {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}

// SubmissionInput 是匿名提交反馈的输入：{title, fields, author_name}
// （spec「反馈入口」；author_name 是提交者自报的署名，ADR 0003：不经核实）。
type SubmissionInput struct {
	Title      string
	Fields     map[string]string
	AuthorName string
	// IdempotencyKey 认领提交前匿名上传到这个入口的附件。
	IdempotencyKey string
}

// SubmissionResult 是提交成功的结果：新工单 + 交给提交者的访客链接
// （Key 是明文密钥，只在提交响应里出现一次）。
type SubmissionResult struct {
	Ticket            Ticket
	LinkKey           string
	LinkID            int64
	LinkNote          string
	ExpiresAt         time.Time
	DeviceToken       string
	AccessCode        string
	ClaimedAt         time.Time
	DepartmentContact string
}

// SubmitToIntake 匿名提交一张反馈：严格校验（缺必填 422 missing_fields，与成员
// 建单的 warnings 相反；「不确定」算已填），成功时建工单并生成一条带「可填写
// 说明」权限的 30 天访客链接。工单、链接、created 与 guest_link_created 事件
// 在同一个事务里写入，全局变更序号随之推进。
//
// 与成员建单的差别：类型由入口的模板决定（恒为 report）；预设标签与维度字段值
// 自动打上；不要求提交者选维度标签（GLOSSARY：入口提交是维度标签要求的唯一
// 例外）；创建者记录为访客（经返回的那条链接，含入口信息），视同说明的创建者。
func (m *Manager) SubmitToIntake(ctx context.Context, slug string, in SubmissionInput) (SubmissionResult, error) {
	intake, err := m.ActiveIntakeBySlug(ctx, slug)
	if err != nil {
		return SubmissionResult{}, err
	}
	// 入口始终使用模板的最新版本（停用的模板不能再用来提交）。
	tmpl, err := m.templates.Get(ctx, intake.TemplateKey)
	if err != nil {
		return SubmissionResult{}, err
	}
	if !tmpl.Active {
		return SubmissionResult{}, ErrTemplateInactive
	}
	if tmpl.Kind != "report" {
		return SubmissionResult{}, ErrTemplateNotReport
	}

	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return SubmissionResult{}, ErrTitleRequired
	}
	if err := validateFieldsAgainst(tmpl.Fields, in.Fields); err != nil {
		return SubmissionResult{}, err
	}
	// 严格校验：缺必填项拦下（MissingRequired 认「不确定」算已填），逐项提示。
	if missing := template.MissingRequired(in.Fields, tmpl.Fields); len(missing) > 0 {
		return SubmissionResult{}, &MissingFieldsError{Fields: missingFieldHints(missing, tmpl.Fields)}
	}
	authorName := strings.TrimSpace(in.AuthorName)
	if authorName == "" {
		return SubmissionResult{}, ErrClaimNameRequired
	}
	labels := append([]string{}, tmpl.PresetLabels...)
	for _, f := range tmpl.Fields {
		v := strings.TrimSpace(in.Fields[f.Key])
		// 维度字段选中的值同时成为标签；「不确定」不打标签（决议 6）。
		if f.Type == template.TypeDimension && v != "" && v != template.UnsureOption {
			labels = append(labels, f.Dimension+":"+v)
		}
	}
	labels = dedupe(labels)
	if err := m.labels.Validate(ctx, labels); err != nil {
		return SubmissionResult{}, err
	}
	// 入口附件必须属于本入口和本次提交的幂等键；ClaimIntakeTx 在建单事务里
	// 再按数据库当前状态做最终校验。
	var attachmentRefs []attachments.PostRef
	for _, v := range in.Fields {
		attachmentRefs = append(attachmentRefs, attachments.ParsePostRefs(v, nil)...)
	}
	if len(attachmentRefs) > 0 && in.IdempotencyKey == "" {
		return SubmissionResult{}, attachments.ErrNotUsable
	}
	notifyPaused := false

	key, err := identity.NewGuestKey()
	if err != nil {
		return SubmissionResult{}, err
	}
	keyCiphertext, err := m.encryptedGuestLinkKey(key)
	if err != nil {
		return SubmissionResult{}, err
	}
	note := "反馈入口「" + intake.Name + "」提交人"
	now := m.clk.Now()
	date := now.Format("20060102")
	author := Author{AuthorName: authorName} // LinkID/LinkNote 链接落库后补上

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return SubmissionResult{}, err
	}
	defer tx.Rollback()
	var notified []int64
	notifyPaused, err = m.reserveIntakeDailyTx(ctx, tx, intake.ID, date)
	if err != nil {
		return SubmissionResult{}, err
	}

	// 当日序号与全局序号在同一事务内分配；墓碑同样计数，编号永不复用。
	number, err := nextTicketNumber(ctx, tx, date)
	if err != nil {
		return SubmissionResult{}, err
	}

	snapshot := template.Snapshot{Key: tmpl.Key, Version: tmpl.Version, Definition: tmpl.Definition}
	brief := template.RenderBrief(in.Fields, snapshot.Fields)
	fieldsJSON, err := json.Marshal(in.Fields)
	if err != nil {
		return SubmissionResult{}, fmt.Errorf("序列化字段值失败：%w", err)
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return SubmissionResult{}, fmt.Errorf("序列化模板快照失败：%w", err)
	}
	t := Ticket{
		Kind: "report", Title: in.Title, BriefMD: brief, Status: "open",
		Creator:     author,
		CreatedAt:   now,
		TemplateKey: tmpl.Key, TemplateDef: &snapshot, Fields: in.Fields,
		CreatorIntakeID: intake.ID,
	}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO tickets (number, kind, title, status, brief_md, brief_revision,
		                     created_at, last_change,
		                     template_key, template_version, template_snapshot, fields_json,
		                     creator_intake_id, creator_author_name)
		VALUES (?, 'report', ?, 'open', ?, 1, ?, 0, ?, ?, ?, ?, ?, ?)
		RETURNING seq`,
		number, in.Title, brief, now.Format(time.RFC3339Nano),
		tmpl.Key, tmpl.Version, string(snapshotJSON), string(fieldsJSON),
		intake.ID, nullStr(authorName)).Scan(&t.Seq); err != nil {
		return SubmissionResult{}, fmt.Errorf("写入工单失败：%w", err)
	}

	// 交给提交者的访客链接：30 天、「可填写说明」（提交者视同创建者，靠它修订说明）。
	var linkID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO guest_links (ticket_seq, key_hash, key_ciphertext, note, expires_at, can_edit_brief,
		                         created_by_member_id, created_at)
		VALUES (?, ?, ?, ?, ?, 1, ?, ?)
		RETURNING id`,
		t.Seq, identity.CredentialHash(key), keyCiphertext, note,
		now.Add(30*24*time.Hour).Format(time.RFC3339Nano),
		intake.CreatedBy.MemberID, now.Format(time.RFC3339Nano)).Scan(&linkID); err != nil {
		return SubmissionResult{}, fmt.Errorf("写入提交者的访客链接失败：%w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE tickets SET creator_link_id = ? WHERE seq = ?`, linkID, t.Seq); err != nil {
		return SubmissionResult{}, err
	}
	author.LinkID, author.LinkNote = linkID, note
	t.Creator = author
	t.CreatorLinkID = linkID

	// created 事件（actor 是提交的访客）与 guest_link_created 事件，同事务推进
	// 变更序号；insertLinkEvent 顺带把工单 last_change 推到最新一条。
	if _, _, err := m.appendEvent(ctx, tx, t.Seq, now, author, "created"); err != nil {
		return SubmissionResult{}, err
	}
	if err := m.insertBriefVersionTx(ctx, tx, t, author, now); err != nil {
		return SubmissionResult{}, err
	}
	if err := m.insertLinkEvent(ctx, tx, t.Seq, author, "guest_link_created", note, "", now); err != nil {
		return SubmissionResult{}, err
	}
	if m.notifier != nil {
		t.Number = number
		after := Full{Ticket: t}
		after.Timeline = []Event{{Seq: 1, At: now, Actor: author, Type: "created"}}
		notified, err = m.notifier.NotifyTx(ctx, tx, NotificationInput{
			After: after, Actor: author, Event: after.Timeline[0],
		})
		if err != nil {
			return SubmissionResult{}, err
		}
	}
	for _, l := range labels {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO ticket_labels (ticket_seq, label) VALUES (?, ?)`, t.Seq, l); err != nil {
			return SubmissionResult{}, fmt.Errorf("写入标签 %s 失败：%w", l, err)
		}
	}
	if err := m.atts.ClaimIntakeTx(ctx, tx, attachments.IntakeClaim{
		IntakeID: intake.ID, UploadKey: in.IdempotencyKey,
	}, t.Seq, attachmentRefs, now); err != nil {
		return SubmissionResult{}, err
	}
	if err := m.commitAndWake(tx, notified); err != nil {
		return SubmissionResult{}, err
	}
	if notifyPaused && m.systemNotifier != nil {
		_ = m.systemNotifier.NotifySystem(ctx, "intake_paused",
			"反馈入口「"+intake.Name+"」今天提交数已达上限，入口今日已暂停")
	}
	t.Number = number
	result := SubmissionResult{
		Ticket:  t,
		LinkKey: key, LinkID: linkID, LinkNote: note,
		ExpiresAt: now.Add(30 * 24 * time.Hour),
	}
	claim, err := m.ClaimGuestLink(ctx, identity.GuestLink{
		ID: linkID, TicketSeq: t.Seq, Note: note,
		ExpiresAt: result.ExpiresAt, CanEditBrief: true,
	}, authorName)
	if err != nil {
		return SubmissionResult{}, err
	}
	result.DeviceToken = claim.DeviceToken
	result.AccessCode = claim.AccessCode
	result.ClaimedAt = claim.ClaimedAt
	result.DepartmentContact = intake.DepartmentContact
	return result, nil
}

// reserveIntakeDailyTx 在建单事务里预留一个每日提交名额；达到上限时返回暂停错误。
func (m *Manager) reserveIntakeDailyTx(ctx context.Context, tx *sql.Tx, intakeID int64, day string) (bool, error) {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO intake_daily_usage (intake_id, day, submissions) VALUES (?, ?, 1)
		ON CONFLICT(intake_id, day) DO UPDATE SET submissions = submissions + 1`,
		intakeID, day); err != nil {
		return false, err
	}
	var count int
	if err := tx.QueryRowContext(ctx,
		`SELECT submissions FROM intake_daily_usage WHERE intake_id = ? AND day = ?`, intakeID, day).Scan(&count); err != nil {
		return false, err
	}
	if count > m.dailyIntakeLimit {
		return false, ErrIntakeDailyLimit
	}
	return count == m.dailyIntakeLimit, nil
}

// missingFieldHints 把缺必填的逐项提示拼出来：引导说明、维度字段可「不确定」、
// 单选字段列选项；示例单独一项（spec：hint 逐项说明缺什么、怎么填）。
func missingFieldHints(keys []string, defs []template.Field) []MissingFieldHint {
	byKey := make(map[string]template.Field, len(defs))
	for _, f := range defs {
		byKey[f.Key] = f
	}
	out := make([]MissingFieldHint, 0, len(keys))
	for _, k := range keys {
		f := byKey[k]
		var hint string
		switch f.Type {
		case template.TypeDimension:
			hint = f.Guide
			if hint != "" {
				hint += "；"
			}
			hint += "从「" + f.Dimension + "」里选一个值，不确定可以选「不确定」（也算已填）"
		case template.TypeRadio:
			hint = "从选项里选一个：" + strings.Join(f.Options, "、")
			if f.Guide != "" {
				hint = f.Guide + "。" + hint
			}
		default:
			hint = f.Guide
		}
		if strings.TrimSpace(hint) == "" {
			hint = "这是必填项，请填写"
		}
		out = append(out, MissingFieldHint{Field: k, Hint: hint, Example: f.Example})
	}
	return out
}
