// Package labels 是「标签字典」模块：预置维度、字典值与不带维度的标签。
// #29 只做「标签必须是字典中已有的值」这一条校验；完整的标签操作
// （allow_new、别名、停用、改名、合并）在后续工单 #31 加入。
package labels

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Manager 读写标签字典。
type Manager struct {
	db *sql.DB
}

func NewManager(db *sql.DB) *Manager { return &Manager{db: db} }

// UnknownValueError 表示一个标签不在字典中。
// Suggestions 是同维度（或不带维度标签）里的近似值，帮调用方自纠。
type UnknownValueError struct {
	Label       string
	Suggestions []string
}

func (e *UnknownValueError) Error() string {
	return fmt.Sprintf("标签字典中没有 %s", e.Label)
}

// Validate 校验 labels 里的每个标签都是字典中已有的值：
// `维度:值` 要求维度和值都已存在；不带维度的标签要求在 plain 标签里。
// 遇到第一个未知标签就返回 *UnknownValueError。
func (m *Manager) Validate(ctx context.Context, labels []string) error {
	for _, label := range labels {
		dim, val, hasDim := splitLabel(label)
		if !hasDim {
			var known int
			if err := m.db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM plain_labels WHERE label = ?`, label).Scan(&known); err != nil {
				return err
			}
			if known == 0 {
				plains, err := m.plainLabels(ctx)
				if err != nil {
					return err
				}
				return &UnknownValueError{Label: label, Suggestions: suggest(label, plains)}
			}
			continue
		}
		var known int
		if err := m.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM label_values WHERE dimension = ? AND value = ?`, dim, val).Scan(&known); err != nil {
			return err
		}
		if known > 0 {
			continue
		}
		values, err := m.DimensionValues(ctx, dim)
		if err != nil {
			return err
		}
		q := make([]string, len(values))
		for i, v := range values {
			q[i] = dim + ":" + v
		}
		return &UnknownValueError{Label: label, Suggestions: suggest(label, q)}
	}
	return nil
}

// DimensionValues 返回维度下已有的值；维度不存在时返回空列表（由调用方决定怎么报错）。
func (m *Manager) DimensionValues(ctx context.Context, dimension string) ([]string, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT value FROM label_values WHERE dimension = ? ORDER BY rowid`, dimension)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}

// DimensionNames 返回全部维度（hint 示例用）。
func (m *Manager) DimensionNames(ctx context.Context) ([]string, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT name FROM label_dimensions ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

func (m *Manager) plainLabels(ctx context.Context) ([]string, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT label FROM plain_labels ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var labels []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		labels = append(labels, l)
	}
	return labels, rows.Err()
}

// splitLabel 把「维度:值」拆开；没有冒号时 hasDim 为 false。
// 值里还可能出现冒号（如时间），只按第一个冒号拆。
func splitLabel(label string) (dim, val string, hasDim bool) {
	i := strings.Index(label, ":")
	if i < 0 {
		return "", label, false
	}
	return label[:i], label[i+1:], true
}

// norm 是标签匹配用的规范化：忽略大小写、空格、连字符和下划线
// （与 ticket-query 原型一致，E-203 能对上 E203）。
func norm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch r {
		case ' ', '-', '_', '\t':
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// suggest 挑出与 q 规范化后相等或互相包含的字典值（最多 5 个）。
func suggest(q string, values []string) []string {
	nq := norm(q)
	var out []string
	for _, v := range values {
		nv := norm(v)
		if nq == "" || nv == "" {
			continue
		}
		if nv == nq || strings.Contains(nv, nq) || strings.Contains(nq, nv) {
			out = append(out, v)
			if len(out) >= 5 {
				break
			}
		}
	}
	return out
}
