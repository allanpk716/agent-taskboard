// 访客链接密钥的解析（#32）：链接 /g/<密钥> 的密钥直接作 Bearer token。
// 撤销与过期即时生效——每个请求都实时查库判定。
package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
)

// guestKeyPrefix 让访客链接密钥一眼可辨（与 Agent token 的 tbag_ 区分）。
const guestKeyPrefix = "tbg_"

// 访客链接凭证状态错误；HTTP 层把它们映射成 401 link_revoked / link_expired。
var (
	// ErrLinkRevoked 表示访客链接已被撤销。
	ErrLinkRevoked = errors.New("访客链接已被撤销")
	// ErrLinkExpired 表示访客链接已过期。
	ErrLinkExpired = errors.New("访客链接已过期")
	// ErrLinkUnclaimed 表示访客链接还没有被第一位访客认领。
	ErrLinkUnclaimed = errors.New("访客链接尚未认领")
	// ErrLinkClaimed 表示访客链接已被第一位访客认领，链接密钥本身不再可访问。
	ErrLinkClaimed = errors.New("访客链接已被认领")
)

// LinkStatusError 携带被拒访客链接的身份信息，访问记录需要在返回 401 前取它。
type LinkStatusError struct {
	Link   GuestLink
	Status string // revoked | expired
	Err    error
}

func (e *LinkStatusError) Error() string { return e.Err.Error() }
func (e *LinkStatusError) Unwrap() error { return e.Err }

// GuestLink 是凭证解析后的访客链接身份（Viewer.GuestLink）：
// 备注链接创建者写；到期时刻与「可填写说明」权限决定访客能做什么。
type GuestLink struct {
	ID           int64
	TicketSeq    int64
	Note         string
	ExpiresAt    time.Time
	CanEditBrief bool
	ClaimName    string
	EntryMethod  string
}

// NewGuestKey 生成一条访客链接的密钥（明文）。明文只在创建响应里出现一次，
// 库里只存哈希。
func NewGuestKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := randRead(buf); err != nil {
		return "", err
	}
	return guestKeyPrefix + base64URLEncode(buf), nil
}

// NewGuestDeviceKey 生成一台已认领设备的凭证明文，只在认领响应里出现一次。
func NewGuestDeviceKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := randRead(buf); err != nil {
		return "", err
	}
	return "tbgd_" + base64URLEncode(buf), nil
}

// NewAccessCode generates the one-time six-digit code shown to a guest.
func NewAccessCode() (string, error) {
	n, err := cryptoRandInt(1000000)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n), nil
}

// IsGuestDeviceKey 用于凭证解析前的分派。
func IsGuestDeviceKey(token string) bool { return strings.HasPrefix(token, "tbgd_") }

// CredentialHash 返回凭证（Agent token 或访客链接密钥）的存储哈希。
// 两类 Bearer 凭证用同一方案，查库按哈希比对。
func CredentialHash(credential string) string { return hashToken(credential) }

// ViewerFromGuestKey 把访客链接密钥解析成 Viewer。密钥未知返回
// ErrInvalidCredentials；已撤销返回 ErrLinkRevoked；已过期返回 ErrLinkExpired。
// 撤销与过期即时生效：每次调用都实时查库，过期判定用注入时钟。
func (m *Manager) ViewerFromGuestKey(ctx context.Context, key string) (Viewer, error) {
	var (
		id        int64
		ticketSeq int64
		note      string
		expiresAt string
		revokedAt sql.NullString
		claimedAt sql.NullString
		canEdit   int
	)
	err := m.db.QueryRowContext(ctx, `
		SELECT id, ticket_seq, note, expires_at, revoked_at, claimed_at, can_edit_brief
		FROM guest_links WHERE key_hash = ?`, hashToken(key)).
		Scan(&id, &ticketSeq, &note, &expiresAt, &revokedAt, &claimedAt, &canEdit)
	if errors.Is(err, sql.ErrNoRows) {
		return Viewer{}, ErrInvalidCredentials
	}
	if err != nil {
		return Viewer{}, err
	}
	if revokedAt.Valid {
		return Viewer{}, &LinkStatusError{Link: GuestLink{ID: id, TicketSeq: ticketSeq, Note: note}, Status: "revoked", Err: ErrLinkRevoked}
	}
	expiry, err := time.ParseInLocation(time.RFC3339Nano, expiresAt, clock.Beijing)
	if err != nil {
		return Viewer{}, err
	}
	if !m.clock.Now().Before(expiry) {
		return Viewer{}, &LinkStatusError{Link: GuestLink{ID: id, TicketSeq: ticketSeq, Note: note}, Status: "expired", Err: ErrLinkExpired}
	}
	if !claimedAt.Valid {
		return Viewer{}, &LinkStatusError{Link: GuestLink{ID: id, TicketSeq: ticketSeq, Note: note}, Status: "unclaimed", Err: ErrLinkUnclaimed}
	}
	return Viewer{}, &LinkStatusError{
		Link:   GuestLink{ID: id, TicketSeq: ticketSeq, Note: note, ExpiresAt: expiry, CanEditBrief: canEdit == 1},
		Status: "claimed", Err: ErrLinkClaimed,
	}
}

