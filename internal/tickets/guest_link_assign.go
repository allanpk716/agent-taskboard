// 批量把无主老链接归到访客，并处理访问码沿用或生成。
package tickets

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/guestcredentials"
	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// GuestAccessCodeChoice 是批量归属的访问码选项：沿用一条已认领老链接，
// 或由网页成员生成只显示一次的新码。
type GuestAccessCodeChoice struct {
	Provided     bool
	Generate     bool
	SourceLinkID int64
}

// GuestAccessCodeAction 是批量归属预览和结果里的访问码处理方案；JSON 值保持
// API 文档中的字符串不变。
type GuestAccessCodeAction string

const (
	GuestAccessCodeKeepGuest       GuestAccessCodeAction = "keep_guest"
	GuestAccessCodeRemainUnclaimed GuestAccessCodeAction = "remain_unclaimed"
	GuestAccessCodeChoiceRequired  GuestAccessCodeAction = "choice_required"
	GuestAccessCodeReuseSource     GuestAccessCodeAction = "reuse_source_link"
	GuestAccessCodeGenerate        GuestAccessCodeAction = "generate"
)

// AssignableGuestLinkPreview 描述一次批量归属的影响，不改变任何数据。
type AssignableGuestLinkPreview struct {
	GuestID                int64
	GuestName              string
	GuestClaimed           bool
	LinkCount              int
	TicketCount            int
	ClaimedLinkCount       int
	DeviceCount            int
	AccessCodeAction       GuestAccessCodeAction
	SourceLinkIDs          []int64
	SourceLinkID           int64
	SourceDeviceCount      int
	InvalidatedDeviceCount int
}

// AssignGuestLinksResult 是批量归属成功后的汇总；每条链接还在各自工单上
// 留一条 guest_link_assigned 时间线事件。
type AssignGuestLinksResult struct {
	GuestID          int64
	GuestName        string
	LinkCount        int
	TicketCount      int
	AccessCodeAction GuestAccessCodeAction
	AccessCode       string
	Links            []AssignedGuestLinkSummary
}

// AssignedGuestLinkSummary 是批量归属响应里的链接摘要。
type AssignedGuestLinkSummary struct {
	ID           int64
	TicketNumber string
	TicketTitle  string
}

type assignableGuestLink struct {
	ID           int64
	TicketSeq    int64
	TicketNumber string
	TicketTitle  string
	Note         string
	HolderID     int64
	GuestID      int64
	ExpiresAt    time.Time
	Revoked      bool
	Claimed      bool
	DeviceCount  int
}

func validateGuestLinkIDs(ids []int64) error {
	if len(ids) == 0 {
		return ErrInvalidGuestLinkIDs
	}
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return ErrInvalidGuestLinkIDs
		}
		seen[id] = true
	}
	return nil
}

// GuestLinkAssignmentPreview 返回把 ids 归给 guestID 前的影响。choice_required
// 时可以带 source_link_id 或 generate，让预览反映成员所选访问码方案的后果。
func (m *Manager) GuestLinkAssignmentPreview(ctx context.Context, viewer identity.Viewer,
	guestID int64, ids []int64, choice GuestAccessCodeChoice) (AssignableGuestLinkPreview, error) {
	if err := validateGuestLinkIDs(ids); err != nil {
		return AssignableGuestLinkPreview{}, err
	}
	visible, err := m.VisibleSeqs(ctx, viewer)
	if err != nil {
		return AssignableGuestLinkPreview{}, err
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return AssignableGuestLinkPreview{}, err
	}
	defer tx.Rollback()

	actor := authorOf(viewer)
	targetGuestID, guestName, _, guestClaimed, err := m.guestForNewLinkTx(ctx, tx, actor,
		GuestLinkSpec{GuestID: guestID}, m.clk.Now())
	if err != nil {
		return AssignableGuestLinkPreview{}, err
	}
	links, err := m.assignableGuestLinksTx(ctx, tx, visible, ids, m.clk.Now())
	if err != nil {
		return AssignableGuestLinkPreview{}, err
	}
	return applyGuestAccessCodeChoice(assignmentPreview(targetGuestID, guestName, guestClaimed, links),
		links, choice, viewer)
}

