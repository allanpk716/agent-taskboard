// 访客链接的生命周期（#32）：创建、列表、撤销与延期。都由成员（含成员 Agent）
// 操作，任何成员都能为任意工单创建；创建、撤销、延期、换新地址各记一条时间线事件，
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
	"github.com/allanpk716/agent-taskboard/internal/guestcredentials"
	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// 访客链接管理的校验错误；HTTP 层把它们映射成 4xx 错误码。
var (
	// ErrNoteRequired 表示创建访客链接时没写备注。
	ErrNoteRequired = errors.New("访客链接需要备注，如「检验科-张三」")
	// ErrGuestRequired 表示创建访客链接时既没带已有访客，也没带新访客名字。
	ErrGuestRequired = errors.New("创建访客链接需要选择一位已有访客，或填写新访客名字")
	// ErrGuestBothProvided 表示已有访客与新访客名字同时出现（只能二选一）。
	ErrGuestBothProvided = errors.New("已有访客与新访客名字只能二选一")
	// ErrGuestNotFound 表示 guest_id 指向的访客不存在。
	ErrGuestNotFound = errors.New("没有这位访客")
	// ErrGuestDisabled 表示访客已停用，主页与名下链接都不可用。
	ErrGuestDisabled = errors.New("这位访客已停用")
	// ErrGuestManagementNotAllowed 表示当前成员不能管理这位访客。
	ErrGuestManagementNotAllowed = errors.New("你不能管理这位访客")
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
	// ErrGuestLinkCipherUnavailable 表示服务端加密密钥不可用，不能加密或解密可再次复制的链接。
	ErrGuestLinkCipherUnavailable = errors.New("访客链接加密密钥不可用，暂不能直接复制或换新地址")
	// ErrGuestShareTextUnavailable 表示老链接没有可恢复的密钥，不能直接复制转发文案。
	ErrGuestShareTextUnavailable = errors.New("这是老访客链接，平台没有保存可恢复的密钥")
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
	GuestID      int64  // 选择已有访客（与 NewGuestName 互斥）
	NewGuestName string // 当场新建访客的名字
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
	expiresAt, err := spec.validate(now)
	if err != nil {
		return time.Time{}, err
	}
	spec.NewGuestName = strings.TrimSpace(spec.NewGuestName)
	if spec.GuestID == 0 && spec.NewGuestName == "" {
		return time.Time{}, ErrGuestRequired
	}
	if spec.GuestID != 0 && spec.NewGuestName != "" {
		return time.Time{}, ErrGuestBothProvided
	}
	return expiresAt, nil
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

// GuestLinkCreated 是创建访客链接的结果。Key 给 HTTP 创建响应还原一次完整地址；
// 服务端另存加密密钥，供之后再次生成转发文案。
type GuestLinkCreated struct {
	ID           int64
	Key          string
	HomeKey      string
	Note         string
	GuestID      int64
	GuestName    string
	Claimed      bool
	ExpiresAt    time.Time
	CanEditBrief bool
}

// CreateGuestLink 以 viewer（成员或成员 Agent）的身份为 ref 指向的工单创建一条
// 访客链接，记一条 guest_link_created 事件（带备注）。
func (m *Manager) CreateGuestLink(ctx context.Context, viewer identity.Viewer, ref string, spec GuestLinkSpec) (GuestLinkCreated, error) {
	full, err := m.ForViewer(ctx, viewer, ref)
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
// （带备注）；链接与工单在同一个事务里创建（#64「请人反馈」）或独立创建
// （#32）共用这里，备注的规范化与校验也统一在这里（validateCreate）完成。
func (m *Manager) createGuestLinkTx(ctx context.Context, tx *sql.Tx, ticketSeq int64,
	actor Author, spec GuestLinkSpec, now time.Time) (*GuestLinkCreated, error) {
	expiresAt, err := spec.validateCreate(now)
	if err != nil {
		return nil, err
	}
	guestID, guestName, holderID, claimed, err := m.guestForNewLinkTx(ctx, tx, actor, spec, now)
	if err != nil {
		return nil, err
	}
	home, homeErr := m.ensureGuestHomeKeyTx(ctx, tx, guestID)
	if homeErr != nil {
		return nil, homeErr
	}
	key, err := identity.NewGuestKey()
	if err != nil {
		return nil, err
	}
	keyCiphertext, err := m.encryptedGuestLinkKey(key)
	if err != nil {
		return nil, err
	}
	var agentID any
	if actor.AgentID != 0 {
		agentID = actor.AgentID
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO guest_links (ticket_seq, guest_id, credential_holder_id, key_hash, key_ciphertext,
		                         note, expires_at, can_edit_brief, created_by_member_id,
		                         created_by_agent_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id`,
		ticketSeq, nullInt(guestID), holderID, identity.CredentialHash(key), keyCiphertext,
		spec.Note, expiresAt.Format(time.RFC3339Nano),
		boolToInt(spec.CanEditBrief), actor.MemberID, agentID, now.Format(time.RFC3339Nano),
	).Scan(&id); err != nil {
		return nil, fmt.Errorf("写入访客链接失败：%w", err)
	}
	if err := m.insertLinkEvent(ctx, tx, ticketSeq, actor, "guest_link_created",
		spec.Note, "", now); err != nil {
		return nil, err
	}
	return &GuestLinkCreated{
		ID: id, Key: key, Note: spec.Note, GuestID: guestID, GuestName: guestName,
		Claimed: claimed, ExpiresAt: expiresAt, CanEditBrief: spec.CanEditBrief,
		HomeKey: home.Key,
	}, nil
}

// guestForNewLinkTx 解析新链接的归属访客：选择已有访客或当场新建一位，
// 并返回该访客的凭证持有者。新访客与持有者在调用方事务里一起写入，
// 保证链接、访客与凭证要么全部成功、要么全部回滚。
func (m *Manager) guestForNewLinkTx(ctx context.Context, tx *sql.Tx, actor Author,
	spec GuestLinkSpec, now time.Time) (guestID int64, guestName string, holderID int64, claimed bool, err error) {
	if spec.GuestID != 0 {
		var claimedAt sql.NullString
		if err := tx.QueryRowContext(ctx, `
			SELECT gu.id, gu.name, h.claimed_at
			FROM guests gu
			JOIN guest_credential_holders h ON h.owner_type = 'guest' AND h.owner_id = gu.id
			WHERE gu.id = ?`, spec.GuestID).Scan(&guestID, &guestName, &claimedAt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return 0, "", 0, false, ErrGuestNotFound
			}
			return 0, "", 0, false, err
		}
		claimed = claimedAt.Valid
	} else {
		var agentID any
		if actor.AgentID != 0 {
			agentID = actor.AgentID
		}
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO guests (name, created_by_member_id, created_by_agent_id, created_at)
			VALUES (?, ?, ?, ?) RETURNING id`,
			spec.NewGuestName, actor.MemberID, agentID, now.Format(time.RFC3339Nano)).
			Scan(&guestID); err != nil {
			return 0, "", 0, false, fmt.Errorf("写入访客失败：%w", err)
		}
		guestName = spec.NewGuestName
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM guest_credential_holders WHERE owner_type = 'guest' AND owner_id = ?`, guestID).
		Scan(&holderID); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, "", 0, false, err
		}
		holder, err := guestcredentials.CreateGuestHolder(ctx, tx, guestID, now)
		if err != nil {
			return 0, "", 0, false, err
		}
		holderID = holder.ID
	}
	return guestID, guestName, holderID, claimed, nil
}

// GuestLinkInfo 是访客链接的列表项：不含明文密钥。keyCiphertext 只在服务端
// 内部用来判断能否再次复制，不进入 HTTP 响应。
type GuestLinkInfo struct {
	ID                 int64
	Note               string
	GuestID            int64
	GuestName          string
	ExpiresAt          time.Time
	RevokedAt          *time.Time
	ClaimedAt          *time.Time
	AccessCodeLockedAt *time.Time
	ClaimName          string
	CanEditBrief       bool
	CreatedBy          Author
	CreatedAt          time.Time
	UseCount           int
	DeviceCount        int
	keyCiphertext      string
	ShareTextAvailable bool
}

// GuestLinks 列出 ref 指向的工单的全部访客链接（成员视角；访客 403 members_only
// 在 HTTP 层拦截）。
func (m *Manager) GuestLinks(ctx context.Context, ref string) ([]GuestLinkInfo, error) {
	full, err := m.ByRef(ctx, ref)
	if err != nil {
		return nil, err
	}
	links, err := GuestLinksForTicket(ctx, m.db, full.Seq)
	if err != nil {
		return nil, err
	}
	now := m.clk.Now()
	for i := range links {
		links[i].ShareTextAvailable = links[i].RevokedAt == nil &&
			now.Before(links[i].ExpiresAt) && m.guestLinkCipher != nil &&
			m.guestLinkCipher.CanDecrypt(links[i].keyCiphertext)
	}
	return links, nil
}

// GuestLinksForTicket 读取一张工单的全部访客链接状态；db 与写事务都可以传入，
// 通知和等待方推导因此在同一事务快照上工作。
func GuestLinksForTicket(ctx context.Context, q Queryer, ticketSeq int64) ([]GuestLinkInfo, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT g.id, g.key_ciphertext, g.note, g.expires_at, g.revoked_at,
		       cred.claimed_at, cred.claim_name, cred.access_code_locked_at,
		       g.can_edit_brief, g.created_at,
		       g.guest_id, gu.name,
		       m.id, m.name, a.id, a.name, a.revoked_at,
		       (SELECT COUNT(*) FROM timeline_events e
		         WHERE e.actor_link_id = g.id AND e.type != 'guest_link_claimed'),
		       (SELECT COUNT(*) FROM guest_credential_devices d
		         WHERE d.credential_holder_id = g.credential_holder_id)
		FROM guest_links g
		JOIN guest_credential_holders cred ON cred.id = g.credential_holder_id
		LEFT JOIN guests gu ON gu.id = g.guest_id
		LEFT JOIN members m ON m.id = g.created_by_member_id
		LEFT JOIN agent_tokens a ON a.id = g.created_by_agent_id
		WHERE g.ticket_seq = ? ORDER BY g.id`, ticketSeq)
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
			claimedAt    sql.NullString
			claimName    sql.NullString
			lockedAt     sql.NullString
			keyCipher    sql.NullString
			guestID      sql.NullInt64
			guestName    sql.NullString
		)
		if err := rows.Scan(&info.ID, &keyCipher, &info.Note, &expiresAt, &revokedAt, &claimedAt, &claimName,
			&lockedAt, &canEdit, &createdAt, &guestID, &guestName, &memberID, &memberName, &agentID, &agentName, &agentRevoked,
			&info.UseCount, &info.DeviceCount); err != nil {
			return nil, err
		}
		info.GuestID = guestID.Int64
		info.GuestName = guestName.String
		if info.ExpiresAt, err = parseTime(expiresAt); err != nil {
			return nil, err
		}
		if info.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		info.ClaimName = claimName.String
		info.keyCiphertext = keyCipher.String
		if claimedAt.Valid {
			t, err := parseTime(claimedAt.String)
			if err != nil {
				return nil, err
			}
			info.ClaimedAt = &t
		}
		if lockedAt.Valid {
			t, err := parseTime(lockedAt.String)
			if err != nil {
				return nil, err
			}
			info.AccessCodeLockedAt = &t
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

// GuestInfo 是部门访客名单的列表项。
type GuestInfo struct {
	ID                 int64
	Name               string
	ClaimedAt          *time.Time
	ClaimName          string
	AccessCodeLockedAt *time.Time
	LinkCount          int
	CreatedBy          Author
	CreatedAt          time.Time
}

// Guests 列出部门访客名单；query 非空时按名字包含匹配（HTTP 层保证只有
// 成员和成员 Agent 能调用）。按名字排序，成员和 Agent 都能稳定查找。
func (m *Manager) Guests(ctx context.Context, query string) ([]GuestInfo, error) {
	query = strings.TrimSpace(query)
	where := ""
	args := []any{}
	if query != "" {
		where = " WHERE gu.name LIKE ? ESCAPE '\\'"
		args = append(args, "%"+escapeLike(query)+"%")
	}
	rows, err := m.db.QueryContext(ctx, `
		SELECT gu.id, gu.name, h.claimed_at, h.claim_name, h.access_code_locked_at,
		       (SELECT COUNT(*) FROM guest_links l WHERE l.guest_id = gu.id),
		       gu.created_by_member_id, mm.name, aa.id, aa.name, aa.revoked_at, gu.created_at
		FROM guests gu
		JOIN guest_credential_holders h ON h.owner_type = 'guest' AND h.owner_id = gu.id
		LEFT JOIN members mm ON mm.id = gu.created_by_member_id
		LEFT JOIN agent_tokens aa ON aa.id = gu.created_by_agent_id`+where+`
		ORDER BY gu.name, gu.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GuestInfo{}
	for rows.Next() {
		var (
			info         GuestInfo
			claimedAt    sql.NullString
			claimName    sql.NullString
			lockedAt     sql.NullString
			memberID     sql.NullInt64
			memberName   sql.NullString
			agentID      sql.NullInt64
			agentName    sql.NullString
			agentRevoked sql.NullString
			createdAt    string
		)
		if err := rows.Scan(&info.ID, &info.Name, &claimedAt, &claimName, &lockedAt,
			&info.LinkCount, &memberID, &memberName, &agentID, &agentName, &agentRevoked, &createdAt); err != nil {
			return nil, err
		}
		var err error
		if info.ClaimedAt, err = nullableTime(claimedAt); err != nil {
			return nil, err
		}
		if info.AccessCodeLockedAt, err = nullableTime(lockedAt); err != nil {
			return nil, err
		}
		if info.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		info.ClaimName = claimName.String
		info.CreatedBy = Author{
			MemberID: memberID.Int64, MemberName: memberName.String,
			AgentID: agentID.Int64, AgentName: agentName.String,
			AgentRevoked: agentRevoked.Valid && agentRevoked.String != "",
		}
		out = append(out, info)
	}
	return out, rows.Err()
}

func nullableTime(v sql.NullString) (*time.Time, error) {
	if !v.Valid {
		return nil, nil
	}
	at, err := parseTime(v.String)
	if err != nil {
		return nil, err
	}
	return &at, nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// GuestDefaultLink 返回长期访客最早发出且仍有效的链接，供会话接口沿用
// “贴一条凭证就能查到工单”的旧用法；完整的名下工单列表属于访客主页（#329）。
func (m *Manager) GuestDefaultLink(ctx context.Context, guestID int64) (*identity.GuestLink, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT g.id, g.ticket_seq, g.note, g.expires_at, g.can_edit_brief,
		       g.guest_id, gu.name, g.credential_holder_id
		FROM guest_links g
		JOIN guests gu ON gu.id = g.guest_id
		WHERE g.guest_id = ? AND g.revoked_at IS NULL AND gu.disabled_at IS NULL
		ORDER BY g.id`, guestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := m.clk.Now()
	for rows.Next() {
		var (
			link      identity.GuestLink
			expiresAt string
			canEdit   int
			guestName string
		)
		if err := rows.Scan(&link.ID, &link.TicketSeq, &link.Note, &expiresAt, &canEdit,
			&link.GuestID, &guestName, &link.CredentialHolderID); err != nil {
			return nil, err
		}
		expiry, err := time.ParseInLocation(time.RFC3339Nano, expiresAt, clock.Beijing)
		if err != nil {
			return nil, err
		}
		if !now.Before(expiry) {
			continue
		}
		link.ExpiresAt = expiry
		link.CanEditBrief = canEdit == 1
		link.GuestName = guestName
		return &link, nil
	}
	return nil, rows.Err()
}

// RevokeGuestLink 撤销 ref 工单下的一条访客链接，立即生效，记一条
// guest_link_revoked 事件。已撤销的再撤销是幂等成功，不重复记事件。
func (m *Manager) RevokeGuestLink(ctx context.Context, viewer identity.Viewer, ref string, id int64) error {
	full, err := m.ForViewer(ctx, viewer, ref)
	if err != nil {
		return err
	}
	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	row, err := m.linkForTicketTx(ctx, tx, full.Seq, id)
	if err != nil {
		return err
	}
	if !row.RevokedAt.Valid {
		if _, err := tx.ExecContext(ctx,
			`UPDATE guest_links SET revoked_at = ? WHERE id = ?`,
			now.Format(time.RFC3339Nano), id); err != nil {
			return err
		}
		if err := m.insertLinkEvent(ctx, tx, full.Seq, authorOf(viewer), "guest_link_revoked",
			row.Note, "", now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ExtendGuestLink 把 ref 工单下的一条访客链接延期到新的有效期，记一条
// guest_link_extended 事件（带备注与新到期时刻）。已撤销或已过期的链接不能延期。
func (m *Manager) ExtendGuestLink(ctx context.Context, viewer identity.Viewer, ref string, id int64, spec GuestLinkSpec) (GuestLinkInfo, error) {
	full, err := m.ForViewer(ctx, viewer, ref)
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

	row, err := m.activeLinkRowForTicketTx(ctx, tx, full.Seq, id, now)
	if err != nil {
		return GuestLinkInfo{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE guest_links SET expires_at = ? WHERE id = ?`,
		expiresAt.Format(time.RFC3339Nano), id); err != nil {
		return GuestLinkInfo{}, err
	}
	if err := m.insertLinkEvent(ctx, tx, full.Seq, authorOf(viewer), "guest_link_extended",
		row.Note, expiresAt.Format(time.RFC3339Nano), now); err != nil {
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

// guestLinkRow 是复制转发文案、换新地址、撤销和延期共用的链接行。
type guestLinkRow struct {
	Note          string
	GuestID       int64
	GuestName     string
	ExpiresAt     string
	RevokedAt     sql.NullString
	ClaimedAt     sql.NullString
	KeyCiphertext sql.NullString
	HolderID      int64
	ExpiresAtTime time.Time
}

// linkForTicketTx 取属于该工单的一条链接；不属于或不存在报 ErrGuestLinkNotFound
// （避免跨工单误操作别的链接）。
func (m *Manager) linkForTicketTx(ctx context.Context, q Queryer, ticketSeq, id int64) (guestLinkRow, error) {
	var (
		row guestLinkRow
	)
	var guestID sql.NullInt64
	var guestName sql.NullString
	err := q.QueryRowContext(ctx, `
		SELECT g.note, g.expires_at, g.revoked_at, cred.claimed_at,
		       g.key_ciphertext, g.credential_holder_id, g.guest_id, gu.name
		FROM guest_links g
		JOIN guest_credential_holders cred ON cred.id = g.credential_holder_id
		LEFT JOIN guests gu ON gu.id = g.guest_id
		WHERE g.id = ? AND g.ticket_seq = ?`, id, ticketSeq).
		Scan(&row.Note, &row.ExpiresAt, &row.RevokedAt, &row.ClaimedAt,
			&row.KeyCiphertext, &row.HolderID, &guestID, &guestName)
	row.GuestID, row.GuestName = guestID.Int64, guestName.String
	if errors.Is(err, sql.ErrNoRows) {
		return guestLinkRow{}, ErrGuestLinkNotFound
	}
	if err != nil {
		return guestLinkRow{}, err
	}
	return row, nil
}

// activeLinkRowForTicketTx 在 linkForTicketTx 的基础上解析有效期，并统一
// 拒绝已撤销或已过期的链接。
func (m *Manager) activeLinkRowForTicketTx(ctx context.Context, q Queryer, ticketSeq, id int64, now time.Time) (guestLinkRow, error) {
	row, err := m.linkForTicketTx(ctx, q, ticketSeq, id)
	if err != nil {
		return guestLinkRow{}, err
	}
	expiresAt, err := parseTime(row.ExpiresAt)
	if err != nil {
		return guestLinkRow{}, err
	}
	if row.RevokedAt.Valid {
		return guestLinkRow{}, &LinkNotExtendableError{Reason: "revoked"}
	}
	if !now.Before(expiresAt) {
		return guestLinkRow{}, &LinkNotExtendableError{Reason: "expired"}
	}
	row.ExpiresAtTime = expiresAt
	return row, nil
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
