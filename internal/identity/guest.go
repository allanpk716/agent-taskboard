// 访客链接密钥的解析（#32）：链接 /g/<密钥> 的密钥直接作 Bearer token。
// 撤销与过期即时生效——每个请求都实时查库判定。
package identity

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/guestcredentials"
)

// guestKeyPrefix / guestHomeKeyPrefix 让单条链接与主页密钥一眼可辨
// （与 Agent token 的 tbag_ 区分）。
const (
	guestKeyPrefix     = "tbg_"
	guestHomeKeyPrefix = "tbh_"
)

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
	// ErrHomeClaimed 表示访客主页已被认领，主页密钥本身不再可访问。
	ErrHomeClaimed = errors.New("访客主页已被认领")
)

// LinkStatusError 携带被拒访客链接的身份信息，访问记录需要在返回 401 前取它。
type LinkStatusError struct {
	Link   GuestLink
	Status GuestLinkStatus
	Err    error
}

// GuestLinkStatus 是访客链接对外暴露的稳定状态值。
type GuestLinkStatus string

const (
	// GuestLinkUnclaimed 表示链接尚未被第一位访客认领。
	GuestLinkUnclaimed GuestLinkStatus = "unclaimed"
	// GuestLinkClaimed 表示链接已有唯一访客。
	GuestLinkClaimed GuestLinkStatus = "claimed"
	// GuestLinkAccessLocked 表示访问码连续输错后已锁定。
	GuestLinkAccessLocked GuestLinkStatus = "access_code_locked"
	// GuestLinkRevoked 表示链接已被成员撤销。
	GuestLinkRevoked GuestLinkStatus = "revoked"
	// GuestLinkExpired 表示链接已过期。
	GuestLinkExpired GuestLinkStatus = "expired"
)

// GuestLinkStatusOf 统一推导链接状态；撤销与锁定分别优先于已认领。
func GuestLinkStatusOf(revokedAt, lockedAt, claimedAt *time.Time) GuestLinkStatus {
	switch {
	case revokedAt != nil:
		return GuestLinkRevoked
	case lockedAt != nil:
		return GuestLinkAccessLocked
	case claimedAt != nil:
		return GuestLinkClaimed
	default:
		return GuestLinkUnclaimed
	}
}

func (e *LinkStatusError) Error() string { return e.Err.Error() }
func (e *LinkStatusError) Unwrap() error { return e.Err }

// HomeStatusError 携带被拒主页的身份信息，HTTP 层据此返回认领或访问码状态。
type HomeStatusError struct {
	Home   GuestHome
	Status GuestLinkStatus
	Err    error
}

func (e *HomeStatusError) Error() string { return e.Err.Error() }
func (e *HomeStatusError) Unwrap() error { return e.Err }

// GuestLink 是凭证解析后的访客链接身份（Viewer.GuestLink）：
// 备注链接创建者写；到期时刻与「可填写说明」权限决定访客能做什么。
type GuestLink struct {
	ID                 int64
	TicketSeq          int64
	Note               string
	GuestID            int64
	GuestName          string
	ExpiresAt          time.Time
	CanEditBrief       bool
	ClaimName          string
	EntryMethod        string
	CredentialHolderID int64
}

// Guest 是部门级、长期存在的访客身份（#328）。凭设备凭证解析出访客时，
// 可用链接集合是“该访客名下未撤销、未过期的链接”；按工单判权由
// tickets.ForViewer 完成并把结果写回 Viewer.GuestLink。
type Guest struct {
	ID                 int64
	Name               string
	ClaimName          string
	EntryMethod        string
	CredentialHolderID int64
}

// GuestHome 是主页密钥解析出的长期访客身份；主页本身没有有效期，
// 状态只由访客停用与凭证认领决定。
type GuestHome struct {
	ID                 int64
	Name               string
	CredentialHolderID int64
	ClaimName          string
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

// NewGuestHomeKey 生成一位访客的主页密钥（明文）。成员复制主页文案时服务端
// 保存哈希与密文；明文可随文案重复还原，但只发给访客本人。
func NewGuestHomeKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := randRead(buf); err != nil {
		return "", err
	}
	return guestHomeKeyPrefix + base64URLEncode(buf), nil
}

