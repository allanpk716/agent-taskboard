// 访客链接的生命周期（#32）：创建、列表、撤销与延期。都由成员（含成员 Agent）
// 操作，任何成员都能为任意工单创建；创建、撤销、延期各记一条时间线事件，
// 与链接行的写入在同一个事务里推进全局变更序号。
package tickets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// 访客链接管理的校验错误；HTTP 层把它们映射成 4xx 错误码。
var (
	// ErrNoteRequired 表示创建访客链接时没写备注。
	ErrNoteRequired = errors.New("访客链接需要备注，如「检验科-张三」")
	// ErrInvalidValidDays 表示 valid_days 不是 7/30/90 之一（或与 expires_at 并存）。
	ErrInvalidValidDays = errors.New("有效期只能选 7、30 或 90 天，或改用 expires_at 指定日期（二选一）")
	// ErrInvalidExpiresAt 表示 expires_at 不是合法时间或不晚于现在（没有永久选项）。
	ErrInvalidExpiresAt = errors.New("expires_at 必须是晚于当前时间的日期或时刻；访客链接没有永久有效的选项")
	// ErrGuestLinkNotFound 表示这个工单下没有这条访客链接。
	ErrGuestLinkNotFound = errors.New("没有这条访客链接")
	// ErrLinkCannotEditBrief 表示这条访客链接没有「可填写说明」权限。
	ErrLinkCannotEditBrief = errors.New("这条访客链接没有「可填写说明」权限")
	// ErrTicketClosed 表示工单已关闭，访客链接只读。
	ErrTicketClosed = errors.New("工单已关闭，访客链接只读")
)

// LinkNotExtendableError 是对已撤销或已过期的链接延期（409）。
type LinkNotExtendableError struct {
	Reason string // revoked | expired
}

func (e *LinkNotExtendableError) Error() string {
	if e.Reason == "revoked" {
		return "访客链接已被撤销，不能延期"
	}
	return "访客链接已过期，不能延期"
}

// GuestLinkSpec 是访客链接的有效期写法：valid_days（7/30/90）或 expires_at 二选一；
// 都不填时默认 30 天。CanEditBrief 是「可填写说明」权限。
type GuestLinkSpec struct {
	Note         string
	ValidDays    int        // 0 = 未提供
	ExpiresAt    *time.Time // 与 ValidDays 互斥；日期字符串已由 HTTP 层解析
	CanEditBrief bool
}

// validateCreate 校验并规范化「创建」一条访客链接的输入（与延期不同，创建
// 必须有备注）：备注去空白、必填，有效期合法并算出到期时刻。写入前由
// createGuestLinkTx 调用，单建（#32）与「请人反馈」（#64）两个入口共用；
// 「请人反馈」在建单前先调它快速失败，任何校验失败都不动工单。
func (spec *GuestLinkSpec) validateCreate(now time.Time) (time.Time, error) {
	spec.Note = strings.TrimSpace(spec.Note)
	if spec.Note == "" {
		return time.Time{}, ErrNoteRequired
	}
	return spec.validate(now)
}

// validate 校验并算出到期时刻（不检查备注：延期请求不带备注）。
func (spec GuestLinkSpec) validate(now time.Time) (time.Time, error) {
	if spec.ValidDays != 0 && spec.ExpiresAt != nil {
		return time.Time{}, ErrInvalidValidDays // 二选一
	}
	switch spec.ValidDays {
	case 0:
		if spec.ExpiresAt == nil {
			return now.Add(30 * 24 * time.Hour), nil // 默认 30 天
		}
		if !spec.ExpiresAt.After(now) {
			return time.Time{}, ErrInvalidExpiresAt
		}
		return *spec.ExpiresAt, nil
	case 7, 30, 90:
		return now.Add(time.Duration(spec.ValidDays) * 24 * time.Hour), nil
	default:
		return time.Time{}, ErrInvalidValidDays
	}
}

// GuestLinkCreated 是创建访客链接的结果。Key 是明文密钥，只在创建响应里出现一次。
type GuestLinkCreated struct {
	ID           int64
	Key          string
	Note         string
	ExpiresAt    time.Time
	CanEditBrief bool
}

// CreateGuestLink 以 viewer（成员或成员 Agent）的身份为 ref 指向的工单创建一条
// 访客链接，记一条 guest_link_created 事件（带备注）。
func (m *Manager) CreateGuestLink(ctx context.Context, viewer identity.Viewer, ref string, spec GuestLinkSpec) (GuestLinkCreated, error) {
	full, err := m.ByRef(ctx, ref)
	if err != nil {
		return GuestLinkCreated{}, err
	}
	// 校验（备注、有效期）由 createGuestLinkTx 里的 validateCreate 统一完成。
	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return GuestLinkCreated{}, err
	}
	defer tx.Rollback()

	link, err := m.createGuestLinkTx(ctx, tx, full.Seq, authorOf(viewer), spec, now)
	if err != nil {
		return GuestLinkCreated{}, err
	}
	if err := tx.Commit(); err != nil {
		return GuestLinkCreated{}, err
	}
	return *link, nil
}

