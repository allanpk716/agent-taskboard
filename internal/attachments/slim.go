// 附件瘦身状态的推导（#281/#284）：工单级状态由附件状态现场推导，
// 不另存会漂移的冗余标记。
package attachments

import (
	"context"
	"sort"
	"strings"
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

// SlimSummaryOf 从一张工单的附件列表推导瘦身汇总。
func SlimSummaryOf(atts []AttachmentInfo) SlimSummary {
	summary := SlimSummary{Status: SlimStatusNone, Packages: []string{}}
	total := 0
	seen := map[string]bool{}
	for _, att := range atts {
		if att.State != "attached" {
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
	switch {
	case summary.RemovedCount == 0:
	case summary.RemovedCount == total:
		summary.Status = SlimStatusFull
	default:
		summary.Status = SlimStatusPartial
	}
	sort.Strings(summary.Packages)
	return summary
}

// SlimStatuses 批量推导一组工单的瘦身状态（列表行内标记用）。
func (m *Manager) SlimStatuses(ctx context.Context, seqs []int64) (map[int64]string, error) {
	statuses := make(map[int64]string, len(seqs))
	if len(seqs) == 0 {
		return statuses, nil
	}
	args := make([]any, 0, len(seqs))
	for _, seq := range seqs {
		args = append(args, seq)
	}
	rows, err := m.db.QueryContext(ctx, `
		SELECT ticket_seq, COUNT(*),
		       COALESCE(SUM(CASE WHEN slim_removed_at IS NOT NULL THEN 1 ELSE 0 END), 0)
		FROM attachments
		WHERE state = 'attached' AND ticket_seq IN (`+placeholders(len(args))+`)
		GROUP BY ticket_seq`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var seq, total, removed int64
		if err := rows.Scan(&seq, &total, &removed); err != nil {
			return nil, err
		}
		switch {
		case removed == 0:
			statuses[seq] = SlimStatusNone
		case removed == total:
			statuses[seq] = SlimStatusFull
		default:
			statuses[seq] = SlimStatusPartial
		}
	}
	return statuses, rows.Err()
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
