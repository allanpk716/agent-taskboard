package tickets

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/guestcredentials"
	"github.com/allanpk716/agent-taskboard/internal/identity"
)

var (
	// ErrClaimNameRequired 表示访客认领时没填称呼。
	ErrClaimNameRequired = errors.New("认领需要填写你的称呼")
	// ErrLinkAlreadyClaimed 表示链接已被另一位访客抢先认领。
	ErrLinkAlreadyClaimed = errors.New("这条链接已被认领")
	// ErrHomeAlreadyClaimed 表示主页已被另一位访客抢先认领。
	ErrHomeAlreadyClaimed = errors.New("这个主页已被认领")
	// ErrLinkNotClaimed 表示链接还没有访客认领。
	ErrLinkNotClaimed = errors.New("这条链接尚未认领")
	// ErrAccessCodeRequired 表示凭访问码进入时没有填访问码。
	ErrAccessCodeRequired = errors.New("请输入 6 位数字访问码")
	// ErrAccessCodeInvalid 表示访问码不正确。
	ErrAccessCodeInvalid = errors.New("访问码不正确")
	// ErrAccessCodeLocked 表示连续输错后访问码已被锁定。
	ErrAccessCodeLocked = errors.New("访问码已锁定")
	// ErrGuestAccessCodeGuestLevel 表示有主链接不能单独重置访问码：
	// 访问码以访客为单位管理。
	ErrGuestAccessCodeGuestLevel = errors.New("这条链接属于一位访客，访问码按访客为单位管理")
)

// GuestAccessCodeGuestLevelError 表示有主链接的访问码属于整个访客，不能在
// 单条链接上重置；GuestID 告诉调用方改用哪位访客的访客级重置接口。
type GuestAccessCodeGuestLevelError struct {
	GuestID int64
}

func (e *GuestAccessCodeGuestLevelError) Error() string { return ErrGuestAccessCodeGuestLevel.Error() }
func (e *GuestAccessCodeGuestLevelError) Unwrap() error { return ErrGuestAccessCodeGuestLevel }

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
	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return GuestClaimResult{}, err
	}
	defer tx.Rollback()

	claim, err := guestcredentials.Claim(ctx, tx, link.CredentialHolderID, authorName, now)
	if err != nil {
		switch {
		case errors.Is(err, guestcredentials.ErrClaimNameRequired):
			return GuestClaimResult{}, ErrClaimNameRequired
		case errors.Is(err, guestcredentials.ErrAlreadyClaimed):
			return GuestClaimResult{}, ErrLinkAlreadyClaimed
		}
		return GuestClaimResult{}, err
	}
	actor := Author{LinkID: link.ID, LinkNote: link.Note, GuestName: link.GuestName, AuthorName: authorName}
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
	return GuestClaimResult{DeviceToken: claim.DeviceToken, AccessCode: claim.AccessCode, ClaimedAt: now}, nil
}

// EnterGuestLink 校验访问码，签发另一台设备凭证，并清零共享的连续失败计数。
func (m *Manager) EnterGuestLink(ctx context.Context, link identity.GuestLink, code string) (GuestClaimResult, error) {
	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return GuestClaimResult{}, err
	}
	defer tx.Rollback()
	if _, err := m.verifyGuestAccessCodeTx(ctx, tx, link, code, now); err != nil {
		_ = tx.Commit()
		return GuestClaimResult{}, err
	}
	token, err := guestcredentials.IssueDevice(ctx, tx, link.CredentialHolderID, "access_code", now)
	if err != nil {
		return GuestClaimResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return GuestClaimResult{}, err
	}
	return GuestClaimResult{DeviceToken: token, ClaimedAt: now}, nil
}

// ViewerFromGuestAgentCredential 校验「链接 + 访问码」Agent 凭证，不签发
// 设备凭证。即使链接属于长期访客，这张凭证也只作用于这一条链接对应的
// 工单；失败计数仍由访客级凭证持有者统一累计。
func (m *Manager) ViewerFromGuestAgentCredential(ctx context.Context, link identity.GuestLink, code string) (identity.Viewer, error) {
	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return identity.Viewer{}, err
	}
	defer tx.Rollback()
	if _, err := m.verifyGuestAccessCodeTx(ctx, tx, link, code, now); err != nil {
		_ = tx.Commit()
		return identity.Viewer{}, err
	}
	if err := tx.Commit(); err != nil {
		return identity.Viewer{}, err
	}
	link.EntryMethod = "access_code"
	return identity.Viewer{Kind: identity.ViewerGuest, GuestLink: &link}, nil
}

