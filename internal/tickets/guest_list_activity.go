// 访客名单行的活动统计：最近联系、最近发送、名下工单数与待处理数。
package tickets

import (
	"context"
	"strconv"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// fillGuestListActivity 补齐名单行的活动统计与待处理数。最近联系取访客经
// 名下链接打开查看者可见工单的最近一次访问记录；最近发送取名下可见工单上
// 访客链接的最近创建时间；待处理数只数查看者可见、未关闭且当前轮到访客的
// 工单（ADR 0019：访客聚合不能绕过工单可见范围）。
func (m *Manager) fillGuestListActivity(ctx context.Context, viewer identity.Viewer, guests []GuestInfo) error {
	placeholders := make([]string, len(guests))
	idArgs := make([]any, len(guests))
	byID := make(map[int64]*GuestInfo, len(guests))
	for i := range guests {
		placeholders[i] = "?"
		idArgs[i] = guests[i].ID
		byID[guests[i].ID] = &guests[i]
	}
	inList := strings.Join(placeholders, ",")
	visible, err := m.VisibleSeqs(ctx, viewer)
	if err != nil {
		return err
	}
	visiblePlaceholders := make([]string, len(visible))
	visibleArgs := make([]any, len(visible))
	for i, seq := range visible {
		visiblePlaceholders[i] = "?"
		visibleArgs[i] = seq
	}
	visibleList := strings.Join(visiblePlaceholders, ",")
	activityArgs := append(idArgs, visibleArgs...)

	rows, err := m.db.QueryContext(ctx, `
		SELECT l.guest_id, MAX(r.last_at)
		FROM access_records r
		JOIN guest_links l ON l.id = r.actor_link_id
		JOIN tickets t ON t.seq = l.ticket_seq
		WHERE r.actor_kind = 'guest' AND l.guest_id IN (`+inList+`)
		  AND l.ticket_seq IN (`+visibleList+`)
		GROUP BY l.guest_id`, activityArgs...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var guestID int64
		var lastAt string
		if err := rows.Scan(&guestID, &lastAt); err != nil {
			return err
		}
		at, err := parseTime(lastAt)
		if err != nil {
			return err
		}
		if g, ok := byID[guestID]; ok {
			g.LastContactAt = &at
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	sentRows, err := m.db.QueryContext(ctx, `
		SELECT l.guest_id, MAX(l.created_at), COUNT(DISTINCT l.ticket_seq)
		FROM guest_links l
		JOIN tickets t ON t.seq = l.ticket_seq
		WHERE l.guest_id IN (`+inList+`)
		  AND l.ticket_seq IN (`+visibleList+`)
		GROUP BY l.guest_id`, activityArgs...)
	if err != nil {
		return err
	}
	defer sentRows.Close()
	for sentRows.Next() {
		var guestID int64
		var sentAt string
		var ticketCount int
		if err := sentRows.Scan(&guestID, &sentAt, &ticketCount); err != nil {
			return err
		}
		at, err := parseTime(sentAt)
		if err != nil {
			return err
		}
		if g, ok := byID[guestID]; ok {
			g.TicketCount = ticketCount
			g.LastSentAt = &at
		}
	}
	if err := sentRows.Err(); err != nil {
		return err
	}
	sentRows.Close()

	seqRows, err := m.db.QueryContext(ctx, `
		SELECT DISTINCT l.guest_id, l.ticket_seq
		FROM guest_links l
		JOIN tickets t ON t.seq = l.ticket_seq
		WHERE l.revoked_at IS NULL AND l.guest_id IN (`+inList+`)
		  AND l.ticket_seq IN (`+visibleList+`)
		ORDER BY l.guest_id, l.ticket_seq`, activityArgs...)
	if err != nil {
		return err
	}
	seqsByGuest := map[int64][]int64{}
	allSeqs := map[int64]bool{}
	for seqRows.Next() {
		var guestID, seq int64
		if err := seqRows.Scan(&guestID, &seq); err != nil {
			seqRows.Close()
			return err
		}
		seqsByGuest[guestID] = append(seqsByGuest[guestID], seq)
		allSeqs[seq] = true
	}
	if err := seqRows.Err(); err != nil {
		seqRows.Close()
		return err
	}
	seqRows.Close()
	if len(allSeqs) == 0 {
		return nil
	}

	now := m.clk.Now()
	fullBySeq := map[int64]Full{}
	linksBySeq := map[int64][]GuestLinkInfo{}
	for guestID, seqs := range seqsByGuest {
		pending := 0
		for _, seq := range seqs {
			full, ok := fullBySeq[seq]
			if !ok {
				full, err = m.ByRef(ctx, strconv.FormatInt(seq, 10))
				if err != nil {
					return err
				}
				fullBySeq[seq] = full
			}
			own, ok := linksBySeq[seq]
			if !ok {
				own, err = GuestLinksForTicket(ctx, m.db, seq)
				if err != nil {
					return err
				}
				linksBySeq[seq] = own
			}
			guestLinks := make([]GuestLinkInfo, 0, len(own))
			for _, l := range own {
				if l.GuestID == guestID {
					guestLinks = append(guestLinks, l)
				}
			}
			if turn := WaitingOnOf(full, guestLinks, now); guestWaitingOn(turn, full) {
				pending++
			}
		}
		if g, ok := byID[guestID]; ok {
			g.PendingCount = pending
		}
	}
	return nil
}
