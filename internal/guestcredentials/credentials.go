// Package guestcredentials 管理访客凭证：访问码、失败计数、锁定和已信任设备。
// 凭证挂在 Holder 上；当前 Holder 可以是一条无主访客链接，后续也可以是一位长期访客。
package guestcredentials

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
)

// OwnerType 说明一个凭证持有者是哪类领域对象。当前只有无主访客链接；
// guest 留给后续长期访客票，不在本包提前实现访客本身。
type OwnerType string

const (
	OwnerTypeGuestLink OwnerType = "guest_link"
	OwnerTypeGuest     OwnerType = "guest"
)

var (
	ErrClaimNameRequired  = errors.New("认领需要填写你的称呼")
	ErrAccessCodeRequired = errors.New("请输入 6 位数字访问码")
	ErrAccessCodeInvalid  = errors.New("访问码不正确")
	ErrAccessCodeLocked   = errors.New("访问码已锁定")
	ErrAlreadyClaimed     = errors.New("这个凭证持有者已被认领")
	ErrDeviceNotFound     = errors.New("访客设备凭证不存在")
)

// Executor 是写入凭证所需的最小 SQL 接口；调用方可以传连接池，也可以把
// 方法放进外层事务，让凭证变化与时间线等业务数据保持原子。
type Executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Holder 是一个可持有访客凭证的对象。
type Holder struct {
	ID                 int64
	OwnerType          OwnerType
	OwnerID            int64
	ClaimedAt          *time.Time
	ClaimName          string
	AccessCodeHash     *string
	AccessCodeFailures int
	AccessCodeLockedAt *time.Time
}

// ClaimResult 的两段明文都只出现一次，调用方必须立即交给访客。
type ClaimResult struct {
	DeviceToken string
	AccessCode  string
}

// ResetResult 的新访问码只出现一次。
type ResetResult struct {
	AccessCode string
}

// Link 是按设备解析无主链接时带出的链接状态。它只包含身份解析所需的字段，
// 权限和有效期判断仍归 identity 模块。
type Link struct {
	ID           int64
	TicketSeq    int64
	Note         string
	ClaimName    string
	ExpiresAt    time.Time
	RevokedAt    time.Time
	CanEditBrief bool
}

// Guest 是按设备解析长期访客时带出的访客身份。停用等管理状态属于
// 访客本体（#332）；这里只带身份解析与作用域判定需要的字段。
type Guest struct {
	ID       int64
	Name     string
	Disabled bool
	Merged   bool
}

// ResolvedDevice 是一台已信任设备解析出的凭证持有者。
type ResolvedDevice struct {
	HolderID    int64
	OwnerType   OwnerType
	OwnerID     int64
	ClaimName   string
	EntryMethod string
	Link        *Link
	Guest       *Guest
}

// CredentialHash 与平台其他 Bearer 凭证一样使用 SHA-256。
func CredentialHash(credential string) string {
	sum := sha256.Sum256([]byte(credential))
	return hex.EncodeToString(sum[:])
}

// NewDeviceToken 生成一台已认领设备的凭证明文，只在响应里出现一次。
func NewDeviceToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "tbgd_" + base64.RawURLEncoding.EncodeToString(buf), nil
}

// NewAccessCode 生成只显示一次的 6 位访客访问码。
func NewAccessCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", fmt.Errorf("generate access code: %w", err)
	}
	return fmt.Sprintf("%06d", n), nil
}

// CreateLinkHolder 给一条链接建立属于自己的凭证持有者，并把链接指到它。
func CreateLinkHolder(ctx context.Context, q Executor, linkID int64, now time.Time) (Holder, error) {
	var id int64
	err := q.QueryRowContext(ctx, `
		INSERT INTO guest_credential_holders
			(owner_type, owner_id, access_code_failures, created_at)
		VALUES ('guest_link', ?, 0, ?) RETURNING id`,
		linkID, formatTime(now)).Scan(&id)
	if err != nil {
		return Holder{}, fmt.Errorf("写入访客凭证持有者失败：%w", err)
	}
	if _, err := q.ExecContext(ctx,
		`UPDATE guest_links SET credential_holder_id = ? WHERE id = ?`, id, linkID); err != nil {
		return Holder{}, fmt.Errorf("绑定访客凭证持有者失败：%w", err)
	}
	return Holder{ID: id, OwnerType: OwnerTypeGuestLink, OwnerID: linkID}, nil
}

