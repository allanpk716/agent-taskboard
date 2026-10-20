package tickets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/identity"
)

var (
	// ErrClaimNameRequired 表示访客认领时没填称呼。
	ErrClaimNameRequired = errors.New("认领需要填写你的称呼")
	// ErrLinkAlreadyClaimed 表示链接已被另一位访客抢先认领。
	ErrLinkAlreadyClaimed = errors.New("这条链接已被认领")
	// ErrLinkNotClaimed 表示链接还没有访客认领。
	ErrLinkNotClaimed = errors.New("这条链接尚未认领")
	// ErrAccessCodeRequired 表示凭访问码进入时没有填访问码。
	ErrAccessCodeRequired = errors.New("请输入 6 位数字访问码")
	// ErrAccessCodeInvalid 表示访问码不正确。
	ErrAccessCodeInvalid = errors.New("访问码不正确")
	// ErrAccessCodeLocked 表示连续输错后访问码已被锁定。
	ErrAccessCodeLocked = errors.New("访问码已锁定")
)

// GuestClaimResult 是认领成功的输出；DeviceToken 只出现一次。
type GuestClaimResult struct {
	DeviceToken string
	AccessCode  string
	ClaimedAt   time.Time
}

// ClaimGuestLink 原子记录第一位访客的自报称呼，签发一台设备凭证，
// 并追加时间线事件。
func (m *Manager) ClaimGuestLink(ctx context.Context, link identity.GuestLink, authorName string) (GuestClaimResult, error) {
	authorName = strings.TrimSpace(authorName)
	if authorName == "" {
		return GuestClaimResult{}, ErrClaimNameRequired
	}
	token, err := identity.NewGuestDeviceKey()
	if err != nil {
		return GuestClaimResult{}, err
	}
	code, err := identity.NewAccessCode()
	if err != nil {
		return GuestClaimResult{}, err
	}
	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return GuestClaimResult{}, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE guest_links SET claimed_at = ?, claim_name = ?, access_code_hash = ?
		WHERE id = ? AND claimed_at IS NULL`,
		now.Format(time.RFC3339Nano), authorName, identity.CredentialHash(code), link.ID)
	if err != nil {
		return GuestClaimResult{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return GuestClaimResult{}, err
	}
	if n == 0 {
		return GuestClaimResult{}, ErrLinkAlreadyClaimed
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO guest_devices (guest_link_id, token_hash, entry_method, created_at)
		VALUES (?, ?, 'device', ?)`,
		link.ID, identity.CredentialHash(token), now.Format(time.RFC3339Nano)); err != nil {
		return GuestClaimResult{}, fmt.Errorf("写入访客设备凭证失败：%w", err)
	}
	actor := Author{LinkID: link.ID, LinkNote: link.Note, AuthorName: authorName}
	eventSeq, change, err := m.appendEvent(ctx, tx, link.TicketSeq, now, actor, "guest_link_claimed")
	if err != nil {
		return GuestClaimResult{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE timeline_events SET note = ? WHERE ticket_seq = ? AND seq = ?`,
		link.Note, link.TicketSeq, eventSeq); err != nil {
		return GuestClaimResult{}, err
	}
	if err := touchLastChange(ctx, tx, link.TicketSeq, change); err != nil {
		return GuestClaimResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return GuestClaimResult{}, err
	}
	return GuestClaimResult{DeviceToken: token, AccessCode: code, ClaimedAt: now}, nil
}

// EnterGuestLink 校验访问码，签发另一台设备凭证，并清零共享的连续失败计数。
func (m *Manager) EnterGuestLink(ctx context.Context, link identity.GuestLink, code string) (GuestClaimResult, error) {
	token, err := identity.NewGuestDeviceKey()
	if err != nil {
		return GuestClaimResult{}, err
	}
	if err := m.verifyGuestAccessCode(ctx, link, code); err != nil {
		return GuestClaimResult{}, err
	}
	now := m.clk.Now()
	if _, err := m.db.ExecContext(ctx, `
		INSERT INTO guest_devices (guest_link_id, token_hash, entry_method, created_at)
		VALUES (?, ?, 'access_code', ?)`,
		link.ID, identity.CredentialHash(token), now.Format(time.RFC3339Nano)); err != nil {
		return GuestClaimResult{}, fmt.Errorf("写入访客设备凭证失败：%w", err)
	}
	return GuestClaimResult{DeviceToken: token, ClaimedAt: now}, nil
}

// ViewerFromGuestAgentCredential 校验访问码 Bearer 凭证，不签发设备凭证。
// 每次请求复用浏览器的连续失败计数，并按 access_code 记录进入方式。
func (m *Manager) ViewerFromGuestAgentCredential(ctx context.Context, link identity.GuestLink, code string) (identity.Viewer, error) {
	if err := m.verifyGuestAccessCode(ctx, link, code); err != nil {
		return identity.Viewer{}, err
	}
	link.EntryMethod = "access_code"
	return identity.Viewer{Kind: identity.ViewerGuest, GuestLink: &link}, nil
}

// verifyGuestAccessCode 在同一事务里完成浏览器与 Agent 共用的访问码校验，
// 并更新共同的失败计数。
func (m *Manager) verifyGuestAccessCode(ctx context.Context, link identity.GuestLink, code string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return ErrAccessCodeRequired
	}
	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var (
		hash       *string
		failures   int
		lockedNull sql.NullString
		claimName  string
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT access_code_hash, access_code_failures, access_code_locked_at, claim_name
		FROM guest_links WHERE id = ?`, link.ID).
		Scan(&hash, &failures, &lockedNull, &claimName); err != nil {
		return err
	}
	if lockedNull.Valid {
		return ErrAccessCodeLocked
	}
	if hash == nil || identity.CredentialHash(code) != *hash {
		failures++
		if failures >= 5 {
			if _, err := tx.ExecContext(ctx, `
				UPDATE guest_links SET access_code_failures = ?, access_code_locked_at = ? WHERE id = ?`,
				failures, now.Format(time.RFC3339Nano), link.ID); err != nil {
				return err
			}
			actor := Author{LinkID: link.ID, LinkNote: link.Note, AuthorName: claimName}
			if err := m.insertLinkEvent(ctx, tx, link.TicketSeq, actor, "guest_access_locked",
				link.Note, "", now); err != nil {
				return err
			}
			if err := tx.Commit(); err != nil {
				return err
			}
			return ErrAccessCodeLocked
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE guest_links SET access_code_failures = ? WHERE id = ?`, failures, link.ID); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return ErrAccessCodeInvalid
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE guest_links SET access_code_failures = 0 WHERE id = ?`, link.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// ResetGuestAccessCode 让所有已记住设备失效并签发新的访问码；
// 不重新开放链接给别人认领。
func (m *Manager) ResetGuestAccessCode(ctx context.Context, viewer identity.Viewer, ref string, id int64) (string, error) {
	full, err := m.ByRef(ctx, ref)
	if err != nil {
		return "", err
	}
	code, err := identity.NewAccessCode()
	if err != nil {
		return "", err
	}
	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var claimedAt sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT claimed_at FROM guest_links WHERE id = ? AND ticket_seq = ?`, id, full.Seq).
		Scan(&claimedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrGuestLinkNotFound
		}
		return "", err
	}
	if !claimedAt.Valid {
		return "", ErrLinkNotClaimed
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM guest_devices WHERE guest_link_id = ?`, id); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE guest_links
		SET access_code_hash = ?, access_code_failures = 0, access_code_locked_at = NULL
		WHERE id = ?`, identity.CredentialHash(code), id); err != nil {
		return "", err
	}
	var note string
	if err := tx.QueryRowContext(ctx, `SELECT note FROM guest_links WHERE id = ?`, id).Scan(&note); err != nil {
		return "", err
	}
	if err := m.insertLinkEvent(ctx, tx, full.Seq, authorOf(viewer), "guest_access_reset",
		note, "", now); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return code, nil
}

// GuestLinkStatus 只读取认领界面需要的状态字段。
func (m *Manager) GuestLinkStatus(ctx context.Context, link identity.GuestLink) (state identity.GuestLinkStatus, claimName string, claimedAt *time.Time, err error) {
	var claimedNull sql.NullString
	var nameNull sql.NullString
	var lockedNull sql.NullString
	err = m.db.QueryRowContext(ctx,
		`SELECT claimed_at, claim_name, access_code_locked_at FROM guest_links WHERE id = ?`, link.ID).
		Scan(&claimedNull, &nameNull, &lockedNull)
	if err != nil {
		return "", "", nil, err
	}
	var claimed *time.Time
	if claimedNull.Valid {
		at, parseErr := time.ParseInLocation(time.RFC3339Nano, claimedNull.String, m.clk.Now().Location())
		if parseErr != nil {
			return "", "", nil, parseErr
		}
		claimed = &at
	}
	return identity.GuestLinkStatusOf(nil, nullTime(lockedNull), claimed), nameNull.String, claimed, nil
}

func nullTime(null sql.NullString) *time.Time {
	if !null.Valid {
		return nil
	}
	at, err := time.ParseInLocation(time.RFC3339Nano, null.String, time.Local)
	if err != nil {
		return nil
	}
	return &at
}
