// 访客管理（#332）：成员查看访客详情、改名、停用与恢复。
// 查看详情只列调用者可见范围内的工单与链接；改名沿用“访客建立者、管理员
// 或名下任一链接的管理人”的管理边界；停用与恢复只接受网页成员会话。
package tickets

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/identity"
)

var (
	// ErrGuestNameRequired 表示改名时没有给出新的访客名字。
	ErrGuestNameRequired = errors.New("访客名字不能为空")
	// ErrGuestAlreadyDisabled 表示访客已经处于停用状态。
	ErrGuestAlreadyDisabled = errors.New("这位访客已经是停用状态")
	// ErrGuestNotDisabled 表示访客当前是正常状态，无需恢复。
	ErrGuestNotDisabled = errors.New("这位访客没有停用")
)

// GuestDetailTicket 是访客详情里的一张工单：工单概要加上这位访客名下
// 指向这张工单的全部链接。
type GuestDetailTicket struct {
	Seq            int64
	Number         string
	Title          string
	Status         string
	WaitingOnGuest bool
	WaitingReason  string
	Links          []GuestLinkInfo
}

// GuestDetailResult 是成员查看访客详情的结果；Tickets 已按调用者可见范围过滤。
type GuestDetailResult struct {
	Guest   GuestInfo
	Tickets []GuestDetailTicket
}

// GuestDetail 返回访客的名字、认领与停用状态，以及名下在 viewer 可见范围内的
// 工单和链接。部门共享的访客名单不能绕过工单可见范围（spec 用户故事 9）。
func (m *Manager) GuestDetail(ctx context.Context, viewer identity.Viewer, guestID int64) (GuestDetailResult, error) {
	guest, err := m.guestByID(ctx, guestID)
	if err != nil {
		return GuestDetailResult{}, err
	}
	visible, err := m.VisibleSeqs(ctx, viewer)
	if err != nil {
		return GuestDetailResult{}, err
	}
	visibleSet := make(map[int64]bool, len(visible))
	for _, seq := range visible {
		visibleSet[seq] = true
	}
	rows, err := m.db.QueryContext(ctx, `
		SELECT ticket_seq FROM guest_links WHERE guest_id = ? GROUP BY ticket_seq ORDER BY ticket_seq`,
		guestID)
	if err != nil {
		return GuestDetailResult{}, err
	}
	defer rows.Close()
	seqs := []int64{}
	for rows.Next() {
		var seq int64
		if err := rows.Scan(&seq); err != nil {
			return GuestDetailResult{}, err
		}
		if visibleSet[seq] {
			seqs = append(seqs, seq)
		}
	}
	if err := rows.Err(); err != nil {
		return GuestDetailResult{}, err
	}
	tickets := make([]GuestDetailTicket, 0, len(seqs))
	for _, seq := range seqs {
		full, err := m.ByRef(ctx, strconv.FormatInt(seq, 10))
		if err != nil {
			return GuestDetailResult{}, err
		}
		links, err := GuestLinksForTicket(ctx, m.db, seq)
		if err != nil {
			return GuestDetailResult{}, err
		}
		own := make([]GuestLinkInfo, 0, len(links))
		for _, l := range links {
			if l.GuestID == guestID {
				l.ShareTextAvailable = l.RevokedAt == nil && m.clk.Now().Before(l.ExpiresAt) &&
					m.guestLinkCipher != nil && m.guestLinkCipher.CanDecrypt(l.keyCiphertext)
				own = append(own, l)
			}
		}
		item := GuestDetailTicket{
			Seq: full.Seq, Number: full.Number, Title: full.Title, Status: full.Status, Links: own,
		}
		if turn := WaitingOnOf(full, own, m.clk.Now()); turn.Party == "guest" && full.Status != "closed" {
			item.WaitingOnGuest = true
			item.WaitingReason = guestDetailWaitingReason(turn)
		}
		tickets = append(tickets, item)
	}
	sortGuestDetailTickets(tickets)
	return GuestDetailResult{Guest: guest, Tickets: tickets}, nil
}

// guestDetailWaitingReason 把轮到访客的原因翻译给成员看：不是访客自己
// 看主页，而是成员在档案页理解“为什么置顶、TA 要做什么”。
func guestDetailWaitingReason(turn WaitingOn) string {
	if turn.Text == "访客补全说明" {
		return "等待访客补全说明"
	}
	return "已回复，等待访客答复"
}

// sortGuestDetailTickets 让轮到访客的工单排最前（同组新工单在前），
// 其他开启工单其次，已关闭工单最后；与访客主页的排序口径一致。
func sortGuestDetailTickets(items []GuestDetailTicket) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && guestDetailTicketLess(items[j], items[j-1]); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

func guestDetailTicketLess(a, b GuestDetailTicket) bool {
	ra, rb := guestDetailRank(a), guestDetailRank(b)
	if ra != rb {
		return ra < rb
	}
	return a.Seq > b.Seq
}