// AssignGuestLinks 把一批无主老链接归到 guestID。凭证处理复用单条归属：
// 已认领访客保留自己的凭证；未认领访客可沿用一条已认领链接的凭证，或生成
// 只显示一次的新码；全未认领时保持未认领。所有校验、改归属、凭证变化和
// 时间线事件都在一个事务里完成。
func (m *Manager) AssignGuestLinks(ctx context.Context, viewer identity.Viewer, guestID int64,
	ids []int64, choice GuestAccessCodeChoice) (AssignGuestLinksResult, error) {
	if err := validateGuestLinkIDs(ids); err != nil {
		return AssignGuestLinksResult{}, err
	}
	visible, err := m.VisibleSeqs(ctx, viewer)
	if err != nil {
		return AssignGuestLinksResult{}, err
	}
	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return AssignGuestLinksResult{}, err
	}
	defer tx.Rollback()

	actor := authorOf(viewer)
	targetGuestID, guestName, guestHolderID, guestClaimed, err := m.guestForNewLinkTx(ctx, tx, actor,
		GuestLinkSpec{GuestID: guestID}, now)
	if err != nil {
		return AssignGuestLinksResult{}, err
	}
	links, err := m.assignableGuestLinksTx(ctx, tx, visible, ids, now)
	if err != nil {
		return AssignGuestLinksResult{}, err
	}
	preview := assignmentPreview(targetGuestID, guestName, guestClaimed, links)
	if choice.Provided && (preview.AccessCodeAction == GuestAccessCodeKeepGuest ||
		preview.AccessCodeAction == GuestAccessCodeRemainUnclaimed) {
		return AssignGuestLinksResult{}, ErrGuestAccessCodeChoiceNotAllowed
	}
	if preview.AccessCodeAction == GuestAccessCodeChoiceRequired {
		if !choice.Provided {
			return AssignGuestLinksResult{}, ErrGuestAccessCodeChoiceRequired
		}
		preview, err = applyGuestAccessCodeChoice(preview, links, choice, viewer)
		if err != nil {
			return AssignGuestLinksResult{}, err
		}
	} else if choice.Provided {
		return AssignGuestLinksResult{}, ErrGuestAccessCodeChoiceNotAllowed
	}
	if _, err := m.ensureGuestHomeKeyTx(ctx, tx, targetGuestID); err != nil {
		return AssignGuestLinksResult{}, err
	}
	orderedLinks := links
	if choice.SourceLinkID != 0 {
		orderedLinks = make([]assignableGuestLink, 0, len(links))
		for _, link := range links {
			if link.ID == choice.SourceLinkID {
				orderedLinks = append(orderedLinks, link)
			}
		}
		for _, link := range links {
			if link.ID != choice.SourceLinkID {
				orderedLinks = append(orderedLinks, link)
			}
		}
	}
	newAccessCode := ""
	if choice.Generate {
		reset, err := guestcredentials.Reset(ctx, tx, guestHolderID, now)
		if err != nil {
			return AssignGuestLinksResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE guest_credential_holders
			SET claimed_at = ?, claim_name = ?
			WHERE id = ?`, now.Format(time.RFC3339Nano), guestName, guestHolderID); err != nil {
			return AssignGuestLinksResult{}, err
		}
		newAccessCode = reset.AccessCode
	}
	for _, link := range orderedLinks {
		if _, err := tx.ExecContext(ctx,
			`UPDATE guest_links SET guest_id = ?, credential_holder_id = ? WHERE id = ?`,
			targetGuestID, guestHolderID, link.ID); err != nil {
			return AssignGuestLinksResult{}, err
		}
		if err := assignGuestCredentialsTx(ctx, tx, link.HolderID, guestHolderID, link.Claimed); err != nil {
			return AssignGuestLinksResult{}, err
		}
		if err := m.insertAssignmentEvent(ctx, tx, link.TicketSeq, actor,
			link.Note, guestName, "manual", now); err != nil {
			return AssignGuestLinksResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return AssignGuestLinksResult{}, err
	}
	result := AssignGuestLinksResult{
		GuestID: targetGuestID, GuestName: guestName, LinkCount: preview.LinkCount,
		TicketCount: preview.TicketCount, AccessCodeAction: preview.AccessCodeAction,
		AccessCode: newAccessCode,
		Links:      make([]AssignedGuestLinkSummary, 0, len(links)),
	}
	for _, link := range links {
		result.Links = append(result.Links, AssignedGuestLinkSummary{
			ID: link.ID, TicketNumber: link.TicketNumber, TicketTitle: link.TicketTitle,
		})
	}
	return result, nil
}

func (m *Manager) assignableGuestLinksTx(ctx context.Context, tx *sql.Tx,
	visible []int64, ids []int64, now time.Time) ([]assignableGuestLink, error) {
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT g.id, g.ticket_seq, t.number, t.title, g.note, g.expires_at,
		       g.revoked_at, h.claimed_at, g.guest_id, g.credential_holder_id,
		       (SELECT COUNT(*) FROM guest_credential_devices d
		         WHERE d.credential_holder_id = g.credential_holder_id)
		FROM guest_links g
		JOIN tickets t ON t.seq = g.ticket_seq
		JOIN guest_credential_holders h ON h.id = g.credential_holder_id
		WHERE g.id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY g.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	links := make([]assignableGuestLink, 0, len(ids))
	for rows.Next() {
		var (
			link      assignableGuestLink
			expiresAt string
			revokedAt sql.NullString
			claimedAt sql.NullString
			guestID   sql.NullInt64
		)
		if err := rows.Scan(&link.ID, &link.TicketSeq, &link.TicketNumber, &link.TicketTitle,
			&link.Note, &expiresAt, &revokedAt, &claimedAt, &guestID,
			&link.HolderID, &link.DeviceCount); err != nil {
			return nil, err
		}
		if link.ExpiresAt, err = parseTime(expiresAt); err != nil {
			return nil, err
		}
		link.GuestID = guestID.Int64
		link.Revoked = revokedAt.Valid
		link.Claimed = claimedAt.Valid
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(links) != len(ids) {
		return nil, ErrGuestLinkNotFound
	}
	visibleSet := make(map[int64]bool, len(visible))
	for _, seq := range visible {
		visibleSet[seq] = true
	}
	for _, link := range links {
		if !visibleSet[link.TicketSeq] {
			return nil, ErrGuestLinkNotFound
		}
		if link.GuestID != 0 {
			return nil, ErrGuestLinkAlreadyAssigned
		}
		if link.Revoked {
			return nil, &GuestLinkNotAssignableError{Reason: "revoked"}
		}
		if !now.Before(link.ExpiresAt) {
			return nil, &GuestLinkNotAssignableError{Reason: "expired"}
		}
	}
	return links, nil
}

func assignmentPreview(guestID int64, guestName string, guestClaimed bool,
	links []assignableGuestLink) AssignableGuestLinkPreview {
	preview := AssignableGuestLinkPreview{
		GuestID: guestID, GuestName: guestName, GuestClaimed: guestClaimed,
		LinkCount: len(links), SourceLinkIDs: []int64{},
		AccessCodeAction: GuestAccessCodeRemainUnclaimed,
	}
	tickets := map[int64]bool{}
	for _, link := range links {
		tickets[link.TicketSeq] = true
		preview.DeviceCount += link.DeviceCount
		if link.Claimed {
			preview.ClaimedLinkCount++
			preview.SourceLinkIDs = append(preview.SourceLinkIDs, link.ID)
		}
	}
	preview.TicketCount = len(tickets)
	if guestClaimed {
		preview.AccessCodeAction = GuestAccessCodeKeepGuest
		preview.SourceLinkIDs = []int64{}
	} else if preview.ClaimedLinkCount > 0 {
		preview.AccessCodeAction = GuestAccessCodeChoiceRequired
	}
	return preview
}

func applyGuestAccessCodeChoice(preview AssignableGuestLinkPreview, links []assignableGuestLink,
	choice GuestAccessCodeChoice, viewer identity.Viewer) (AssignableGuestLinkPreview, error) {
	if !choice.Provided {
		return preview, nil
	}
	if preview.AccessCodeAction != GuestAccessCodeChoiceRequired {
		return AssignableGuestLinkPreview{}, ErrGuestAccessCodeChoiceNotAllowed
	}
	if choice.Generate {
		if !viewer.IsWebSession() {
			return AssignableGuestLinkPreview{}, ErrGuestAccessCodeGenerateWebSessionRequired
		}
		preview.AccessCodeAction = GuestAccessCodeGenerate
		preview.SourceLinkID = 0
		preview.SourceDeviceCount = 0
		preview.InvalidatedDeviceCount = preview.DeviceCount
		return preview, nil
	}
	for _, link := range links {
		if link.ID != choice.SourceLinkID {
			continue
		}
		if !link.Claimed {
			return AssignableGuestLinkPreview{}, ErrGuestAccessCodeChoiceInvalid
		}
		preview.AccessCodeAction = GuestAccessCodeReuseSource
		preview.SourceLinkID = link.ID
		preview.SourceDeviceCount = link.DeviceCount
		preview.InvalidatedDeviceCount = preview.DeviceCount - link.DeviceCount
		return preview, nil
	}
	return AssignableGuestLinkPreview{}, ErrGuestAccessCodeChoiceInvalid
}