// GuestLinkForKey serves the claim interface, which can inspect an unclaimed
// link without turning it into a normal viewer.
func (m *Manager) GuestLinkForKey(ctx context.Context, key string) (GuestLink, error) {
	var (
		id        int64
		ticketSeq int64
		note      string
		expiresAt string
		revokedAt sql.NullString
		claimedAt sql.NullString
		claimName sql.NullString
		canEdit   int
	)
	err := m.db.QueryRowContext(ctx, `
		SELECT id, ticket_seq, note, expires_at, revoked_at, claimed_at, claim_name, can_edit_brief
		FROM guest_links WHERE key_hash = ?`, hashToken(key)).
		Scan(&id, &ticketSeq, &note, &expiresAt, &revokedAt, &claimedAt, &claimName, &canEdit)
	if errors.Is(err, sql.ErrNoRows) {
		return GuestLink{}, ErrInvalidCredentials
	}
	if err != nil {
		return GuestLink{}, err
	}
	expiry, err := time.ParseInLocation(time.RFC3339Nano, expiresAt, clock.Beijing)
	if err != nil {
		return GuestLink{}, err
	}
	link := GuestLink{ID: id, TicketSeq: ticketSeq, Note: note, ExpiresAt: expiry,
		CanEditBrief: canEdit == 1, ClaimName: claimName.String}
	if revokedAt.Valid {
		return link, &LinkStatusError{Link: link, Status: "revoked", Err: ErrLinkRevoked}
	}
	if !m.clock.Now().Before(expiry) {
		return link, &LinkStatusError{Link: link, Status: "expired", Err: ErrLinkExpired}
	}
	if claimedAt.Valid {
		return link, &LinkStatusError{Link: link, Status: "claimed", Err: ErrLinkClaimed}
	}
	return link, nil
}

// ViewerFromGuestDevice parses a remembered device credential.
func (m *Manager) ViewerFromGuestDevice(ctx context.Context, token string) (Viewer, error) {
	var (
		id        int64
		ticketSeq int64
		note      string
		expiresAt string
		revokedAt sql.NullString
		claimName string
		entry     string
		canEdit   int
	)
	err := m.db.QueryRowContext(ctx, `
		SELECT g.id, g.ticket_seq, g.note, g.expires_at, g.revoked_at, g.claim_name, g.can_edit_brief,
		       d.entry_method
		FROM guest_devices d JOIN guest_links g ON g.id = d.guest_link_id
		WHERE d.token_hash = ?`, hashToken(token)).
		Scan(&id, &ticketSeq, &note, &expiresAt, &revokedAt, &claimName, &canEdit, &entry)
	if errors.Is(err, sql.ErrNoRows) {
		return Viewer{}, ErrInvalidCredentials
	}
	if err != nil {
		return Viewer{}, err
	}
	link := GuestLink{ID: id, TicketSeq: ticketSeq, Note: note, ClaimName: claimName, EntryMethod: entry}
	if revokedAt.Valid {
		return Viewer{}, &LinkStatusError{Link: link, Status: "revoked", Err: ErrLinkRevoked}
	}
	expiry, err := time.ParseInLocation(time.RFC3339Nano, expiresAt, clock.Beijing)
	if err != nil {
		return Viewer{}, err
	}
	link.ExpiresAt = expiry
	if !m.clock.Now().Before(expiry) {
		return Viewer{}, &LinkStatusError{Link: link, Status: "expired", Err: ErrLinkExpired}
	}
	link.CanEditBrief = canEdit == 1
	return Viewer{Kind: ViewerGuest, GuestLink: &link}, nil
}