// NewGuestDeviceKey 生成一台已认领设备的凭证明文，只在认领响应里出现一次。
func NewGuestDeviceKey() (string, error) {
	return guestcredentials.NewDeviceToken()
}

// NewAccessCode 生成只显示一次的 6 位访客访问码。
func NewAccessCode() (string, error) {
	return guestcredentials.NewAccessCode()
}

// IsGuestDeviceKey 用于凭证解析前的分派。
func IsGuestDeviceKey(token string) bool { return strings.HasPrefix(token, "tbgd_") }

// IsGuestHomeKey 用于认领接口在设备凭证与主页密钥之间分派。
func IsGuestHomeKey(token string) bool { return strings.HasPrefix(token, guestHomeKeyPrefix) }

// GuestAgentCredential 是访客 Agent 的 Bearer 凭证写法：链接密钥与访问码用冒号相连。
type GuestAgentCredential struct {
	Key        string
	AccessCode string
	Home       bool
}

// ParseGuestAgentCredential 识别 tbg_<链接密钥>:<访问码> 或
// tbh_<主页密钥>:<访问码>；密钥与访问码本身都不含冒号。
func ParseGuestAgentCredential(token string) (GuestAgentCredential, bool) {
	home := strings.HasPrefix(token, guestHomeKeyPrefix)
	if !home && !strings.HasPrefix(token, guestKeyPrefix) {
		return GuestAgentCredential{}, false
	}
	key, code, ok := strings.Cut(token, ":")
	if !ok || key == "" || code == "" {
		return GuestAgentCredential{}, false
	}
	return GuestAgentCredential{Key: key, AccessCode: code, Home: home}, true
}

// CredentialHash 返回凭证（Agent token 或访客链接密钥）的存储哈希。
// 两类 Bearer 凭证用同一方案，查库按哈希比对。
func CredentialHash(credential string) string { return guestcredentials.CredentialHash(credential) }

// ViewerFromGuestKey 把访客链接密钥解析成 Viewer。密钥未知返回
// ErrInvalidCredentials；已撤销返回 ErrLinkRevoked；已过期返回 ErrLinkExpired。
// 撤销与过期即时生效：每次调用都实时查库，过期判定用注入时钟。
func (m *Manager) ViewerFromGuestKey(ctx context.Context, key string) (Viewer, error) {
	var (
		id            int64
		ticketSeq     int64
		note          string
		expiresAt     string
		revokedAt     sql.NullString
		claimedAt     sql.NullString
		guestID       sql.NullInt64
		guestName     sql.NullString
		guestDisabled sql.NullString
		holderID      int64
		canEdit       int
	)
	err := m.db.QueryRowContext(ctx, `
		SELECT g.id, g.ticket_seq, g.note, g.expires_at, g.revoked_at,
		       g.credential_holder_id, g.can_edit_brief, h.claimed_at, g.guest_id, gu.name, gu.disabled_at
		FROM guest_links g
		JOIN guest_credential_holders h ON h.id = g.credential_holder_id
		LEFT JOIN guests gu ON gu.id = g.guest_id
		WHERE g.key_hash = ?`, hashToken(key)).
		Scan(&id, &ticketSeq, &note, &expiresAt, &revokedAt, &holderID, &canEdit, &claimedAt, &guestID, &guestName, &guestDisabled)
	if errors.Is(err, sql.ErrNoRows) {
		return Viewer{}, ErrInvalidCredentials
	}
	if err != nil {
		return Viewer{}, err
	}
	if guestDisabled.Valid {
		// 所属访客已停用：链接视同失效（#332），与主页密钥同样返回
		// ErrInvalidCredentials，不进入认领或输码流程。
		return Viewer{}, ErrInvalidCredentials
	}
	expiry, err := time.ParseInLocation(time.RFC3339Nano, expiresAt, clock.Beijing)
	if err != nil {
		return Viewer{}, err
	}
	if revokedAt.Valid {
		return Viewer{}, &LinkStatusError{Link: guestLinkIdentity(id, ticketSeq, note, guestID, guestName, expiry, canEdit == 1, holderID), Status: GuestLinkRevoked, Err: ErrLinkRevoked}
	}
	if !m.clock.Now().Before(expiry) {
		return Viewer{}, &LinkStatusError{Link: guestLinkIdentity(id, ticketSeq, note, guestID, guestName, expiry, canEdit == 1, holderID), Status: GuestLinkExpired, Err: ErrLinkExpired}
	}
	if !claimedAt.Valid {
		return Viewer{}, &LinkStatusError{Link: guestLinkIdentity(id, ticketSeq, note, guestID, guestName, expiry, canEdit == 1, holderID), Status: GuestLinkUnclaimed, Err: ErrLinkUnclaimed}
	}
	return Viewer{}, &LinkStatusError{
		Link:   guestLinkIdentity(id, ticketSeq, note, guestID, guestName, expiry, canEdit == 1, holderID),
		Status: GuestLinkClaimed, Err: ErrLinkClaimed,
	}
}

