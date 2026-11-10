// 三种重置拆分（#331，ADR 0019）：重置链接只换单条链接地址；重置访问码以
// 访客为单位，作废全部设备并清除锁定；重置主页地址只换主页凭证。后两者
// 只由成员本人在网页上操作，成员 Agent 在 HTTP 层先行拒绝。
package tickets

import (
	"context"
	"database/sql"
	"errors"
	"github.com/allanpk716/agent-taskboard/internal/guestcredentials"
	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// ErrGuestNotClaimed 表示访客还没有认领，尚无访问码可重置。
var ErrGuestNotClaimed = errors.New("这位访客尚未认领，还没有访问码")

// GuestAccessCodeResetPreview 是成员提交重置前的影响预览。
type GuestAccessCodeResetPreview struct {
	GuestID          int64
	GuestName        string
	DeviceCount      int
	TicketCount      int
	AccessCodeLocked bool
}

// GuestAccessCodeReset 是访客级重置访问码的一次性结果；AccessCode 只随这一次
// 调用返回，HomeKey 供 HTTP 层生成当前主页地址。
type GuestAccessCodeReset struct {
	GuestID    int64
	GuestName  string
	AccessCode string
	HomeKey    string
}

// GuestAccessCodeResetPreviewForViewer 校验管理边界后返回重置影响：将作废的
// 设备数与涉及的工单数（同一张工单的多条链接只算一次）。
func (m *Manager) GuestAccessCodeResetPreviewForViewer(ctx context.Context,
	viewer identity.Viewer, guestID int64) (GuestAccessCodeResetPreview, error) {
	if viewer.Kind != identity.ViewerMember {
		return GuestAccessCodeResetPreview{}, ErrGuestManagementNotAllowed
	}
	if err := m.ensureGuestManageable(ctx, viewer, guestID); err != nil {
		return GuestAccessCodeResetPreview{}, err
	}
	var out GuestAccessCodeResetPreview
	var mergedInto sql.NullInt64
	err := m.db.QueryRowContext(ctx, `
		SELECT gu.id, gu.name,
		       gu.merged_into_guest_id,
		       (SELECT COUNT(*) FROM guest_credential_devices d
		         JOIN guest_credential_holders h ON h.id = d.credential_holder_id
		         WHERE h.owner_type = 'guest' AND h.owner_id = gu.id),
		       (SELECT COUNT(DISTINCT g.ticket_seq) FROM guest_links g WHERE g.guest_id = gu.id)
		FROM guests gu WHERE gu.id = ?`, guestID).
		Scan(&out.GuestID, &out.GuestName, &mergedInto, &out.DeviceCount, &out.TicketCount)
	if errors.Is(err, sql.ErrNoRows) {
		return GuestAccessCodeResetPreview{}, ErrGuestNotFound
	}
	if err != nil {
		return GuestAccessCodeResetPreview{}, err
	}
	if mergedInto.Valid {
		return GuestAccessCodeResetPreview{}, ErrGuestAlreadyMerged
	}
	locked, err := m.guestAccessCodeLocked(ctx, guestID)
	if err != nil {
		return GuestAccessCodeResetPreview{}, err
	}
	out.AccessCodeLocked = locked
	return out, nil
}

// ResetGuestAccessCodeForGuest 以访客为单位重置访问码：作废主页与名下全部
// 链接的设备、清除连续输错锁定、生成只显示一次的新访问码；重置文案使用当前
// 主页地址，因此需要能恢复主页密钥。名下每张工单各记一条时间线事件。
func (m *Manager) ResetGuestAccessCodeForGuest(ctx context.Context,
	viewer identity.Viewer, guestID int64) (GuestAccessCodeReset, error) {
	if viewer.Kind != identity.ViewerMember {
		return GuestAccessCodeReset{}, ErrGuestManagementNotAllowed
	}
	if err := m.ensureGuestManageable(ctx, viewer, guestID); err != nil {
		return GuestAccessCodeReset{}, err
	}
	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return GuestAccessCodeReset{}, err
	}
	defer tx.Rollback()

	var (
		name        string
		holderID    int64
		claimedNull sql.NullString
		mergedInto  sql.NullInt64
	)
	err = tx.QueryRowContext(ctx, `
		SELECT gu.name, gu.merged_into_guest_id, h.id, h.claimed_at
		FROM guests gu
		LEFT JOIN guest_credential_holders h ON h.owner_type = 'guest' AND h.owner_id = gu.id
		WHERE gu.id = ?`, guestID).Scan(&name, &mergedInto, &holderID, &claimedNull)
	if errors.Is(err, sql.ErrNoRows) {
		return GuestAccessCodeReset{}, ErrGuestNotFound
	}
	if err != nil {
		return GuestAccessCodeReset{}, err
	}
	if mergedInto.Valid {
		return GuestAccessCodeReset{}, ErrGuestAlreadyMerged
	}
	if holderID == 0 || !claimedNull.Valid {
		return GuestAccessCodeReset{}, ErrGuestNotClaimed
	}
	home, err := m.ensureGuestHomeKeyTx(ctx, tx, guestID)
	if err != nil {
		return GuestAccessCodeReset{}, err
	}
	if home.Key == "" {
		return GuestAccessCodeReset{}, ErrGuestLinkCipherUnavailable
	}
	reset, err := guestcredentials.Reset(ctx, tx, holderID, now)
	if err != nil {
		return GuestAccessCodeReset{}, err
	}

	rows, err := tx.QueryContext(ctx,
		`SELECT DISTINCT ticket_seq FROM guest_links WHERE guest_id = ? ORDER BY ticket_seq`, guestID)
	if err != nil {
		return GuestAccessCodeReset{}, err
	}
	seqs := []int64{}
	for rows.Next() {
		var seq int64
		if err := rows.Scan(&seq); err != nil {
			rows.Close()
			return GuestAccessCodeReset{}, err
		}
		seqs = append(seqs, seq)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return GuestAccessCodeReset{}, err
	}
	rows.Close()

	actor := authorOf(viewer)
	for _, seq := range seqs {
		eventSeq, change, err := m.appendEvent(ctx, tx, seq, now, actor, "guest_access_code_reset")
		if err != nil {
			return GuestAccessCodeReset{}, err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE timeline_events SET note = ? WHERE ticket_seq = ? AND seq = ?`,
			name, seq, eventSeq); err != nil {
			return GuestAccessCodeReset{}, err
		}
		if err := touchLastChange(ctx, tx, seq, change); err != nil {
			return GuestAccessCodeReset{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return GuestAccessCodeReset{}, err
	}
	return GuestAccessCodeReset{
		GuestID: guestID, GuestName: name, AccessCode: reset.AccessCode, HomeKey: home.Key,
	}, nil
}

// ResetGuestHomeKeyForViewer 只重置访客主页地址：旧主页密钥立即失效，新密钥
// 一次性返回供成员复制主页文案；访问码、设备与名下链接都不变，也不写工单
// 时间线。服务端加密密钥不可用时返回 503 用的 ErrGuestLinkCipherUnavailable。
func (m *Manager) ResetGuestHomeKeyForViewer(ctx context.Context,
	viewer identity.Viewer, guestID int64) (GuestHomeShare, error) {
	if viewer.Kind != identity.ViewerMember {
		return GuestHomeShare{}, ErrGuestManagementNotAllowed
	}
	if err := m.ensureGuestManageable(ctx, viewer, guestID); err != nil {
		return GuestHomeShare{}, err
	}
	if m.guestLinkCipher == nil {
		return GuestHomeShare{}, ErrGuestLinkCipherUnavailable
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return GuestHomeShare{}, err
	}
	defer tx.Rollback()

	var share GuestHomeShare
	var mergedInto sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT id, name, merged_into_guest_id FROM guests WHERE id = ?`, guestID).
		Scan(&share.GuestID, &share.GuestName, &mergedInto)
	if errors.Is(err, sql.ErrNoRows) {
		return GuestHomeShare{}, ErrGuestNotFound
	}
	if err != nil {
		return GuestHomeShare{}, err
	}
	if mergedInto.Valid {
		return GuestHomeShare{}, ErrGuestAlreadyMerged
	}
	key, err := identity.NewGuestHomeKey()
	if err != nil {
		return GuestHomeShare{}, err
	}
	ciphertext, err := m.guestLinkCipher.Encrypt(key)
	if err != nil {
		return GuestHomeShare{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE guests SET home_key_hash = ?, home_key_ciphertext = ? WHERE id = ?`,
		identity.CredentialHash(key), ciphertext, guestID); err != nil {
		return GuestHomeShare{}, err
	}
	if err := tx.Commit(); err != nil {
		return GuestHomeShare{}, err
	}
	share.Key = key
	return share, nil
}

// GuestAccessCodeResetText 生成访客级「主页 + 新访问码」重置文案：称呼对方、
// 当前主页地址与新访问码，并提醒不要转发、Agent 也要改用新码。访问码只随
// 这一次响应出现，调用方必须传能直接打开的绝对主页地址。
func GuestAccessCodeResetTextForGuest(guestName, homeURL, accessCode string) string {
	return guestName + "：你的访问码已重置。你的工单主页：" + homeURL +
		" ，新访问码：" + accessCode +
		" 。所有旧设备和旧访问码已失效，打开主页或任一工单链接时输入新访问码即可。" +
		"访问码请勿转发；如果你让 Agent 处理过你的工单，也请把新访问码告诉它。"
}

// guestAccessCodeLocked 读取访客级访问码是否处于锁定状态。
func (m *Manager) guestAccessCodeLocked(ctx context.Context, guestID int64) (bool, error) {
	var lockedNull sql.NullString
	err := m.db.QueryRowContext(ctx, `
		SELECT h.access_code_locked_at
		FROM guest_credential_holders h
		WHERE h.owner_type = 'guest' AND h.owner_id = ?`, guestID).Scan(&lockedNull)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return lockedNull.Valid, nil
}
