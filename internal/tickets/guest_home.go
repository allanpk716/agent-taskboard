// 访客主页（#329）：长期主页密钥、访客级认领与“名下仍可参与的工单”列表。
// 主页本身不指向单张工单，只打开主页不写任何访问记录。
package tickets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/guestcredentials"
	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// GuestHomeShare 是成员重复复制主页文案的结果。Key 只在本结果中短暂还原，
// 数据库仍只保存哈希与密文。
type GuestHomeShare struct {
	GuestID   int64
	GuestName string
	Key       string
}

// GuestHomeTicket 是主页列表项。WaitingOn 表示当前是否轮到这位访客；
// Reason 是给访客看的行动原因；Closed 工单 ReadOnly。
type GuestHomeTicket struct {
	Seq          int64
	Number       string
	Title        string
	Status       string
	API          string
	WaitingOn    bool
	Reason       string
	ReadOnly     bool
	ExpiresAt    time.Time
	CanEditBrief bool
}

// GuestHomeShareText 生成成员发给访客的主页文案。它带主页凭证但不带访问码，
// 可随时重复生成。
func GuestHomeShareText(name, url string) string {
	return name + "：这是你的 agent-taskboard 工单主页：" + url +
		" 。以后我发给你的工单都会列在这里；第一次打开请确认是你本人，并保存只显示一次的访问码。" +
		"主页只归你一个人，请不要转发；换设备时输入访问码即可重新进入。"
}

// GuestHomeShare 确保这位访客有主页密钥并还原明文，供成员重复复制主页文案。
func (m *Manager) GuestHomeShare(ctx context.Context, guestID int64) (GuestHomeShare, error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return GuestHomeShare{}, err
	}
	defer tx.Rollback()
	share, err := m.ensureGuestHomeKeyTx(ctx, tx, guestID)
	if err != nil {
		return GuestHomeShare{}, err
	}
	if share.Key == "" {
		return GuestHomeShare{}, ErrGuestLinkCipherUnavailable
	}
	if err := tx.Commit(); err != nil {
		return GuestHomeShare{}, err
	}
	return share, nil
}

// GuestHomeShareForViewer 先核对“访客建立者、管理员或名下任一链接管理人”
// 的管理边界，再还原主页密钥；部门共享的访客名单不能变成绕过工单可见
// 范围的凭证出口。
func (m *Manager) GuestHomeShareForViewer(ctx context.Context, viewer identity.Viewer, guestID int64) (GuestHomeShare, error) {
	if viewer.Kind != identity.ViewerMember {
		return GuestHomeShare{}, ErrGuestManagementNotAllowed
	}
	if err := m.guestManagementAllowed(ctx, viewer, guestID); err != nil {
		return GuestHomeShare{}, err
	}
	return m.GuestHomeShare(ctx, guestID)
}

// guestManagementAllowed 判断 viewer 是否属于“访客建立者、管理员，或其名下
// 任一链接的管理人”。链接管理人沿用链接管理口径：创建该链接，或工单在
// 自己可见范围内。无关成员统一返回 ErrGuestManagementNotAllowed。
func (m *Manager) guestManagementAllowed(ctx context.Context, viewer identity.Viewer, guestID int64) error {
	var createdByMember, createdByAgent int64
	err := m.db.QueryRowContext(ctx, `
		SELECT created_by_member_id, COALESCE(created_by_agent_id, 0)
		FROM guests WHERE id = ?`, guestID).Scan(&createdByMember, &createdByAgent)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrGuestNotFound
	}
	if err != nil {
		return err
	}
	memberID := viewer.Member.ID
	allowed := viewer.Member.IsAdmin || createdByMember == memberID ||
		(viewer.Agent != nil && createdByAgent == viewer.Agent.ID)
	if allowed {
		return nil
	}
	rows, err := m.db.QueryContext(ctx, `
		SELECT ticket_seq, created_by_member_id FROM guest_links
		WHERE guest_id = ? AND revoked_at IS NULL`, guestID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type candidate struct {
		ticketSeq int64
		createdBy int64
	}
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.ticketSeq, &c.createdBy); err != nil {
			return err
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, c := range candidates {
		if c.createdBy == memberID {
			return nil
		}
		full, err := m.ByRef(ctx, strconv.FormatInt(c.ticketSeq, 10))
		if err != nil {
			return err
		}
		visible, err := m.CanSee(ctx, viewer, full)
		if err != nil {
			return err
		}
		if visible {
			return nil
		}
	}
	return ErrGuestManagementNotAllowed
}

