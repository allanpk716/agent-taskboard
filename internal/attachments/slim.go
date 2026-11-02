// 附件瘦身状态的推导（#281/#284）：工单级状态由附件状态现场推导，
// 不另存会漂移的冗余标记。
package attachments

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/allanpk716/agent-taskboard/internal/store"
	"sort"
	"strconv"
	"time"
)

// 瘦身状态：none = 没有已瘦身附件；partial = 部分附件已瘦身；
// full = 附件已全部瘦身。
const (
	SlimStatusNone    = "none"
	SlimStatusPartial = "partial"
	SlimStatusFull    = "full"
)

// SlimSummary 汇总一张工单的附件瘦身情况（只统计已挂上的附件；
// 待用附件还没挂到工单上，不参与推导）。
type SlimSummary struct {
	RemovedCount  int
	LastRemovedAt time.Time // 零值表示没有已瘦身附件
	Packages      []string
	Status        string
}

// InlineImageRefs 汇总一张工单的全部说明版本与回复里以插图语法引用的附件。
// 插图永不参与瘦身（见 ADR 0015），瘦身候选与瘦身状态推导都用它排除插图。
func (m *Manager) InlineImageRefs(ctx context.Context, ticketSeq int64) (map[string]bool, error) {
	type source struct {
		markdown string
		idsJSON  string
	}
	sources := []source{}
	briefRows, err := m.db.QueryContext(ctx, `
		SELECT brief_md, COALESCE(attachment_ids, '[]') FROM brief_versions
		WHERE ticket_seq = ?`, ticketSeq)
	if err != nil {
		return nil, err
	}
	for briefRows.Next() {
		var src source
		if err := briefRows.Scan(&src.markdown, &src.idsJSON); err != nil {
			briefRows.Close()
			return nil, err
		}
		sources = append(sources, src)
	}
	if err := briefRows.Err(); err != nil {
		briefRows.Close()
		return nil, err
	}
	briefRows.Close()

	replyRows, err := m.db.QueryContext(ctx, `
		SELECT COALESCE(body_md, ''), COALESCE(attachment_ids, '[]')
		FROM timeline_events WHERE ticket_seq = ? AND type = 'reply'`, ticketSeq)
	if err != nil {
		return nil, err
	}
	for replyRows.Next() {
		var src source
		if err := replyRows.Scan(&src.markdown, &src.idsJSON); err != nil {
			replyRows.Close()
			return nil, err
		}
		sources = append(sources, src)
	}
	if err := replyRows.Err(); err != nil {
		replyRows.Close()
		return nil, err
	}
	replyRows.Close()

	out := map[string]bool{}
	for _, src := range sources {
		var ids []string
		if err := json.Unmarshal([]byte(src.idsJSON), &ids); err != nil {
			return nil, fmt.Errorf("解析工单 %d 的附件引用失败：%w", ticketSeq, err)
		}
		for _, ref := range ParsePostRefs(src.markdown, ids) {
			out[ref.ID] = out[ref.ID] || ref.ImageSyntax
		}
	}
	return out, nil
}

// InlineImage 判断一个已挂上的附件是否是插图（插图语法引用且扩展名是图片）。
// 瘦身候选与瘦身状态分母共用这一定义。
func InlineImage(inlineRefs map[string]bool, ref, filename string) bool {
	return inlineRefs[ref] && IsImage(filename)
}

// slimStatus 由「附件总数（不含插图）」与「已瘦身附件数」推导工单级状态。
func slimStatus(total, removed int) string {
	switch {
	case removed == 0:
		return SlimStatusNone
	case removed == total:
		return SlimStatusFull
	default:
		return SlimStatusPartial
	}
}

// SlimSummaryOf 从一张工单的附件列表推导瘦身汇总。分母只算附件，
// 插图不计入（与瘦身候选的口径一致）。
func (m *Manager) SlimSummaryOf(ctx context.Context, atts []AttachmentInfo) (SlimSummary, error) {
	summary := SlimSummary{Status: SlimStatusNone, Packages: []string{}}
	if len(atts) == 0 {
		return summary, nil
	}
	inline, err := m.InlineImageRefs(ctx, atts[0].TicketSeq)
	if err != nil {
		return SlimSummary{}, err
	}
	total := 0
	seen := map[string]bool{}
	for _, att := range atts {
		if att.State != "attached" {
			continue
		}
		if InlineImage(inline, att.Ref(), att.Filename) {
			continue
		}
		total++
		if att.SlimRemovedAt == nil {
			continue
		}
		summary.RemovedCount++
		if att.SlimRemovedAt.After(summary.LastRemovedAt) {
			summary.LastRemovedAt = *att.SlimRemovedAt
		}
		if att.SlimPackageName != "" && !seen[att.SlimPackageName] {
			seen[att.SlimPackageName] = true
			summary.Packages = append(summary.Packages, att.SlimPackageName)
		}
	}
	summary.Status = slimStatus(total, summary.RemovedCount)
	sort.Strings(summary.Packages)
	return summary, nil
}

// SlimStatuses 批量推导一组工单的瘦身状态（列表行内标记用）。
// 分母与 SlimSummaryOf 一致：只算附件，不算插图。
func (m *Manager) SlimStatuses(ctx context.Context, seqs []int64) (map[int64]string, error) {
	statuses := make(map[int64]string, len(seqs))
	if len(seqs) == 0 {
		return statuses, nil
	}
	type attachmentRow struct {
		id       int64
		filename string
		removed  bool
	}
	args := make([]any, 0, len(seqs))
	for _, seq := range seqs {
		args = append(args, seq)
	}
	bySeq := make(map[int64][]attachmentRow, len(seqs))
	rows, err := m.db.QueryContext(ctx, `
		SELECT ticket_seq, id, filename, slim_removed_at IS NOT NULL
		FROM attachments
		WHERE state = 'attached' AND ticket_seq IN (`+store.Placeholders(len(args))+`)
		ORDER BY ticket_seq, id`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var seq int64
		var att attachmentRow
		if err := rows.Scan(&seq, &att.id, &att.filename, &att.removed); err != nil {
			rows.Close()
			return nil, err
		}
		bySeq[seq] = append(bySeq[seq], att)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	for _, seq := range seqs {
		atts, ok := bySeq[seq]
		if !ok {
			statuses[seq] = SlimStatusNone
			continue
		}
		inline, err := m.InlineImageRefs(ctx, seq)
		if err != nil {
			return nil, err
		}
		total, removed := 0, 0
		for _, att := range atts {
			if InlineImage(inline, "att_"+strconv.FormatInt(att.id, 10), att.filename) {
				continue
			}
			total++
			if att.removed {
				removed++
			}
		}
		statuses[seq] = slimStatus(total, removed)
	}
	return statuses, nil
}