func guestDetailRank(item GuestDetailTicket) int {
	switch {
	case item.WaitingOnGuest:
		return 0
	case item.Status == "closed":
		return 2
	default:
		return 1
	}
}

// RenameGuest 给访客改名。新回复与时间线按写入时的名字快照署名，历史留痕
// 不改写；改名本身不写入工单时间线（spec「留痕」）。
func (m *Manager) RenameGuest(ctx context.Context, viewer identity.Viewer, guestID int64, name string) (GuestInfo, error) {
	if viewer.Kind != identity.ViewerMember {
		return GuestInfo{}, ErrGuestManagementNotAllowed
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return GuestInfo{}, ErrGuestNameRequired
	}
	if err := m.ensureGuestManageable(ctx, viewer, guestID); err != nil {
		return GuestInfo{}, err
	}
	if _, err := m.db.ExecContext(ctx, `UPDATE guests SET name = ? WHERE id = ?`, name, guestID); err != nil {
		return GuestInfo{}, err
	}
	return m.guestByID(ctx, guestID)
}

// DisableGuest 停用一位访客：主页与名下全部链接一起失效，但不改写各链接的
// 撤销与有效期字段。每张受影响的工单写一条 guest_disabled 事件，不发通知。
// 只接受网页成员会话（成员 Agent 被拒绝）。
func (m *Manager) DisableGuest(ctx context.Context, viewer identity.Viewer, guestID int64) error {
	return m.setGuestDisabled(ctx, viewer, guestID, true)
}

// EnableGuest 恢复已停用的访客：名下链接按各自原来的有效期重新生效。
func (m *Manager) EnableGuest(ctx context.Context, viewer identity.Viewer, guestID int64) error {
	return m.setGuestDisabled(ctx, viewer, guestID, false)
}

func (m *Manager) setGuestDisabled(ctx context.Context, viewer identity.Viewer, guestID int64, disable bool) error {
	if !viewer.IsWebSession() {
		return ErrGuestManagementNotAllowed
	}
	if err := m.ensureGuestManageable(ctx, viewer, guestID); err != nil {
		return err
	}
	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var disabledAt sql.NullString
	var mergedInto sql.NullInt64
	var guestName string
	if err := tx.QueryRowContext(ctx,
		`SELECT name, disabled_at, merged_into_guest_id FROM guests WHERE id = ?`, guestID).
		Scan(&guestName, &disabledAt, &mergedInto); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrGuestNotFound
		}
		return err
	}
	if mergedInto.Valid {
		if err := tx.Commit(); err != nil {
			return err
		}
		return ErrGuestAlreadyMerged
	}
	eventType := "guest_enabled"
	var newDisabled any
	if disable {
		eventType = "guest_disabled"
		newDisabled = now.Format(time.RFC3339Nano)
		if disabledAt.Valid {
			if err := tx.Commit(); err != nil {
				return err
			}
			return ErrGuestAlreadyDisabled
		}
	} else if !disabledAt.Valid {
		if err := tx.Commit(); err != nil {
			return err
		}
		return ErrGuestNotDisabled
	}
	if _, err := tx.ExecContext(ctx, `UPDATE guests SET disabled_at = ? WHERE id = ?`, newDisabled, guestID); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT ticket_seq, expires_at FROM guest_links
		WHERE guest_id = ? AND revoked_at IS NULL ORDER BY ticket_seq`, guestID)
	if err != nil {
		return err
	}
	seqs := []int64{}
	for rows.Next() {
		var seq int64
		var expiresAt string
		if err := rows.Scan(&seq, &expiresAt); err != nil {
			rows.Close()
			return err
		}
		expiry, err := parseTime(expiresAt)
		if err != nil {
			rows.Close()
			return err
		}
		// 只在这次操作真正改变链接可用性的工单上留痕：链接已撤销或已过期时，
		// 停用/恢复不影响它，不写事件。
		if !now.Before(expiry) {
			continue
		}
		seqs = append(seqs, seq)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	actor := authorOf(viewer)
	actor.GuestName = guestName
	for _, seq := range seqs {
		eventSeq, change, err := m.appendEvent(ctx, tx, seq, now, actor, eventType)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE timeline_events SET assign_to = ? WHERE ticket_seq = ? AND seq = ?`,
			guestName, seq, eventSeq); err != nil {
			return err
		}
		if err := touchLastChange(ctx, tx, seq, change); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// guestByID 读取一位访客的名单信息（含停用状态）。
func (m *Manager) guestByID(ctx context.Context, guestID int64) (GuestInfo, error) {
	guests, err := m.guestsQuery(ctx, " WHERE gu.id = ?", []any{guestID})
	if err != nil {
		return GuestInfo{}, err
	}
	if len(guests) == 1 {
		return guests[0], nil
	}
	return GuestInfo{}, ErrGuestNotFound
}