// verifyGuestAccessCodeTx 在调用方事务里完成浏览器与 Agent 共用的访问码校验，
// 并更新共同的失败计数；刚好锁定时返回 true，让调用方补业务时间线。
func (m *Manager) verifyGuestAccessCodeTx(ctx context.Context, tx *sql.Tx,
	link identity.GuestLink, code string, now time.Time) (bool, error) {
	becameLocked, err := guestcredentials.VerifyAccessCode(ctx, tx,
		link.CredentialHolderID, code, now)
	if err != nil {
		mapped := err
		switch {
		case errors.Is(err, guestcredentials.ErrAccessCodeRequired):
			mapped = ErrAccessCodeRequired
		case errors.Is(err, guestcredentials.ErrAccessCodeInvalid):
			mapped = ErrAccessCodeInvalid
		case errors.Is(err, guestcredentials.ErrAccessCodeLocked):
			mapped = ErrAccessCodeLocked
		}
		if becameLocked {
			actor := Author{LinkID: link.ID, LinkNote: link.Note, AuthorName: link.ClaimName}
			if eventErr := m.insertLinkEvent(ctx, tx, link.TicketSeq, actor,
				"guest_access_locked", link.Note, "", now); eventErr != nil {
				return false, eventErr
			}
		}
		return becameLocked, mapped
	}
	if becameLocked {
		actor := Author{LinkID: link.ID, LinkNote: link.Note, AuthorName: link.ClaimName}
		if err := m.insertLinkEvent(ctx, tx, link.TicketSeq, actor, "guest_access_locked",
			link.Note, "", now); err != nil {
			return false, err
		}
	}
	return becameLocked, nil
}

// ResetGuestAccessCode 让所有已记住设备失效并签发新的访问码，不重新开放链接
// 给别人认领。返回链接当前状态，供 HTTP 层生成一次性重置文案。
func (m *Manager) ResetGuestAccessCode(ctx context.Context, viewer identity.Viewer, ref string, id int64) (GuestLinkShare, error) {
	full, err := m.ForViewer(ctx, viewer, ref)
	if err != nil {
		return GuestLinkShare{}, err
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
	if row.GuestID != 0 {
		return GuestLinkShare{}, &GuestAccessCodeGuestLevelError{GuestID: row.GuestID}
	}
	if !row.ClaimedAt.Valid {
		return GuestLinkShare{}, ErrLinkNotClaimed
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
	reset, err := guestcredentials.Reset(ctx, tx, row.HolderID, now)
	if err != nil {
		return GuestLinkShare{}, err
	}
	if err := m.insertLinkEvent(ctx, tx, full.Seq, authorOf(viewer), "guest_access_reset",
		row.Note, "", now); err != nil {
		return GuestLinkShare{}, err
	}
	if err := tx.Commit(); err != nil {
		return GuestLinkShare{}, err
	}
	return GuestLinkShare{
		Key: key, Note: row.Note, Title: full.Title, ExpiresAt: row.ExpiresAtTime,
		Claimed: true, AccessCode: reset.AccessCode,
	}, nil
}

// GuestLinkStatus 只读取认领界面需要的状态字段。
func (m *Manager) GuestLinkStatus(ctx context.Context, link identity.GuestLink) (state identity.GuestLinkStatus, claimName string, claimedAt *time.Time, err error) {
	var claimedNull sql.NullString
	var nameNull sql.NullString
	var lockedNull sql.NullString
	err = m.db.QueryRowContext(ctx,
		`SELECT h.claimed_at, h.claim_name, h.access_code_locked_at
		 FROM guest_credential_holders h
		 JOIN guest_links g ON g.credential_holder_id = h.id
		 WHERE g.id = ?`, link.ID).
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