// ensureGuestHomeKeyTx 生成或还原一位访客的主页密钥。老迁移数据没有密钥；
// 当前加密密钥不可用时保持空值，等密钥可用后再生成，不用不可恢复的地址。
func (m *Manager) ensureGuestHomeKeyTx(ctx context.Context, tx *sql.Tx, guestID int64) (GuestHomeShare, error) {
	var (
		share      GuestHomeShare
		disabledAt sql.NullString
		hash       sql.NullString
		ciphertext sql.NullString
	)
	err := tx.QueryRowContext(ctx, `
		SELECT id, name, disabled_at, home_key_hash, home_key_ciphertext
		FROM guests WHERE id = ?`, guestID).
		Scan(&share.GuestID, &share.GuestName, &disabledAt, &hash, &ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return GuestHomeShare{}, ErrGuestNotFound
	}
	if err != nil {
		return GuestHomeShare{}, err
	}
	if disabledAt.Valid {
		return GuestHomeShare{}, ErrGuestDisabled
	}
	if !hash.Valid && !ciphertext.Valid {
		if m.guestLinkCipher == nil {
			return share, nil
		}
		key, err := identity.NewGuestHomeKey()
		if err != nil {
			return GuestHomeShare{}, err
		}
		encrypted, err := m.guestLinkCipher.Encrypt(key)
		if err != nil {
			return GuestHomeShare{}, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE guests SET home_key_hash = ?, home_key_ciphertext = ?
			WHERE id = ? AND home_key_hash IS NULL`,
			identity.CredentialHash(key), encrypted, guestID); err != nil {
			return GuestHomeShare{}, fmt.Errorf("写入访客主页密钥失败：%w", err)
		}
		share.Key = key
		return share, nil
	}
	if m.guestLinkCipher == nil || !ciphertext.Valid {
		return GuestHomeShare{}, ErrGuestShareTextUnavailable
	}
	key, err := m.guestLinkCipher.Decrypt(ciphertext.String)
	if err != nil || identity.CredentialHash(key) != hash.String {
		return GuestHomeShare{}, ErrGuestShareTextUnavailable
	}
	share.Key = key
	return share, nil
}

// ClaimGuestHome 认领整个长期访客身份并签发一台设备凭证。主页不属于任何
// 一张工单，因此这里不写工单时间线。
func (m *Manager) ClaimGuestHome(ctx context.Context, home identity.GuestHome, authorName string) (GuestClaimResult, error) {
	authorName = trimClaimName(authorName)
	if authorName == "" {
		return GuestClaimResult{}, ErrClaimNameRequired
	}
	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return GuestClaimResult{}, err
	}
	defer tx.Rollback()
	claim, err := guestcredentials.Claim(ctx, tx, home.CredentialHolderID, authorName, now)
	if err != nil {
		switch {
		case errors.Is(err, guestcredentials.ErrClaimNameRequired):
			return GuestClaimResult{}, ErrClaimNameRequired
		case errors.Is(err, guestcredentials.ErrAlreadyClaimed):
			return GuestClaimResult{}, ErrLinkAlreadyClaimed
		}
		return GuestClaimResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return GuestClaimResult{}, err
	}
	return GuestClaimResult{DeviceToken: claim.DeviceToken, AccessCode: claim.AccessCode, ClaimedAt: now}, nil
}

// EnterGuestHome 用访客级访问码给一台新设备签发凭证。
func (m *Manager) EnterGuestHome(ctx context.Context, home identity.GuestHome, code string) (GuestClaimResult, error) {
	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return GuestClaimResult{}, err
	}
	defer tx.Rollback()
	if _, err := verifyGuestHomeAccessCodeTx(ctx, tx, home, code, now); err != nil {
		_ = tx.Commit()
		return GuestClaimResult{}, err
	}
	token, err := guestcredentials.IssueDevice(ctx, tx, home.CredentialHolderID, "access_code", now)
	if err != nil {
		return GuestClaimResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return GuestClaimResult{}, err
	}
	return GuestClaimResult{DeviceToken: token, ClaimedAt: now}, nil
}

// ViewerFromGuestHomeAgentCredential 校验「主页密钥:访问码」的 Agent 凭证。
// 不签发设备凭证；错误计数与浏览器和链接 Agent 共用。
func (m *Manager) ViewerFromGuestHomeAgentCredential(ctx context.Context, home identity.GuestHome, code string) (identity.Viewer, error) {
	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return identity.Viewer{}, err
	}
	defer tx.Rollback()
	if _, err := verifyGuestHomeAccessCodeTx(ctx, tx, home, code, now); err != nil {
		_ = tx.Commit()
		return identity.Viewer{}, err
	}
	if err := tx.Commit(); err != nil {
		return identity.Viewer{}, err
	}
	return identity.Viewer{Kind: identity.ViewerGuest, Guest: &identity.Guest{
		ID: home.ID, Name: home.Name, ClaimName: home.ClaimName,
		EntryMethod: "access_code", CredentialHolderID: home.CredentialHolderID,
	}, GuestLink: &identity.GuestLink{}}, nil
}

func verifyGuestHomeAccessCodeTx(ctx context.Context, tx *sql.Tx, home identity.GuestHome, code string, now time.Time) (bool, error) {
	becameLocked, err := guestcredentials.VerifyAccessCode(ctx, tx, home.CredentialHolderID, code, now)
	if err == nil {
		return becameLocked, nil
	}
	switch {
	case errors.Is(err, guestcredentials.ErrAccessCodeRequired):
		return becameLocked, ErrAccessCodeRequired
	case errors.Is(err, guestcredentials.ErrAccessCodeInvalid):
		return becameLocked, ErrAccessCodeInvalid
	case errors.Is(err, guestcredentials.ErrAccessCodeLocked):
		return becameLocked, ErrAccessCodeLocked
	}
	return becameLocked, err
}

func trimClaimName(s string) string {
	return strings.TrimSpace(s)
}

// GuestHomeTickets 列出这位访客名下仍有有效链接的工单。轮到访客的排最前，
// 其他开启工单其次，已关闭工单最后且只读；撤销或过期链接对应的工单不列。
func (m *Manager) GuestHomeTickets(ctx context.Context, guestID int64) ([]GuestHomeTicket, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT g.ticket_seq, g.expires_at, g.can_edit_brief
		FROM guest_links g
		JOIN guests gu ON gu.id = g.guest_id
		WHERE g.guest_id = ? AND g.revoked_at IS NULL AND gu.disabled_at IS NULL
		ORDER BY g.ticket_seq, g.can_edit_brief DESC, g.id`, guestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := m.clk.Now()
	type scopedLink struct {
		expires time.Time
		canEdit bool
	}
	links := map[int64]scopedLink{}
	seqs := []int64{}
	for rows.Next() {
		var seq int64
		var expiresAt string
		var canEdit int
		if err := rows.Scan(&seq, &expiresAt, &canEdit); err != nil {
			return nil, err
		}
		expiry, err := time.ParseInLocation(time.RFC3339Nano, expiresAt, clock.Beijing)
		if err != nil {
			return nil, err
		}
		if !now.Before(expiry) {
			continue
		}
		link := scopedLink{expires: expiry, canEdit: canEdit == 1}
		if old, ok := links[seq]; !ok || link.canEdit && !old.canEdit {
			links[seq] = link
		}
		if _, seen := links[seq]; seen && !containsSeq(seqs, seq) {
			seqs = append(seqs, seq)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]GuestHomeTicket, 0, len(seqs))
	for _, seq := range seqs {
		full, err := m.ByRef(ctx, strconv.FormatInt(seq, 10))
		if err != nil {
			return nil, err
		}
		allLinks, err := GuestLinksForTicket(ctx, m.db, seq)
		if err != nil {
			return nil, err
		}
		ownLinks := make([]GuestLinkInfo, 0, len(allLinks))
		for _, l := range allLinks {
			if l.GuestID == guestID {
				ownLinks = append(ownLinks, l)
			}
		}
		turn := WaitingOnOf(full, ownLinks, now)
		item := GuestHomeTicket{
			Seq: full.Seq, Number: full.Number, Title: full.Title, Status: full.Status,
			API: "/api/v1/tickets/" + full.Number, ReadOnly: full.Status == "closed",
			ExpiresAt: links[seq].expires, CanEditBrief: links[seq].canEdit,
		}
		if turn.Party == "guest" && full.Status != "closed" {
			item.WaitingOn = true
			item.Reason = guestHomeWaitingReason(turn)
		}
		out = append(out, item)
	}
	sortGuestHomeTickets(out)
	return out, nil
}

func containsSeq(seqs []int64, seq int64) bool {
	for _, id := range seqs {
		if id == seq {
			return true
		}
	}
	return false
}

func guestHomeWaitingReason(turn WaitingOn) string {
	if turn.Text == "访客补全说明" {
		return "请补全说明"
	}
	if turn.Reason != "" {
		return turn.Reason
	}
	return "对方回复了"
}

func sortGuestHomeTickets(items []GuestHomeTicket) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && guestHomeTicketLess(items[j], items[j-1]); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

func guestHomeTicketLess(a, b GuestHomeTicket) bool {
	ra, rb := guestHomeRank(a), guestHomeRank(b)
	if ra != rb {
		return ra < rb
	}
	return a.Seq > b.Seq
}

func guestHomeRank(item GuestHomeTicket) int {
	switch {
	case item.WaitingOn:
		return 0
	case item.Status == "closed":
		return 2
	default:
		return 1
	}
}
