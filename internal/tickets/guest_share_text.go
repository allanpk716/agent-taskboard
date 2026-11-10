package tickets

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// GuestLinkShare 是可再次生成转发文案所需的当前链接状态。
type GuestLinkShare struct {
	Key        string
	HomeKey    string
	HasHome    bool
	Note       string
	GuestID    int64
	GuestName  string
	Title      string
	ExpiresAt  time.Time
	Claimed    bool
	AccessCode string // 重置访问码 / 重置已认领链接时一次性返回；其余场景为空
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
	homeKey := ""
	// 已认领访客只需要知道“已在主页”，不重复暴露主页地址；未认领时才
	// 附主页，让访客第一次就保存长期入口。
	if row.GuestID != 0 && !row.ClaimedAt.Valid {
		home, err := m.GuestHomeShare(ctx, row.GuestID)
		// 服务端密钥丢失只影响“可重复恢复的密文”：不阻断链接本身的转发
		// 文案；主页地址待重置主页密钥后恢复（#331）。
		if err != nil && !errors.Is(err, ErrGuestShareTextUnavailable) &&
			!errors.Is(err, ErrGuestLinkCipherUnavailable) {
			return GuestLinkShare{}, err
		}
		if err == nil {
			homeKey = home.Key
		}
		// 即使服务端密钥丢失导致主页地址暂时不可恢复，仍要告诉已认领访客
		// 这张工单已经在访客的主页上；主页地址由 #331 的重置主页恢复。
		return GuestLinkShare{
			Key: key, HomeKey: homeKey, HasHome: true, Note: row.Note, GuestID: row.GuestID,
			GuestName: row.GuestName, Title: full.Title, ExpiresAt: row.ExpiresAtTime,
			Claimed: row.ClaimedAt.Valid,
		}, nil
	}
	return GuestLinkShare{
		Key: key, HomeKey: homeKey, HasHome: row.GuestID != 0, Note: row.Note, GuestID: row.GuestID, GuestName: row.GuestName,
		Title: full.Title, ExpiresAt: row.ExpiresAtTime, Claimed: row.ClaimedAt.Valid,
	}, nil
}

// RotateGuestLinkKey 给 ref 工单下的一条有效链接换新密钥，并在同一事务里写
// guest_link_rotated 事件。重置链接只换单条链接的地址：访问码、设备、备注、
// 有效期、认领状态与「可填写说明」权限都保持不变（ADR 0019）。
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
		Key: key, HasHome: row.GuestID != 0, Note: row.Note, GuestID: row.GuestID, GuestName: row.GuestName,
		Title: full.Title, ExpiresAt: row.ExpiresAtTime,
		Claimed: row.ClaimedAt.Valid,
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

// DisplayName 返回转发与重置文案对访客的称呼：有主链接用访客名字，
// 无主老链接沿用链接备注。
func (s GuestLinkShare) DisplayName() string {
	if s.GuestName != "" {
		return s.GuestName
	}
	return s.Note
}

// GuestShareText 生成面向访客的转发文案：称呼对方、当前标题、写详细并传
// 日志照片、同一条链接、免登录与当前有效期。未认领时附主页地址；已认领时
// 只提示这张工单已在主页，不再重复暴露主页凭证。
func GuestShareText(note, title, linkURL, homeURL string, hasHome bool, expiresAt time.Time, claimed bool) string {
	text := note + "：想请你帮忙反馈「" + title + "」的详细情况。打开这个链接，按提示把情况写具体一些" +
		"（什么时候、做了什么、看到什么），可以上传日志和照片：" + linkURL +
		"。不用登录，有效期至 " + expiresAt.In(clock.Beijing).Format("2006-01-02") +
		"。链接只归第一位打开并确认的人，请不要转发"
	if claimed {
		if hasHome {
			text += "。这张工单已加到你的主页。换了设备需要输入当初的访问码，忘了请找我重置"
		} else {
			text += "。换了设备需要输入当初的访问码，忘了请找我重置"
		}
		return text
	}
	if homeURL != "" {
		text += "。你的主页：" + homeURL + " ，第一次打开任一链接或主页都会认领你的全部工单，访问码只显示一次，请保存"
	}
	return text
}

// GuestAccessCodeResetText 生成「原链接 + 新访问码」的重置文案。调用方必须传
// 能直接打开的绝对链接；访问码只随这一次响应出现，交给成员后由成员私下转发。
func GuestAccessCodeResetText(note, title, url, accessCode string, expiresAt time.Time) string {
	return note + "：你的访问码已重置。工单「" + title + "」的链接：" + url +
		" ，新访问码：" + accessCode + " 。换设备打开链接时输入新访问码即可，有效期至 " +
		expiresAt.In(clock.Beijing).Format("2006-01-02") +
		"。访问码请勿转发；如果你让 Agent 处理过这张工单，也请把新访问码告诉它。"
}

// GuestLinkResetText 生成「新链接」的重置文案。重置链接不再连带重置访问码：
// 已认领的链接提醒沿用原访问码；未认领的链接提醒首次认领后才会生成访问码。
func GuestLinkResetText(note, title, url string, claimed bool, expiresAt time.Time) string {
	prefix := note + "：这张工单的链接已更换，旧链接已失效。工单「" + title + "」的链接：" + url +
		" 。有效期至 " + expiresAt.In(clock.Beijing).Format("2006-01-02")
	if claimed {
		return prefix + "。访问码保持不变，换设备打开链接时仍输入原来的访问码。"
	}
	return prefix + "。链接只归第一位打开并确认的人，请不要转发。"
}
