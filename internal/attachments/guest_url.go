// 访客链接绑定的附件地址（#33，orchestrator 决议 9）：浏览器 <img src> 带不上
// Bearer 头，所以给访客渲染的附件地址是短时效签名地址——签名绑（链接、附件、
// 到期时刻），但链接的撤销与过期仍实时查库判定，链接失效后地址随之失效（401）。
package attachments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// 签名地址的有效期：1 小时，且不超过链接本身的到期时刻。
const guestURLTTL = time.Hour

// 签名校验的错误。
var (
	// ErrLinkRevoked 表示访客链接已被撤销（401 link_revoked，HTTP 层映射）。
	ErrLinkRevoked = errors.New("访客链接已被撤销")
	// ErrLinkExpired 表示访客链接已过期（401 link_expired）。
	ErrLinkExpired = errors.New("访客链接已过期")
	// ErrURLExpired 表示签名地址已过期（链接本身可能还有效，重新读工单拿新地址）。
	ErrURLExpired = errors.New("附件地址已过期")
	// ErrBadSignature 表示签名不匹配（伪造或参数被改动）。
	ErrBadSignature = errors.New("附件地址的签名不匹配")
)

// linkSigningRow 是签名所需的链接信息（密钥哈希只存库，明文密钥不落盘）。
type linkSigningRow struct {
	KeyHash   string
	TicketSeq int64
	ExpiresAt time.Time
	Revoked   bool
}

func (m *Manager) linkRow(ctx context.Context, linkID int64) (linkSigningRow, error) {
	var (
		row       linkSigningRow
		expiresAt string
		revokedAt sql.NullString
	)
	err := m.db.QueryRowContext(ctx,
		`SELECT key_hash, ticket_seq, expires_at, revoked_at FROM guest_links WHERE id = ?`,
		linkID).Scan(&row.KeyHash, &row.TicketSeq, &expiresAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return linkSigningRow{}, ErrNotFound
	}
	if err != nil {
		return linkSigningRow{}, err
	}
	if revokedAt.Valid {
		row.Revoked = true
	}
	if row.ExpiresAt, err = parseTime(expiresAt); err != nil {
		return linkSigningRow{}, err
	}
	return row, nil
}

// sign 计算签名：HMAC-SHA256(链接密钥哈希, "attachments|链接id|附件id|到期")。
func sign(keyHash string, linkID, attID, exp int64) string {
	mac := hmac.New(sha256.New, []byte(keyHash))
	fmt.Fprintf(mac, "attachments|%d|%d|%d", linkID, attID, exp)
	return hex.EncodeToString(mac.Sum(nil))
}

// GuestURL 为访客链接生成这个附件的短时效签名地址（嵌在工单 JSON 与 Markdown 里）。
func (m *Manager) GuestURL(ctx context.Context, att AttachmentInfo, linkID int64) (string, error) {
	row, err := m.linkRow(ctx, linkID)
	if err != nil {
		return "", err
	}
	now := m.clk.Now()
	exp := now.Add(guestURLTTL)
	if row.ExpiresAt.Before(exp) {
		exp = row.ExpiresAt // 不超过链接本身的到期时刻
	}
	sig := sign(row.KeyHash, linkID, att.ID, exp.Unix())
	q := url.Values{}
	q.Set("link", strconv.FormatInt(linkID, 10))
	q.Set("exp", strconv.FormatInt(exp.Unix(), 10))
	q.Set("sig", sig)
	return "/api/v1/attachments/" + att.Ref() + "/" + url.PathEscape(att.Filename) + "?" + q.Encode(), nil
}

// VerifyGuestURL 校验签名地址（下载接口用）：签名与到期时刻，然后实时判定链接
// 的撤销与过期（链接失效后地址随之失效），最后核对链接指向的工单。
func (m *Manager) VerifyGuestURL(ctx context.Context, att AttachmentInfo, linkID, exp int64, sig string) error {
	row, err := m.linkRow(ctx, linkID)
	if err != nil {
		return err
	}
	// 先验签名（真实性），再看链接状态（撤销/过期即时生效，与工单同判），
	// 最后看地址自身的到期：链接已失效时按链接的原因报，比「地址过期」准确。
	now := m.clk.Now()
	if !hmac.Equal([]byte(sig), []byte(sign(row.KeyHash, linkID, att.ID, exp))) {
		return ErrBadSignature
	}
	if row.Revoked {
		return ErrLinkRevoked
	}
	if !now.Before(row.ExpiresAt) {
		return ErrLinkExpired
	}
	if now.Unix() >= exp {
		return ErrURLExpired
	}
	if row.TicketSeq != att.TicketSeq {
		return ErrNotFound // 链接指向别的工单：与「不存在」不可区分（防枚举）
	}
	return nil
}
