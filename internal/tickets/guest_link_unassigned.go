// 无主老链接列表：按调用者可见范围过滤，并支持搜索与名字相近推荐。
package tickets

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// UnassignedGuestLink 是成员视角的一条无主老链接；LastUsedAt 来自访问记录。
type UnassignedGuestLink struct {
	ID             int64
	TicketSeq      int64
	TicketNumber   string
	TicketTitle    string
	Note           string
	ExpiresAt      string
	Claimed        bool
	ClaimName      string
	LastUsedAt     *string
	PossiblePerson bool
}

// UnassignedGuestLinks 列出 viewer 可见、未撤销未过期且尚未归属访客的老链接。
// query 匹配工单编号、标题、备注和自报称呼；name 非空时按简单相近规则把
// “可能是这个人”的链接排前并打标。
func (m *Manager) UnassignedGuestLinks(ctx context.Context, viewer identity.Viewer,
	query, name string) ([]UnassignedGuestLink, error) {
	visible, err := m.VisibleSeqs(ctx, viewer)
	if err != nil {
		return nil, err
	}
	visibleSet := make(map[int64]bool, len(visible))
	for _, seq := range visible {
		visibleSet[seq] = true
	}
	rows, err := m.db.QueryContext(ctx, `
		SELECT g.id, g.ticket_seq, t.number, t.title, g.note, g.expires_at,
		       h.claimed_at, h.claim_name,
		       (SELECT MAX(r.last_at) FROM access_records r
		         WHERE r.actor_link_id = g.id AND r.action IN ('view', 'download'))
		FROM guest_links g
		JOIN tickets t ON t.seq = g.ticket_seq
		JOIN guest_credential_holders h ON h.id = g.credential_holder_id
		WHERE g.guest_id IS NULL AND g.revoked_at IS NULL
		ORDER BY g.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := m.clk.Now()
	query = normalizeGuestName(query)
	name = normalizeGuestName(name)
	out := []UnassignedGuestLink{}
	for rows.Next() {
		var (
			item       UnassignedGuestLink
			claimedAt  sql.NullString
			claimName  sql.NullString
			lastUsedAt sql.NullString
		)
		if err := rows.Scan(&item.ID, &item.TicketSeq, &item.TicketNumber, &item.TicketTitle,
			&item.Note, &item.ExpiresAt, &claimedAt, &claimName, &lastUsedAt); err != nil {
			return nil, err
		}
		if !visibleSet[item.TicketSeq] {
			continue
		}
		expiry, err := parseTime(item.ExpiresAt)
		if err != nil {
			return nil, err
		}
		if !now.Before(expiry) {
			continue
		}
		item.Claimed = claimedAt.Valid
		item.ClaimName = claimName.String
		if lastUsedAt.Valid {
			item.LastUsedAt = &lastUsedAt.String
		}
		if query != "" && !unassignedLinkMatches(item, query) {
			continue
		}
		item.PossiblePerson = name != "" && guestNameSimilar(name, item.Note, item.ClaimName)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortUnassignedGuestLinks(out)
	return out, nil
}

func unassignedLinkMatches(item UnassignedGuestLink, query string) bool {
	for _, text := range []string{item.TicketNumber, item.TicketTitle, item.Note, item.ClaimName} {
		if strings.Contains(normalizeGuestName(text), query) {
			return true
		}
	}
	return false
}

func sortUnassignedGuestLinks(items []UnassignedGuestLink) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].PossiblePerson != items[j].PossiblePerson {
			return items[i].PossiblePerson
		}
		if items[i].lastUsedAt() != items[j].lastUsedAt() {
			return items[i].lastUsedAt() > items[j].lastUsedAt()
		}
		return items[i].ID > items[j].ID
	})
}

func (item UnassignedGuestLink) lastUsedAt() string {
	if item.LastUsedAt == nil {
		return ""
	}
	return *item.LastUsedAt
}

// guestNameSimilar 使用 #337 拍板的老链接推荐规则：只去掉访客名字的部门
// 前缀；备注和自报称呼原样参与包含判断，共享姓氏只用于自报称呼。
func guestNameSimilar(target, note, claim string) bool {
	target = compactGuestText(stripDepartmentPrefix(target))
	if target == "" {
		return false
	}

	if guestTextContains(target, compactGuestText(strings.TrimSpace(note))) ||
		guestTextContains(target, compactGuestText(strings.TrimSpace(claim))) {
		return true
	}

	targetRunes := []rune(target)
	claimRunes := []rune(compactGuestText(strings.TrimSpace(claim)))
	return len(targetRunes) > 0 && len(claimRunes) > 0 &&
		unicode.IsLetter(targetRunes[0]) && unicode.IsLetter(claimRunes[0]) &&
		targetRunes[0] == claimRunes[0]
}

func guestTextContains(target, candidate string) bool {
	if utf8.RuneCountInString(candidate) < 2 {
		return false
	}
	return strings.Contains(target, candidate) || strings.Contains(candidate, target)
}

func compactGuestText(s string) string {
	return strings.Join(strings.Fields(s), "")
}

func normalizeGuestName(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

func stripDepartmentPrefix(s string) string {
	if before, after, found := strings.Cut(s, "-"); found && strings.TrimSpace(before) != "" {
		return strings.TrimSpace(after)
	}
	return strings.TrimSpace(s)
}