// CreateGuestHolder 给一位长期访客建立凭证持有者。调用方负责在同一事务里
// 写入访客本体；这里只建立“一位访客一份访问码与设备集合”的凭证结构。
func CreateGuestHolder(ctx context.Context, q Executor, guestID int64, now time.Time) (Holder, error) {
	var id int64
	err := q.QueryRowContext(ctx, `
		INSERT INTO guest_credential_holders
			(owner_type, owner_id, access_code_failures, created_at)
		VALUES ('guest', ?, 0, ?) RETURNING id`,
		guestID, formatTime(now)).Scan(&id)
	if err != nil {
		return Holder{}, fmt.Errorf("写入访客凭证持有者失败：%w", err)
	}
	return Holder{ID: id, OwnerType: OwnerTypeGuest, OwnerID: guestID}, nil
}

// Claim 原子完成第一次认领：写入自报称呼、生成访问码并签发第一台设备。
func Claim(ctx context.Context, q Executor, holderID int64, authorName string, now time.Time) (ClaimResult, error) {
	authorName = strings.TrimSpace(authorName)
	if authorName == "" {
		return ClaimResult{}, ErrClaimNameRequired
	}
	token, err := NewDeviceToken()
	if err != nil {
		return ClaimResult{}, err
	}
	code, err := NewAccessCode()
	if err != nil {
		return ClaimResult{}, err
	}
	res, err := q.ExecContext(ctx, `
		UPDATE guest_credential_holders
		SET claimed_at = ?, claim_name = ?, access_code_hash = ?,
		    access_code_failures = 0, access_code_locked_at = NULL
		WHERE id = ? AND claimed_at IS NULL`,
		formatTime(now), authorName, CredentialHash(code), holderID)
	if err != nil {
		return ClaimResult{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return ClaimResult{}, err
	}
	if n == 0 {
		return ClaimResult{}, ErrAlreadyClaimed
	}
	if err := insertDevice(ctx, q, holderID, token, "device", now); err != nil {
		return ClaimResult{}, err
	}
	return ClaimResult{DeviceToken: token, AccessCode: code}, nil
}

// IssueDevice 给已通过访问码校验的持有者签发另一台设备。
func IssueDevice(ctx context.Context, q Executor, holderID int64, entryMethod string, now time.Time) (string, error) {
	token, err := NewDeviceToken()
	if err != nil {
		return "", err
	}
	if err := insertDevice(ctx, q, holderID, token, entryMethod, now); err != nil {
		return "", err
	}
	return token, nil
}

// VerifyAccessCode 校验访问码并维护共享失败计数。返回值表示这次错误是否刚好
// 触发锁定，调用方据此写业务时间线；正确访问码会清零失败计数。
func VerifyAccessCode(ctx context.Context, q Executor, holderID int64, code string, now time.Time) (bool, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return false, ErrAccessCodeRequired
	}
	var (
		hash       *string
		failures   int
		lockedNull sql.NullString
		claimName  string
	)
	if err := q.QueryRowContext(ctx, `
		SELECT access_code_hash, access_code_failures, access_code_locked_at, claim_name
		FROM guest_credential_holders WHERE id = ?`, holderID).
		Scan(&hash, &failures, &lockedNull, &claimName); err != nil {
		return false, err
	}
	if lockedNull.Valid {
		return false, ErrAccessCodeLocked
	}
	if hash != nil && CredentialHash(code) == *hash {
		_, err := q.ExecContext(ctx,
			`UPDATE guest_credential_holders SET access_code_failures = 0 WHERE id = ?`, holderID)
		return false, err
	}
	failures++
	if failures < 5 {
		if _, err := q.ExecContext(ctx,
			`UPDATE guest_credential_holders SET access_code_failures = ? WHERE id = ?`,
			failures, holderID); err != nil {
			return false, err
		}
		return false, ErrAccessCodeInvalid
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE guest_credential_holders
		SET access_code_failures = ?, access_code_locked_at = ? WHERE id = ?`,
		failures, formatTime(now), holderID); err != nil {
		return false, err
	}
	return true, ErrAccessCodeLocked
}

// Reset 作废持有者名下全部设备，生成新访问码并清零失败与锁定状态。
func Reset(ctx context.Context, q Executor, holderID int64, now time.Time) (ResetResult, error) {
	code, err := NewAccessCode()
	if err != nil {
		return ResetResult{}, err
	}
	if _, err := q.ExecContext(ctx,
		`DELETE FROM guest_credential_devices WHERE credential_holder_id = ?`, holderID); err != nil {
		return ResetResult{}, err
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE guest_credential_holders
		SET access_code_hash = ?, access_code_failures = 0, access_code_locked_at = NULL
		WHERE id = ?`, CredentialHash(code), holderID); err != nil {
		return ResetResult{}, err
	}
	return ResetResult{AccessCode: code}, nil
}

