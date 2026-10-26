package tickets

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// RecoveredGuestLink 是可再次生成转发文案所需的当前链接状态。
type RecoveredGuestLink struct {
	Key       string
	Note      string
	Title     string
	ExpiresAt time.Time
	Claimed   bool
}

// RecoverGuestLink 校验成员能管理这张工单上的这条有效链接，并用服务端密钥
// 还原同一条访客链接。老链接（没有密文或当前密钥解不开）返回 409 用的
// ErrGuestShareTextUnavailable；哈希鉴权不受影响。
func (m *Manager) RecoverGuestLink(ctx context.Context, viewer identity.Viewer, ref string, id int64) (RecoveredGuestLink, error) {
	full, err := m.ForViewer(ctx, viewer, ref)
	if err != nil {
		return RecoveredGuestLink{}, err
	}
	var (
		note         string
		expiresAtRaw string
		revokedAt    sql.NullString
		claimedAt    sql.NullString
		keyCipher    sql.NullString
	)
	err = m.db.QueryRowContext(ctx, `
		SELECT note, expires_at, revoked_at, claimed_at, key_ciphertext
		FROM guest_links WHERE id = ? AND ticket_seq = ?`, id, full.Seq).
		Scan(&note, &expiresAtRaw, &revokedAt, &claimedAt, &keyCipher)
	if errors.Is(err, sql.ErrNoRows) {
		return RecoveredGuestLink{}, ErrGuestLinkNotFound
	}
	if err != nil {
		return RecoveredGuestLink{}, err
	}
	expiresAt, err := parseTime(expiresAtRaw)
	if err != nil {
		return RecoveredGuestLink{}, err
	}
	if revokedAt.Valid {
		return RecoveredGuestLink{}, &LinkNotExtendableError{Reason: "revoked"}
	}
	if !m.clk.Now().Before(expiresAt) {
		return RecoveredGuestLink{}, &LinkNotExtendableError{Reason: "expired"}
	}
	if m.guestLinkCipher == nil || !keyCipher.Valid {
		return RecoveredGuestLink{}, ErrGuestShareTextUnavailable
	}
	key, err := m.guestLinkCipher.Decrypt(keyCipher.String)
	if err != nil {
		return RecoveredGuestLink{}, ErrGuestShareTextUnavailable
	}
	return RecoveredGuestLink{
		Key: key, Note: note, Title: full.Title, ExpiresAt: expiresAt, Claimed: claimedAt.Valid,
	}, nil
}

// RotateGuestLinkKey 给 ref 工单下的一条有效链接换新密钥：哈希、加密密钥与
// guest_link_rotated 事件在同一事务里写入。除地址本身外，备注、有效期、认领
// 状态、设备凭证与访问码都不变。
func (m *Manager) RotateGuestLinkKey(ctx context.Context, viewer identity.Viewer, ref string, id int64) (RecoveredGuestLink, error) {
	full, err := m.ForViewer(ctx, viewer, ref)
	if err != nil {
		return RecoveredGuestLink{}, err
	}
	if m.guestLinkCipher == nil {
		return RecoveredGuestLink{}, ErrGuestLinkCipherUnavailable
	}
	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return RecoveredGuestLink{}, err
	}
	defer tx.Rollback()

	var (
		note         string
		expiresAtRaw string
		revokedAt    sql.NullString
		claimedAt    sql.NullString
	)
	err = tx.QueryRowContext(ctx, `
		SELECT note, expires_at, revoked_at, claimed_at
		FROM guest_links WHERE id = ? AND ticket_seq = ?`, id, full.Seq).
		Scan(&note, &expiresAtRaw, &revokedAt, &claimedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RecoveredGuestLink{}, ErrGuestLinkNotFound
	}
	if err != nil {
		return RecoveredGuestLink{}, err
	}
	expiresAt, err := parseTime(expiresAtRaw)
	if err != nil {
		return RecoveredGuestLink{}, err
	}
	if revokedAt.Valid {
		return RecoveredGuestLink{}, &LinkNotExtendableError{Reason: "revoked"}
	}
	if !now.Before(expiresAt) {
		return RecoveredGuestLink{}, &LinkNotExtendableError{Reason: "expired"}
	}

	key, err := identity.NewGuestKey()
	if err != nil {
		return RecoveredGuestLink{}, err
	}
	keyCiphertext, err := m.guestLinkCipher.Encrypt(key)
	if err != nil {
		return RecoveredGuestLink{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE guest_links SET key_hash = ?, key_ciphertext = ?
		WHERE id = ? AND ticket_seq = ?`,
		identity.CredentialHash(key), keyCiphertext, id, full.Seq); err != nil {
		return RecoveredGuestLink{}, err
	}
	if err := m.insertLinkEvent(ctx, tx, full.Seq, authorOf(viewer), "guest_link_rotated",
		note, "", now); err != nil {
		return RecoveredGuestLink{}, err
	}
	if err := tx.Commit(); err != nil {
		return RecoveredGuestLink{}, err
	}
	return RecoveredGuestLink{
		Key: key, Note: note, Title: full.Title, ExpiresAt: expiresAt, Claimed: claimedAt.Valid,
	}, nil
}

// GuestShareText 生成面向访客的转发文案：称呼对方、当前标题、写详细并传
// 日志照片、同一条链接、免登录与当前有效期；已认领时追加访问码提醒。
func GuestShareText(note, title, url string, expiresAt time.Time, claimed bool) string {
	text := note + "：想请你帮忙反馈「" + title + "」的详细情况。打开这个链接，按提示把情况写具体一些" +
		"（什么时候、做了什么、看到什么），可以上传日志和照片：" + url +
		"。不用登录，有效期至 " + expiresAt.In(clock.Beijing).Format("2006-01-02") +
		"。链接只归第一位打开并确认的人，请不要转发"
	if claimed {
		text += "。换了设备需要输入当初的访问码，忘了请找我重置"
	}
	return text
}
