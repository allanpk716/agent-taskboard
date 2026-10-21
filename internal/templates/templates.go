// Package templates 是「模板」模块的存储侧：版本管理（每次修改产生新版本、
// 记录修改人）、停用（不能删除）、预置模板种子。解析与渲染的纯逻辑在
// internal/template。
package templates

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/template"
)

// 模板维护的错误；HTTP 层把它们映射成错误码。
var (
	// ErrNotFound 表示引用的模板不存在。
	ErrNotFound = errors.New("没有这个模板")
	// ErrInactive 表示模板已停用，不能再用它建单。
	ErrInactive = errors.New("模板已停用")
	// ErrKeyTaken 表示模板 key 已存在（模板 key 不能复用，历史工单还引用着）。
	ErrKeyTaken = errors.New("模板 key 已存在")
	// ErrKeyRequired 表示创建模板时没写 key。
	ErrKeyRequired = errors.New("模板 key 不能为空")
	// ErrInvalidKey 表示 key 里有不适合放进 URL 的字符。
	ErrInvalidKey = errors.New("模板 key 不能包含空格或 /")
	// ErrMarkdownRequired 表示没写模板内容。
	ErrMarkdownRequired = errors.New("模板内容（markdown）不能为空")
)

// Updater 是一次修改的作者（记录每个版本的修改人）。
type Updater struct {
	MemberID   int64
	MemberName string
	AgentID    int64
	AgentName  string
}

// Template 是一份模板的当前版本投影。
type Template struct {
	Key     string
	Version int64
	Active  bool
	template.Definition
	Markdown  string
	UpdatedBy Updater
	UpdatedAt time.Time
}

// Manager 读写模板。
type Manager struct {
	db  *sql.DB
	clk clock.Clock
}

func NewManager(db *sql.DB, c clock.Clock) *Manager { return &Manager{db: db, clk: c} }

// Create 解析并新建一份模板（版本 1）。任何成员（含成员 Agent）都能新建；
// 格式错误（带行号）、key 冲突在这里报出。
func (m *Manager) Create(ctx context.Context, key, markdown string, by identity.Viewer) (Template, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return Template{}, ErrKeyRequired
	}
	if strings.ContainsAny(key, " \t\r\n/") {
		return Template{}, ErrInvalidKey
	}
	if strings.TrimSpace(markdown) == "" {
		return Template{}, ErrMarkdownRequired
	}
	def, err := template.Parse(markdown)
	if err != nil {
		return Template{}, err
	}
	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Template{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO templates (key, active, current_version) VALUES (?, 1, 1)`, key); err != nil {
		return Template{}, ErrKeyTaken
	}
	t := Template{Key: key, Version: 1, Active: true, Definition: def, Markdown: markdown,
		UpdatedBy: updaterOf(by), UpdatedAt: now}
	if err := insertVersion(ctx, tx, t, now); err != nil {
		return Template{}, err
	}
	return t, tx.Commit()
}

// Update 修改模板：解析新内容，产生新版本并记录修改人；已有工单不受影响
// （它们保存的是创建时版本的快照）。
func (m *Manager) Update(ctx context.Context, key, markdown string, by identity.Viewer) (Template, error) {
	if strings.TrimSpace(markdown) == "" {
		return Template{}, ErrMarkdownRequired
	}
	def, err := template.Parse(markdown)
	if err != nil {
		return Template{}, err
	}
	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Template{}, err
	}
	defer tx.Rollback()
	var current int64
	var active int
	if err := tx.QueryRowContext(ctx,
		`SELECT current_version, active FROM templates WHERE key = ?`, key).Scan(&current, &active); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Template{}, ErrNotFound
		}
		return Template{}, err
	}
	next := current + 1
	if _, err := tx.ExecContext(ctx,
		`UPDATE templates SET current_version = ? WHERE key = ?`, next, key); err != nil {
		return Template{}, err
	}
	// 修改已停用的模板不把它重新启用（模板只能停用，没有启用操作）。
	t := Template{Key: key, Version: next, Active: active == 1, Definition: def, Markdown: markdown,
		UpdatedBy: updaterOf(by), UpdatedAt: now}
	if err := insertVersion(ctx, tx, t, now); err != nil {
		return Template{}, err
	}
	return t, tx.Commit()
}

// Deactivate 停用模板（只能停用，不能删除；幂等）。停用后不能用模板建单，
// 历史工单的字段定义不受影响。
func (m *Manager) Deactivate(ctx context.Context, key string) (bool, error) {
	res, err := m.db.ExecContext(ctx, `UPDATE templates SET active = 0 WHERE key = ? AND active = 1`, key)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		// 区分「不存在」与「本来就停用」。
		var known int
		if err := m.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM templates WHERE key = ?`, key).Scan(&known); err != nil {
			return false, err
		}
		if known == 0 {
			return false, ErrNotFound
		}
		return false, nil
	}
	return true, nil
}

// List 列出全部模板（含已停用，带 active 标记），按 key 排序。
func (m *Manager) List(ctx context.Context) ([]Template, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT t.key, t.active, v.version, v.name, v.kind, v.scenario, v.preset_labels, v.agent_note,
		       v.fields, v.groups, v.markdown, v.updated_by_member_id, um.name, v.updated_by_agent_id, ua.name, v.updated_at
		FROM templates t
		JOIN template_versions v ON v.template_key = t.key AND v.version = t.current_version
		LEFT JOIN members um ON um.id = v.updated_by_member_id
		LEFT JOIN agent_tokens ua ON ua.id = v.updated_by_agent_id
		ORDER BY t.key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Template
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Get 读一份模板的当前版本；不存在时返回 ErrNotFound。
func (m *Manager) Get(ctx context.Context, key string) (Template, error) {
	row := m.db.QueryRowContext(ctx, `
		SELECT t.key, t.active, v.version, v.name, v.kind, v.scenario, v.preset_labels, v.agent_note,
		       v.fields, v.groups, v.markdown, v.updated_by_member_id, um.name, v.updated_by_agent_id, ua.name, v.updated_at
		FROM templates t
		JOIN template_versions v ON v.template_key = t.key AND v.version = t.current_version
		LEFT JOIN members um ON um.id = v.updated_by_member_id
		LEFT JOIN agent_tokens ua ON ua.id = v.updated_by_agent_id
		WHERE t.key = ?`, key)
	t, err := scanTemplate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Template{}, ErrNotFound
	}
	return t, err
}

