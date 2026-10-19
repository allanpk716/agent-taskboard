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
)

// GuestClaimResult 是认领成功的输出；DeviceToken 只出现一次。
type GuestClaimResult struct {
	DeviceToken string
	ClaimedAt   time.Time
}

// ClaimGuestLink atomically records the first guest's self-reported name,
// issues one device credential, and appends a timeline event.
func (m *Manager) ClaimGuestLink(ctx context.Context, link identity.GuestLink, authorName string) (GuestClaimResult, error) {
	authorName = strings.TrimSpace(authorName)
	if authorName == "" {
		return GuestClaimResult{}, ErrClaimNameRequired
	}
	token, err := identity.NewGuestDeviceKey()
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
		UPDATE guest_links SET claimed_at = ?, claim_name = ?
		WHERE id = ? AND claimed_at IS NULL`,
		now.Format(time.RFC3339Nano), authorName, link.ID)
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
		INSERT INTO guest_devices (guest_link_id, token_hash, created_at) VALUES (?, ?, ?)`,
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
	return GuestClaimResult{DeviceToken: token, ClaimedAt: now}, nil
}

// GuestLinkStatus reads only the fields needed by the claim interface.
func (m *Manager) GuestLinkStatus(ctx context.Context, link identity.GuestLink) (state, claimName string, claimedAt *time.Time, err error) {
	var claimedNull sql.NullString
	var nameNull sql.NullString
	err = m.db.QueryRowContext(ctx,
		`SELECT claimed_at, claim_name FROM guest_links WHERE id = ?`, link.ID).
		Scan(&claimedNull, &nameNull)
	if err != nil {
		return "", "", nil, err
	}
	if !claimedNull.Valid {
		return "unclaimed", "", nil, nil
	}
	at, err := time.ParseInLocation(time.RFC3339Nano, claimedNull.String, m.clk.Now().Location())
	if err != nil {
		return "", "", nil, err
	}
	return "claimed", nameNull.String, &at, nil
}