// GuestLinkForKey 服务认领接口：它能查看未认领链接，但不会转成普通 Viewer。
func (m *Manager) GuestLinkForKey(ctx context.Context, key string) (GuestLink, error) {
	var (
		id            int64
		ticketSeq     int64
		note          string
		expiresAt     string
		revokedAt     sql.NullString
		claimedAt     sql.NullString
		claimName     sql.NullString
		guestID       sql.NullInt64
		guestName     sql.NullString
		guestDisabled sql.NullString
		holderID      int64
		canEdit       int
	)
	err := m.db.QueryRowContext(ctx, `
		SELECT g.id, g.ticket_seq, g.note, g.expires_at, g.revoked_at,
		       g.credential_holder_id, g.can_edit_brief,
		       h.claimed_at, h.claim_name, g.guest_id, gu.name, gu.disabled_at
		FROM guest_links g
		JOIN guest_credential_holders h ON h.id = g.credential_holder_id
		LEFT JOIN guests gu ON gu.id = g.guest_id
		WHERE g.key_hash = ?`, hashToken(key)).
		Scan(&id, &ticketSeq, &note, &expiresAt, &revokedAt, &holderID, &canEdit, &claimedAt, &claimName, &guestID, &guestName, &guestDisabled)
	if errors.Is(err, sql.ErrNoRows) {
		return GuestLink{}, ErrInvalidCredentials
	}
	if err != nil {
		return GuestLink{}, err
	}
	if guestDisabled.Valid {
		return GuestLink{}, ErrInvalidCredentials
	}
	expiry, err := time.ParseInLocation(time.RFC3339Nano, expiresAt, clock.Beijing)
	if err != nil {
		return GuestLink{}, err
	}
	link := GuestLink{ID: id, TicketSeq: ticketSeq, Note: note,
		GuestID: guestID.Int64, GuestName: guestName.String, ExpiresAt: expiry,
		CanEditBrief: canEdit == 1, ClaimName: claimName.String, CredentialHolderID: holderID}
	if revokedAt.Valid {
		return link, &LinkStatusError{Link: link, Status: GuestLinkRevoked, Err: ErrLinkRevoked}
	}
	if !m.clock.Now().Before(expiry) {
		return link, &LinkStatusError{Link: link, Status: GuestLinkExpired, Err: ErrLinkExpired}
	}
	if claimedAt.Valid {
		return link, &LinkStatusError{Link: link, Status: GuestLinkClaimed, Err: ErrLinkClaimed}
	}
	return link, nil
}