// VersionInfo 是模板历史版本的一项（#42 模板维护页：查看历史版本）。
type VersionInfo struct {
	Version   int64
	Name      string
	Markdown  string
	UpdatedBy Updater
	UpdatedAt time.Time
}

// Versions 列出一份模板的全部历史版本（新→旧，含当前版本），每版带修改人
// 与模板原文；模板不存在时返回 ErrNotFound。
func (m *Manager) Versions(ctx context.Context, key string) ([]VersionInfo, error) {
	var exists int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM templates WHERE key = ?`, key).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, ErrNotFound
	}
	rows, err := m.db.QueryContext(ctx, `
		SELECT v.version, v.name, v.markdown, v.updated_by_member_id, um.name, v.updated_by_agent_id, ua.name, v.updated_at
		FROM template_versions v
		LEFT JOIN members um ON um.id = v.updated_by_member_id
		LEFT JOIN agent_tokens ua ON ua.id = v.updated_by_agent_id
		WHERE v.template_key = ?
		ORDER BY version DESC`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VersionInfo{}
	for rows.Next() {
		var (
			v         VersionInfo
			updatedAt string
			memberID  sql.NullInt64
			member    sql.NullString
			agentID   sql.NullInt64
			agent     sql.NullString
		)
		if err := rows.Scan(&v.Version, &v.Name, &v.Markdown, &memberID, &member, &agentID, &agent, &updatedAt); err != nil {
			return nil, err
		}
		v.UpdatedBy = Updater{MemberID: memberID.Int64, MemberName: member.String, AgentID: agentID.Int64, AgentName: agent.String}
		if v.UpdatedAt, err = time.ParseInLocation(time.RFC3339Nano, updatedAt, time.Local); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// scanner 兼容 *sql.Row 与 *sql.Rows。
type scanner interface{ Scan(dest ...any) error }

func scanTemplate(row scanner) (Template, error) {
	var (
		t         Template
		active    int
		presets   string
		fields    string
		groups    string
		memberID  sql.NullInt64
		member    sql.NullString
		agentID   sql.NullInt64
		agent     sql.NullString
		updatedAt string
	)
	if err := row.Scan(&t.Key, &active, &t.Version, &t.Name, &t.Kind, &t.Scenario, &presets, &t.AgentNote,
		&fields, &groups, &t.Markdown, &memberID, &member, &agentID, &agent, &updatedAt); err != nil {
		return Template{}, err
	}
	t.Active = active == 1
	t.UpdatedBy = Updater{MemberID: memberID.Int64, MemberName: member.String,
		AgentID: agentID.Int64, AgentName: agent.String}
	var err error
	if t.UpdatedAt, err = time.ParseInLocation(time.RFC3339Nano, updatedAt, clock.Beijing); err != nil {
		return Template{}, fmt.Errorf("解析模板更新时间 %q 失败：%w", updatedAt, err)
	}
	if err := json.Unmarshal([]byte(presets), &t.PresetLabels); err != nil {
		return Template{}, fmt.Errorf("解析模板 %s 的预设标签失败：%w", t.Key, err)
	}
	if err := json.Unmarshal([]byte(fields), &t.Fields); err != nil {
		return Template{}, fmt.Errorf("解析模板 %s 的字段定义失败：%w", t.Key, err)
	}
	if err := json.Unmarshal([]byte(groups), &t.Groups); err != nil {
		return Template{}, fmt.Errorf("解析模板 %s 的分组失败：%w", t.Key, err)
	}
	return t, nil
}

func insertVersion(ctx context.Context, tx *sql.Tx, t Template, at time.Time) error {
	presets, err := json.Marshal(t.PresetLabels)
	if err != nil {
		return err
	}
	fields, err := json.Marshal(t.Fields)
	if err != nil {
		return err
	}
	groups, err := json.Marshal(nonNilGroups(t.Groups))
	if err != nil {
		return err
	}
	var agentID, memberID any
	if t.UpdatedBy.AgentID != 0 {
		agentID = t.UpdatedBy.AgentID
	}
	if t.UpdatedBy.MemberID != 0 {
		memberID = t.UpdatedBy.MemberID
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO template_versions (template_key, version, name, kind, scenario, preset_labels, agent_note,
		                               fields, groups, markdown, updated_by_member_id, updated_by_agent_id, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.Key, t.Version, t.Name, t.Kind, t.Scenario, string(presets), t.AgentNote,
		string(fields), string(groups), t.Markdown, memberID, agentID, at.Format(time.RFC3339Nano))
	return err
}

// nonNilGroups 把 nil 分组写成空数组（不分组时存 []，不存 null）。
func nonNilGroups(g []template.Group) []template.Group {
	if g == nil {
		return []template.Group{}
	}
	return g
}

func updaterOf(v identity.Viewer) Updater {
	u := Updater{MemberID: v.Member.ID, MemberName: v.Member.Name}
	if v.Agent != nil {
		u.AgentID = v.Agent.ID
		u.AgentName = v.Agent.Name
	}
	return u
}
