package tickets

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/labels"
)

// AliasResolution 是「写别名被换成标准值」的回执。
type AliasResolution struct {
	Requested string `json:"requested"`
	Label     string `json:"label"`
	Via       string `json:"via"`
}

// SetLabelsInput 是打标签 / 摘标签的输入。别名与近似写法会换成标准值；
// 值不在字典中时，AllowNew 为 true 才新建（维度必须已存在，新维度只能由管理员加）。
type SetLabelsInput struct {
	Ref      string
	Add      []string
	Remove   []string
	AllowNew bool
	Actor    identity.Viewer
}

// SetLabelsResult 是打标签 / 摘标签的结果：最终的标签集合、这次实际改了什么、
// 这次新建进了字典的值。
type SetLabelsResult struct {
	Labels                  []string
	Added                   []string
	Removed                 []string
	AliasResolved           []AliasResolution
	CreatedDictionaryValues []string
}

// SetLabels 以某个 Viewer 给工单打标签 / 摘标签。标签增删各记一条
// label_added / label_removed 时间线事件（访客不可见，#32 起过滤），
// 并推进全局变更序号。摘一个工单上没有的标签是幂等成功；解析不到的写法报 422。
func (m *Manager) SetLabels(ctx context.Context, in SetLabelsInput) (SetLabelsResult, error) {
	full, err := m.loadForViewer(ctx, in.Actor, in.Ref)
	if err != nil {
		return SetLabelsResult{}, err
	}

	type addPlan struct{ requested, label, via string }
	var adds []addPlan
	var unknown []labels.ApplyCandidate
	seenAdd := map[string]bool{}
	for _, q := range in.Add {
		q = strings.TrimSpace(q)
		if q == "" || seenAdd[q] {
			continue
		}
		seenAdd[q] = true
		cand, err := m.labels.ResolveAdd(ctx, q)
		if err != nil {
			return SetLabelsResult{}, err // 通配写法 / 多义 / 停用值
		}
		if cand.Via == "" {
			unknown = append(unknown, cand)
			continue
		}
		adds = append(adds, addPlan{requested: cand.Requested, label: cand.Resolved, via: cand.Via})
	}
	var toCreate []labels.ApplyCandidate
	if len(unknown) > 0 {
		if !in.AllowNew {
			c := unknown[0]
			return SetLabelsResult{}, &labels.UnknownValueError{Label: c.Requested, Suggestions: c.Suggestions}
		}
		for _, c := range unknown {
			if c.Ambiguous {
				return SetLabelsResult{}, &labels.UnknownValueError{Label: c.Requested, Suggestions: c.Suggestions}
			}
			dim, val, hasDim := labels.SplitLabel(c.Requested)
			if hasDim {
				known, err := m.labels.HasDimension(ctx, dim)
				if err != nil {
					return SetLabelsResult{}, err
				}
				if !known {
					return SetLabelsResult{}, labels.ErrUnknownDimension
				}
				c.Requested = dim + ":" + strings.TrimSpace(val)
			}
			toCreate = append(toCreate, c)
			adds = append(adds, addPlan{requested: c.Requested, label: c.Requested, via: "new"})
		}
	}

	var removes []string
	seenRemove := map[string]bool{}
	for _, q := range in.Remove {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		resolved, err := m.labels.ResolveRemove(ctx, q)
		if err != nil {
			return SetLabelsResult{}, err
		}
		if !seenRemove[resolved] {
			seenRemove[resolved] = true
			removes = append(removes, resolved)
		}
	}

	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return SetLabelsResult{}, err
	}
	defer tx.Rollback()

	result := SetLabelsResult{Labels: []string{}, Added: []string{}, Removed: []string{},
		AliasResolved: []AliasResolution{}, CreatedDictionaryValues: []string{}}
	for _, p := range adds {
		if p.label != p.requested {
			result.AliasResolved = append(result.AliasResolved,
				AliasResolution{Requested: p.requested, Label: p.label, Via: p.via})
		}
	}
	// allow_new 新建的字典值和工单标签在同一个事务里生效。
	for _, c := range toCreate {
		dim, val, hasDim := labels.SplitLabel(c.Requested)
		if hasDim {
			if err := m.labels.CreateValueTx(ctx, tx, dim, val); err != nil {
				return SetLabelsResult{}, err
			}
		} else if err := m.labels.CreatePlainTx(ctx, tx, c.Requested); err != nil {
			return SetLabelsResult{}, err
		}
		result.CreatedDictionaryValues = append(result.CreatedDictionaryValues, c.Requested)
	}

	// 当前标签 → 摘掉在单上的 → 加上不在单上的（先摘后加，同一次请求里
	// 又摘又加同一个标签时最终加上）。
	current, err := ticketLabelsTx(ctx, tx, full.Seq)
	if err != nil {
		return SetLabelsResult{}, err
	}
	onTicket := map[string]bool{}
	for _, l := range current {
		onTicket[l] = true
	}
	for _, l := range removes {
		if !onTicket[l] {
			continue // 幂等：摘一个工单上没有的标签是成功，不记事件
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM ticket_labels WHERE ticket_seq = ? AND label = ?`, full.Seq, l); err != nil {
			return SetLabelsResult{}, err
		}
		onTicket[l] = false
		result.Removed = append(result.Removed, l)
		if err := m.insertLabelEvent(ctx, tx, full.Seq, "label_removed", l, in.Actor, now); err != nil {
			return SetLabelsResult{}, err
		}
	}
	for _, p := range adds {
		if onTicket[p.label] {
			continue // 已经在单上：不重复打，也不记事件
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO ticket_labels (ticket_seq, label) VALUES (?, ?)`, full.Seq, p.label); err != nil {
			return SetLabelsResult{}, err
		}
		onTicket[p.label] = true
		result.Added = append(result.Added, p.label)
		if err := m.insertLabelEvent(ctx, tx, full.Seq, "label_added", p.label, in.Actor, now); err != nil {
			return SetLabelsResult{}, err
		}
	}
	if len(result.Added) == 0 && len(result.Removed) == 0 && len(toCreate) == 0 {
		// 什么都没改：不推进变更序号，直接返回当前标签。
		result.Labels = current
		return result, nil
	}
	var lastChange int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(change_seq), 0) FROM timeline_events`).Scan(&lastChange); err != nil {
		return SetLabelsResult{}, err
	}
	if err := touchLastChange(ctx, tx, full.Seq, lastChange); err != nil {
		return SetLabelsResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SetLabelsResult{}, err
	}
	result.Labels, err = m.ticketLabels(ctx, full.Seq)
	if err != nil {
		return SetLabelsResult{}, err
	}
	return result, nil
}

// ticketLabelsTx 在事务里读工单的当前标签。
func ticketLabelsTx(ctx context.Context, tx *sql.Tx, seq int64) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
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

// insertLabelEvent 写一条标签事件（label_added / label_removed，附加列 label），
// 复用 #30 的 appendEvent（单内 seq 与全局变更序号同事务分配），
// 附加列随后用 UPDATE 补上（与回复等事件类型同一模式）。
func (m *Manager) insertLabelEvent(ctx context.Context, tx *sql.Tx, ticketSeq int64,
	eventType, label string, actor identity.Viewer, at time.Time) error {
	eventSeq, _, err := m.appendEvent(ctx, tx, ticketSeq, at, authorOf(actor), eventType)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE timeline_events SET label = ? WHERE ticket_seq = ? AND seq = ?`,
		label, ticketSeq, eventSeq); err != nil {
		return fmt.Errorf("写入 %s 事件的 label 失败：%w", eventType, err)
	}
	return nil
}