// createGuestLinkTx 在已有事务里写入一条访客链接并记 guest_link_created 事件
//（带备注）；链接与工单在同一个事务里创建（#64「请人反馈」）或独立创建
//（#32）共用这里，备注的规范化与校验也统一在这里（validateCreate）完成。
func (m *Manager) createGuestLinkTx(ctx context.Context, tx *sql.Tx, ticketSeq int64,
	actor Author, spec GuestLinkSpec, now time.Time) (*GuestLinkCreated, error) {
	expiresAt, err := spec.validateCreate(now)
	if err != nil {
		return nil, err
	}
	key, err := identity.NewGuestKey()
	if err != nil {
		return nil, err
	}
	var agentID any
	if actor.AgentID != 0 {
		agentID = actor.AgentID
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO guest_links (ticket_seq, key_hash, note, expires_at, can_edit_brief,
		                         created_by_member_id, created_by_agent_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id`,
		ticketSeq, identity.CredentialHash(key), spec.Note, expiresAt.Format(time.RFC3339Nano),
		boolToInt(spec.CanEditBrief), actor.MemberID, agentID, now.Format(time.RFC3339Nano),
	).Scan(&id); err != nil {
		return nil, fmt.Errorf("写入访客链接失败：%w", err)
	}
	if err := m.insertLinkEvent(ctx, tx, ticketSeq, actor, "guest_link_created",
		spec.Note, "", now); err != nil {
		return nil, err
	}
	return &GuestLinkCreated{
		ID: id, Key: key, Note: spec.Note, ExpiresAt: expiresAt, CanEditBrief: spec.CanEditBrief,
	}, nil
}

// GuestLinkInfo 是访客链接的列表项：不含密钥（明文只在创建响应里出现一次）。
type GuestLinkInfo struct {
	ID           int64
	Note         string
	ExpiresAt    time.Time
	RevokedAt    *time.Time
	CanEditBrief bool
	CreatedBy    Author
	CreatedAt    time.Time
	UseCount     int
}

// GuestLinks 列出 ref 指向的工单的全部访客链接（成员视角；访客 403 members_only
// 在 HTTP 层拦截）。
func (m *Manager) GuestLinks(ctx context.Context, ref string) ([]GuestLinkInfo, error) {
	full, err := m.ByRef(ctx, ref)
	if err != nil {
		return nil, err
	}
	rows, err := m.db.QueryContext(ctx, `
		SELECT g.id, g.note, g.expires_at, g.revoked_at, g.can_edit_brief, g.created_at,
		       m.id, m.name, a.id, a.name, a.revoked_at,
		       (SELECT COUNT(*) FROM timeline_events e WHERE e.actor_link_id = g.id)
		FROM guest_links g
		LEFT JOIN members m ON m.id = g.created_by_member_id
		LEFT JOIN agent_tokens a ON a.id = g.created_by_agent_id
		WHERE g.ticket_seq = ? ORDER BY g.id`, full.Seq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GuestLinkInfo{}
	for rows.Next() {
		var (
			info         GuestLinkInfo
			expiresAt    string
			createdAt    string
			revokedAt    sql.NullString
			canEdit      int
			memberID     sql.NullInt64
			memberName   sql.NullString
			agentID      sql.NullInt64
			agentName    sql.NullString
			agentRevoked sql.NullString
		)
		if err := rows.Scan(&info.ID, &info.Note, &expiresAt, &revokedAt, &canEdit, &createdAt,
			&memberID, &memberName, &agentID, &agentName, &agentRevoked, &info.UseCount); err != nil {
			return nil, err
		}
		if info.ExpiresAt, err = parseTime(expiresAt); err != nil {
			return nil, err
		}
		if info.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		if revokedAt.Valid {
			t, err := parseTime(revokedAt.String)
			if err != nil {
				return nil, err
			}
			info.RevokedAt = &t
		}
		info.CanEditBrief = canEdit == 1
		info.CreatedBy = Author{
			MemberID: memberID.Int64, MemberName: memberName.String,
			AgentID: agentID.Int64, AgentName: agentName.String,
		}
		out = append(out, info)
	}
	return out, rows.Err()
}

// RevokeGuestLink 撤销 ref 工单下的一条访客链接，立即生效，记一条
// guest_link_revoked 事件。已撤销的再撤销是幂等成功，不重复记事件。
func (m *Manager) RevokeGuestLink(ctx context.Context, viewer identity.Viewer, ref string, id int64) error {
	full, err := m.ByRef(ctx, ref)
	if err != nil {
		return err
	}
	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	note, revokedAt, err := m.linkForTicketTx(ctx, tx, full.Seq, id)
	if err != nil {
		return err
	}
	if !revokedAt.Valid {
		if _, err := tx.ExecContext(ctx,
			`UPDATE guest_links SET revoked_at = ? WHERE id = ?`,
			now.Format(time.RFC3339Nano), id); err != nil {
			return err
		}
		if err := m.insertLinkEvent(ctx, tx, full.Seq, authorOf(viewer), "guest_link_revoked",
			note, "", now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ExtendGuestLink 把 ref 工单下的一条访客链接延期到新的有效期，记一条
// guest_link_extended 事件（带备注与新到期时刻）。已撤销或已过期的链接不能延期。
func (m *Manager) ExtendGuestLink(ctx context.Context, viewer identity.Viewer, ref string, id int64, spec GuestLinkSpec) (GuestLinkInfo, error) {
	full, err := m.ByRef(ctx, ref)
	if err != nil {
		return GuestLinkInfo{}, err
	}
	now := m.clk.Now()
	expiresAt, err := spec.validate(now)
	if err != nil {
		return GuestLinkInfo{}, err
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return GuestLinkInfo{}, err
	}
	defer tx.Rollback()

	var currentExpiresAt string
	note, revokedAt, err := m.linkRowForTicketTx(ctx, tx, full.Seq, id, &currentExpiresAt)
	if err != nil {
		return GuestLinkInfo{}, err
	}
	if revokedAt.Valid {
		return GuestLinkInfo{}, &LinkNotExtendableError{Reason: "revoked"}
	}
	oldExpiry, err := parseTime(currentExpiresAt)
	if err != nil {
		return GuestLinkInfo{}, err
	}
	if !now.Before(oldExpiry) {
		return GuestLinkInfo{}, &LinkNotExtendableError{Reason: "expired"}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE guest_links SET expires_at = ? WHERE id = ?`,
		expiresAt.Format(time.RFC3339Nano), id); err != nil {
		return GuestLinkInfo{}, err
	}
	if err := m.insertLinkEvent(ctx, tx, full.Seq, authorOf(viewer), "guest_link_extended",
		note, expiresAt.Format(time.RFC3339Nano), now); err != nil {
		return GuestLinkInfo{}, err
	}
	if err := tx.Commit(); err != nil {
		return GuestLinkInfo{}, err
	}
	links, err := m.GuestLinks(ctx, ref)
	if err != nil {
		return GuestLinkInfo{}, err
	}
	for _, l := range links {
		if l.ID == id {
			return l, nil
		}
	}
	return GuestLinkInfo{}, ErrGuestLinkNotFound
}

// linkForTicketTx 取属于该工单的一条链接（note 与撤销时刻）；不属于或不存在报
// ErrGuestLinkNotFound（避免跨工单误操作别的链接）。
func (m *Manager) linkForTicketTx(ctx context.Context, tx *sql.Tx, ticketSeq, id int64) (string, sql.NullString, error) {
	var currentExpiresAt string
	return m.linkRowForTicketTx(ctx, tx, ticketSeq, id, &currentExpiresAt)
}

func (m *Manager) linkRowForTicketTx(ctx context.Context, tx *sql.Tx, ticketSeq, id int64, expiresAtOut *string) (string, sql.NullString, error) {
	var (
		note      string
		expiresAt string
		revokedAt sql.NullString
	)
	err := tx.QueryRowContext(ctx,
		`SELECT note, expires_at, revoked_at FROM guest_links WHERE id = ? AND ticket_seq = ?`,
		id, ticketSeq).Scan(&note, &expiresAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", sql.NullString{}, ErrGuestLinkNotFound
	}
	if err != nil {
		return "", sql.NullString{}, err
	}
	if expiresAtOut != nil {
		*expiresAtOut = expiresAt
	}
	return note, revokedAt, nil
}

// insertLinkEvent 写一条访客链接事件（guest_link_created / revoked / extended），
// 复用 appendEvent，附加列随后用 UPDATE 补上（与回复等同一模式）。
func (m *Manager) insertLinkEvent(ctx context.Context, tx *sql.Tx, ticketSeq int64,
	actor Author, eventType, note, expiresAt string, at time.Time) error {
	eventSeq, change, err := m.appendEvent(ctx, tx, ticketSeq, at, actor, eventType)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE timeline_events SET note = ?, link_expires_at = ? WHERE ticket_seq = ? AND seq = ?`,
		note, nullStr(expiresAt), ticketSeq, eventSeq); err != nil {
		return fmt.Errorf("写入 %s 事件的附加字段失败：%w", eventType, err)
	}
	if err := touchLastChange(ctx, tx, ticketSeq, change); err != nil {
		return err
	}
	return nil
}

func parseTime(s string) (time.Time, error) {
	t, err := time.ParseInLocation(time.RFC3339Nano, s, clock.Beijing)
	if err != nil {
		return time.Time{}, fmt.Errorf("解析时间 %q 失败：%w", s, err)
	}
	return t, nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