// ResolveDevice 按设备凭证明文解析持有者；当前无主链接会带出链接身份。
func ResolveDevice(ctx context.Context, q Executor, token string) (ResolvedDevice, error) {
	var (
		out         ResolvedDevice
		ownerType   string
		claimedNull sql.NullString
		claimName   sql.NullString
		linkID      sql.NullInt64
		ticketSeq   sql.NullInt64
		note        sql.NullString
		expiresAt   sql.NullString
		revokedAt   sql.NullString
		canEdit     sql.NullInt64
		guestID     sql.NullInt64
		guestName   sql.NullString
		guestOffAt  sql.NullString
		guestMerged sql.NullInt64
	)
	err := q.QueryRowContext(ctx, `
		SELECT h.id, h.owner_type, h.owner_id, h.claim_name,
		       d.entry_method, h.claimed_at,
		       l.id, l.ticket_seq, l.note, l.expires_at, l.revoked_at, l.can_edit_brief,
		       gu.id, gu.name, gu.disabled_at, gu.merged_into_guest_id
		FROM guest_credential_devices d
		JOIN guest_credential_holders h ON h.id = d.credential_holder_id
		LEFT JOIN guest_links l
		  ON h.owner_type = 'guest_link' AND h.owner_id = l.id
		LEFT JOIN guests gu
		  ON h.owner_type = 'guest' AND h.owner_id = gu.id
		WHERE d.token_hash = ?`, CredentialHash(token)).
		Scan(&out.HolderID, &ownerType, &out.OwnerID, &claimName,
			&out.EntryMethod, &claimedNull, &linkID, &ticketSeq, &note,
			&expiresAt, &revokedAt, &canEdit, &guestID, &guestName, &guestOffAt, &guestMerged)
	if errors.Is(err, sql.ErrNoRows) {
		return ResolvedDevice{}, ErrDeviceNotFound
	}
	if err != nil {
		return ResolvedDevice{}, err
	}
	out.OwnerType = OwnerType(ownerType)
	out.ClaimName = claimName.String
	if guestID.Valid {
		out.Guest = &Guest{ID: guestID.Int64, Name: guestName.String,
			Disabled: guestOffAt.Valid && guestOffAt.String != "", Merged: guestMerged.Valid}
	}
	if linkID.Valid {
		expiry, err := time.ParseInLocation(time.RFC3339Nano, expiresAt.String, clock.Beijing)
		if err != nil {
			return ResolvedDevice{}, err
		}
		var revoked time.Time
		if revokedAt.Valid {
			revoked, err = time.ParseInLocation(time.RFC3339Nano, revokedAt.String, clock.Beijing)
			if err != nil {
				return ResolvedDevice{}, err
			}
		}
		out.Link = &Link{ID: linkID.Int64, TicketSeq: ticketSeq.Int64,
			Note: note.String, ClaimName: out.ClaimName, ExpiresAt: expiry,
			RevokedAt: revoked, CanEditBrief: canEdit.Int64 == 1}
	}
	return out, nil
}

func insertDevice(ctx context.Context, q Executor, holderID int64, token, entryMethod string, now time.Time) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO guest_credential_devices (credential_holder_id, token_hash, entry_method, created_at)
		VALUES (?, ?, ?, ?)`, holderID, CredentialHash(token), entryMethod, formatTime(now)); err != nil {
		return fmt.Errorf("写入访客设备凭证失败：%w", err)
	}
	return nil
}

func formatTime(t time.Time) string { return t.Format(time.RFC3339Nano) }