// GuestHomeForKey 服务主页认领接口：主页密钥只在未认领时可作为入口；
// 已认领后浏览器使用设备凭证，Agent 使用「主页密钥:访问码」。
func (m *Manager) GuestHomeForKey(ctx context.Context, key string) (GuestHome, error) {
	var (
		home       GuestHome
		disabledAt sql.NullString
		claimedAt  sql.NullString
		claimName  sql.NullString
		lockedAt   sql.NullString
	)
	err := m.db.QueryRowContext(ctx, `
		SELECT gu.id, gu.name, gu.disabled_at, h.id, h.claimed_at, h.claim_name, h.access_code_locked_at
		FROM guests gu
		JOIN guest_credential_holders h ON h.owner_type = 'guest' AND h.owner_id = gu.id
		WHERE gu.home_key_hash = ?`, CredentialHash(key)).
		Scan(&home.ID, &home.Name, &disabledAt, &home.CredentialHolderID, &claimedAt, &claimName, &lockedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return GuestHome{}, ErrInvalidCredentials
	}
	if err != nil {
		return GuestHome{}, err
	}
	if disabledAt.Valid {
		return GuestHome{}, ErrInvalidCredentials
	}
	home.ClaimName = claimName.String
	var claimed *time.Time
	if claimedAt.Valid {
		at, err := time.ParseInLocation(time.RFC3339Nano, claimedAt.String, clock.Beijing)
		if err != nil {
			return GuestHome{}, err
		}
		claimed = &at
	}
	var locked *time.Time
	if lockedAt.Valid {
		at, err := time.ParseInLocation(time.RFC3339Nano, lockedAt.String, clock.Beijing)
		if err != nil {
			return GuestHome{}, err
		}
		locked = &at
	}
	switch GuestLinkStatusOf(nil, locked, claimed) {
	case GuestLinkAccessLocked:
		return home, &HomeStatusError{Home: home, Status: GuestLinkAccessLocked, Err: guestcredentials.ErrAccessCodeLocked}
	case GuestLinkClaimed:
		return home, &HomeStatusError{Home: home, Status: GuestLinkClaimed, Err: ErrHomeClaimed}
	default:
		return home, nil
	}
}

// ViewerFromGuestDevice 解析浏览器记住的设备凭证。
func (m *Manager) ViewerFromGuestDevice(ctx context.Context, token string) (Viewer, error) {
	resolved, err := guestcredentials.ResolveDevice(ctx, m.db, token)
	if errors.Is(err, guestcredentials.ErrDeviceNotFound) {
		return Viewer{}, ErrInvalidCredentials
	}
	if err != nil {
		return Viewer{}, err
	}
	if resolved.OwnerType == guestcredentials.OwnerTypeGuest {
		if resolved.Guest == nil || resolved.Guest.Disabled {
			return Viewer{}, ErrInvalidCredentials
		}
		return Viewer{Kind: ViewerGuest, Guest: &Guest{
			ID: resolved.Guest.ID, Name: resolved.Guest.Name,
			ClaimName: resolved.ClaimName, EntryMethod: resolved.EntryMethod,
			CredentialHolderID: resolved.HolderID,
		}, GuestLink: &GuestLink{}}, nil
	}
	if resolved.OwnerType != guestcredentials.OwnerTypeGuestLink || resolved.Link == nil {
		return Viewer{}, ErrInvalidCredentials
	}
	source := resolved.Link
	link := GuestLink{ID: source.ID, TicketSeq: source.TicketSeq, Note: source.Note,
		ClaimName: source.ClaimName, EntryMethod: resolved.EntryMethod,
		CredentialHolderID: resolved.HolderID, ExpiresAt: source.ExpiresAt}
	if !source.RevokedAt.IsZero() {
		return Viewer{}, &LinkStatusError{Link: link, Status: GuestLinkRevoked, Err: ErrLinkRevoked}
	}
	expiry := source.ExpiresAt
	if !m.clock.Now().Before(expiry) {
		return Viewer{}, &LinkStatusError{Link: link, Status: GuestLinkExpired, Err: ErrLinkExpired}
	}
	link.CanEditBrief = source.CanEditBrief
	return Viewer{Kind: ViewerGuest, GuestLink: &link}, nil
}

func guestLinkIdentity(id, ticketSeq int64, note string, guestID sql.NullInt64, guestName sql.NullString,
	expiry time.Time, canEdit bool, holderID int64) GuestLink {
	return GuestLink{ID: id, TicketSeq: ticketSeq, Note: note,
		GuestID: guestID.Int64, GuestName: guestName.String, ExpiresAt: expiry,
		CanEditBrief: canEdit, CredentialHolderID: holderID}
}
