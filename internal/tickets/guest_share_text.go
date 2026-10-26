package tickets

import (
	"context"
	"database/sql"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// GuestLinkShare 是可再次生成转发文案所需的当前链接状态。
type GuestLinkShare struct {
	Key       string
	Note      string
	Title     string
	ExpiresAt time.Time
	Claimed   bool
}

// RecoverGuestLink 校验成员能管理这张工单上的这条有效链接，并用服务端密钥
// 还原同一条访客链接。老链接（没有密文或当前密钥解不开）返回 409 用的
// ErrGuestShareTextUnavailable；哈希鉴权不受影响。
func (m *Manager) RecoverGuestLink(ctx context.Context, viewer identity.Viewer, ref string, id int64) (GuestLinkShare, error) {
	full, err := m.ForViewer(ctx, viewer, ref)
	if err != nil {
		return GuestLinkShare{}, err
	}
	row, err := m.activeLinkRowForTicketTx(ctx, m.db, full.Seq, id, m.clk.Now())
	if err != nil {
		return GuestLinkShare{}, err
	}
	if m.guestLinkCipher == nil {
		return GuestLinkShare{}, ErrGuestLinkCipherUnavailable
	}
	if !row.KeyCiphertext.Valid {
		return GuestLinkShare{}, ErrGuestShareTextUnavailable
	}
	key, err := m.guestLinkCipher.Decrypt(row.KeyCiphertext.String)
	if err != nil {
		return GuestLinkShare{}, ErrGuestShareTextUnavailable
	}
	return GuestLinkShare{
		Key: key, Note: row.Note, Title: full.Title, ExpiresAt: row.ExpiresAtTime, Claimed: row.ClaimedAt.Valid,
	}, nil
}

// RotateGuestLinkKey 给 ref 工单下的一条有效链接换新密钥：哈希、加密密钥与
// guest_link_rotated 事件在同一事务里写入。除地址本身外，备注、有效期、认领
// 状态、设备凭证与访问码都不变。
func (m *Manager) RotateGuestLinkKey(ctx context.Context, viewer identity.Viewer, ref string, id int64) (GuestLinkShare, error) {
	full, err := m.ForViewer(ctx, viewer, ref)
	if err != nil {
		return GuestLinkShare{}, err
	}
	if m.guestLinkCipher == nil {
		return GuestLinkShare{}, ErrGuestLinkCipherUnavailable
	}
	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return GuestLinkShare{}, err
	}
	defer tx.Rollback()

	row, err := m.activeLinkRowForTicketTx(ctx, tx, full.Seq, id, now)
	if err != nil {
		return GuestLinkShare{}, err
	}

	key, err := identity.NewGuestKey()
	if err != nil {
		return GuestLinkShare{}, err
	}
	keyCiphertext, err := m.encryptedGuestLinkKey(key)
	if err != nil {
		return GuestLinkShare{}, err
	}
	if !keyCiphertext.Valid {
		return GuestLinkShare{}, ErrGuestLinkCipherUnavailable
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE guest_links SET key_hash = ?, key_ciphertext = ?
		WHERE id = ? AND ticket_seq = ?`,
		identity.CredentialHash(key), keyCiphertext, id, full.Seq); err != nil {
		return GuestLinkShare{}, err
	}
	if err := m.insertLinkEvent(ctx, tx, full.Seq, authorOf(viewer), "guest_link_rotated",
		row.Note, "", now); err != nil {
		return GuestLinkShare{}, err
	}
	if err := tx.Commit(); err != nil {
		return GuestLinkShare{}, err
	}
	return GuestLinkShare{
		Key: key, Note: row.Note, Title: full.Title, ExpiresAt: row.ExpiresAtTime, Claimed: row.ClaimedAt.Valid,
	}, nil
}

// encryptedGuestLinkKey 加密新链接密钥；服务端加密密钥不可用时返回空密文，
// 让链接按老链接创建。需要密文的换新地址由调用方自行拒绝。
func (m *Manager) encryptedGuestLinkKey(key string) (sql.NullString, error) {
	if m.guestLinkCipher == nil {
		return sql.NullString{}, nil
	}
	ciphertext, err := m.guestLinkCipher.Encrypt(key)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: ciphertext, Valid: true}, nil
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
