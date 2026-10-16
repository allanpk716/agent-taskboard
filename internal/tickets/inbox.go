// 收件箱（#36）：列出「现在轮到我」的开启工单。收件箱属于成员——同一成员
// 名下所有 Agent 共用一份；等待方与原因都由时间线推导（WaitingOnOf），
// 不存会漂移的状态。未指派的工单不会出现在任何人的收件箱里。
package tickets

import (
	"context"
	"strconv"
)

// InboxItem 是收件箱的一项：轮到我的工单 + 原因 + 增量读取地址。
type InboxItem struct {
	Number   string
	Title    string
	Role     string // assignee | creator（我在这张工单里的角色）
	Reason   string // WHY 文案：新指派给你 / 已交付，待你验收 / …
	SinceSeq int64  // 推导所依据的回复事件 seq；0 = 无相关回复（新指派）
	ReadNew  string // /api/v1/tickets/<编号>/timeline?after=<我在该单最后一条事件的 seq>
	ReadFull string // /api/v1/tickets/<编号>?format=markdown
}

// Inbox 列出 memberID 当前轮到的全部开启工单（按工单序号）。
// read_new 的 after 是我在该单最后一条任意事件（不只回复）的 seq——
// 收件箱项要读的是「上次我看过之后别人做了什么」。
func (m *Manager) Inbox(ctx context.Context, memberID int64) ([]InboxItem, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT seq FROM tickets WHERE status = 'open' ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var seqs []int64
	for rows.Next() {
		var seq int64
		if err := rows.Scan(&seq); err != nil {
			return nil, err
		}
		seqs = append(seqs, seq)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	items := []InboxItem{}
	for _, seq := range seqs {
		full, err := m.ByRef(ctx, strconv.FormatInt(seq, 10))
		if err != nil {
			return nil, err
		}
		turn := WaitingOnOf(full)
		if turn.MemberID != memberID {
			continue
		}
		after := int64(0)
		for _, e := range full.Timeline {
			if e.Actor.MemberID == memberID && e.Seq > after {
				after = e.Seq
			}
		}
		role := "creator"
		if turn.Party == "assignee" {
			role = "assignee"
		}
		items = append(items, InboxItem{
			Number:   full.Number,
			Title:    full.Title,
			Role:     role,
			Reason:   WhyText(turn.Why),
			SinceSeq: turn.SinceSeq,
			ReadNew:  "/api/v1/tickets/" + full.Number + "/timeline?after=" + strconv.FormatInt(after, 10),
			ReadFull: "/api/v1/tickets/" + full.Number + "?format=markdown",
		})
	}
	return items, nil
}
