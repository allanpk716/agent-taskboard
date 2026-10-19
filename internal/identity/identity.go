// Package identity 是「身份与凭证」模块：成员账号、密码校验和网页会话。
// 其余模块只认这里产出的身份，不关心凭证是 cookie 还是 token（token 由后续工单加入）。
package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"golang.org/x/crypto/bcrypt"
)

// SessionTTL 是网页会话的有效期。
const SessionTTL = 7 * 24 * time.Hour

// ErrUsernameTaken 表示登录名已存在。
var ErrUsernameTaken = errors.New("登录名已存在")

// ErrInvalidCredentials 表示用户名或密码不正确。
var ErrInvalidCredentials = errors.New("用户名或密码不正确")

// ErrMemberDeactivated 表示账号已停用（#42：管理员在网页上停用）。
var ErrMemberDeactivated = errors.New("账号已停用")

// ErrMemberNotFound 表示没有这位成员（#42 账号管理）。
var ErrMemberNotFound = errors.New("没有这位成员")

// ErrCannotDeactivateSelf 表示不能停用自己的账号（#42：防止管理员把自己锁在门外）。
var ErrCannotDeactivateSelf = errors.New("不能停用自己的账号")

// ErrMemberInactive 表示目标账号已停用，不能执行当前管理操作。
var ErrMemberInactive = errors.New("成员账号已停用")
var ErrCannotResetSelf = errors.New("不能重置自己的密码")
var ErrInvalidOldPassword = errors.New("原密码不正确")
var ErrPasswordTooShort = errors.New("新密码至少 8 位")
var ErrPasswordUnchanged = errors.New("新密码与旧密码相同")

// Member 是一位成员。
type Member struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	IsAdmin  bool   `json:"is_admin"`
}

// Session 是一次网页登录会话。
type Session struct {
	ID     string
	Member Member
	// ExpiresAt 是过期时刻（北京时间）。
	ExpiresAt time.Time
}

// Manager 管理成员与网页会话。
type Manager struct {
	db    *sql.DB
	clock clock.Clock

	clientMu    sync.Mutex
	clientCodes map[string]clientConnectCode
}

func NewManager(db *sql.DB, c clock.Clock) *Manager {
	return &Manager{db: db, clock: c}
}

// CreateAdmin 创建一位管理员成员。首个管理员在首次部署时由命令行创建。
func (m *Manager) CreateAdmin(ctx context.Context, username, name, password string) (Member, error) {
	return m.createMember(ctx, username, name, password, true)
}

// CreateMember 创建一位普通成员（并非常理员）；成员账号管理接口（#42）走这里。
func (m *Manager) CreateMember(ctx context.Context, username, name, password string) (Member, error) {
	return m.createMember(ctx, username, name, password, false)
}

func (m *Manager) createMember(ctx context.Context, username, name, password string, isAdmin bool) (Member, error) {
	if username == "" || password == "" {
		return Member{}, fmt.Errorf("登录名和密码都不能为空")
	}
	if name == "" {
		name = username
	}
	var exists int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM members WHERE username = ?`, username).Scan(&exists); err != nil {
		return Member{}, err
	}
	if exists > 0 {
		return Member{}, ErrUsernameTaken
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return Member{}, err
	}
	member := Member{Username: username, Name: name, IsAdmin: isAdmin}
	err = m.db.QueryRowContext(ctx, `
		INSERT INTO members (username, name, password_hash, is_admin, created_at)
		VALUES (?, ?, ?, ?, ?)
		RETURNING id`,
		username, name, string(hash), boolToInt(isAdmin), m.clock.Now().Format(time.RFC3339Nano),
	).Scan(&member.ID)
	if err != nil {
		return Member{}, err
	}
	return member, nil
}

// MemberRow 是成员账号的管理视图（#42）：成员信息加上启用状态。
type MemberRow struct {
	Member
	// DeactivatedAt 非空表示已停用。
	DeactivatedAt *time.Time
	Triager       bool
}

// Active 报告账号是否启用。
func (r MemberRow) Active() bool { return r.DeactivatedAt == nil }

// ListMembersDetailed 列出全部成员及其启用状态（#42 管理页的成员账号表）。
// 停用的成员仍列出：历史署名与事件保留，页面上要能对上人。
func (m *Manager) ListMembersDetailed(ctx context.Context) ([]MemberRow, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, username, name, is_admin, deactivated_at, triager FROM members ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MemberRow{}
	for rows.Next() {
		var (
			r           MemberRow
			isAdmin     int
			deactivated sql.NullString
			triager     int
		)
		if err := rows.Scan(&r.ID, &r.Username, &r.Name, &isAdmin, &deactivated, &triager); err != nil {
			return nil, err
		}
		r.IsAdmin = isAdmin == 1
		r.Triager = triager == 1
		if deactivated.Valid && deactivated.String != "" {
			t, err := time.ParseInLocation(time.RFC3339Nano, deactivated.String, clock.Beijing)
			if err != nil {
				return nil, fmt.Errorf("解析成员停用时间失败：%w", err)
			}
			r.DeactivatedAt = &t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetTriager 设置成员的显式初筛人标记（#75）。停用成员也可以保留标记；
// 是否生效由调用方按启用状态过滤。
func (m *Manager) SetTriager(ctx context.Context, id int64, triager bool) error {
	res, err := m.db.ExecContext(ctx, `UPDATE members SET triager = ? WHERE id = ?`, boolToInt(triager), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrMemberNotFound
	}
	return nil
}

// GenerateInitialPassword 生成一位新成员的初始密码（创建成员账号时由服务器
// 生成，只在创建响应里出现一次，#42）。
func GenerateInitialPassword() (string, error) {
	// 去掉易混字符的字母表，够长且方便口头转述。
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"
	buf := make([]byte, 12)
	if _, err := randRead(buf); err != nil {
		return "", err
	}
	for i, b := range buf {
		buf[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(buf), nil
}

// DeactivateMember 停用成员账号（#42）：账号不能再登录、已有会话与其名下
// Agent token 立即失效；停用不是删除，成员与历史内容保留。幂等：已停用的
// 再停用是成功。返回是否发生了变化。
func (m *Manager) DeactivateMember(ctx context.Context, id int64) (bool, error) {
	var deactivated sql.NullString
	err := m.db.QueryRowContext(ctx, `SELECT deactivated_at FROM members WHERE id = ?`, id).Scan(&deactivated)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrMemberNotFound
	}
	if err != nil {
		return false, err
	}
	if deactivated.Valid {
		return false, nil // 已停用，幂等
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	now := m.clock.Now().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
		UPDATE members SET deactivated_at = ? WHERE id = ?`,
		now, id); err != nil {
		return false, err
	}
	// 已登录的会话一并清掉：停用即时生效。
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE member_id = ?`, id); err != nil {
		return false, err
	}
	if err := m.revokeAllClientCredentialsTx(ctx, tx, id); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE agent_tokens SET revoked_at = ? WHERE member_id = ? AND revoked_at IS NULL`,
		now, id); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// ReactivateMember 恢复成员账号（#112）：只清停用标记。密码、历史会话、
// Agent token 与客户端凭证都不变，因此停用时撤销的 token 不会复活。
// 已启用账号的再恢复是幂等成功。返回是否发生了变化。
func (m *Manager) ReactivateMember(ctx context.Context, id int64) (bool, error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var deactivated sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT deactivated_at FROM members WHERE id = ?`, id).Scan(&deactivated)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrMemberNotFound
	}
	if err != nil {
		return false, err
	}
	if !nullStringSet(deactivated) {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE members SET deactivated_at = NULL WHERE id = ?`, id); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// ChangeOwnPassword 自助改密：保留当前网页会话，注销其他网页会话；
// 客户端凭证与 Agent token 不动（ADR 0004）。
func (m *Manager) ChangeOwnPassword(ctx context.Context, memberID int64, currentSessionID, oldPassword, newPassword string) error {
	if len(newPassword) < 8 {
		return ErrPasswordTooShort
	}

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var hash string
	err = tx.QueryRowContext(ctx,
		`SELECT password_hash FROM members WHERE id = ?`, memberID).Scan(&hash)
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(oldPassword)) != nil {
		return ErrInvalidOldPassword
	}
	if oldPassword == newPassword {
		return ErrPasswordUnchanged
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE members SET password_hash = ? WHERE id = ?`, string(newHash), memberID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM sessions WHERE member_id = ? AND id <> ?`, memberID, currentSessionID); err != nil {
		return err
	}
	return tx.Commit()
}

// AdminResetMemberPassword 管理员重置密码：生成一次性初始密码，并撤销会话
// 与全部客户端凭证（ADR 0004）。
func (m *Manager) AdminResetMemberPassword(ctx context.Context, adminID, memberID int64) (string, error) {
	if adminID == memberID {
		return "", ErrCannotResetSelf
	}
	password, err := GenerateInitialPassword()
	if err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	// 停用检查放进 UPDATE 条件：与并发停用串行化，不会给已停用账号发密码（#113）。
	res, err := tx.ExecContext(ctx,
		`UPDATE members SET password_hash = ? WHERE id = ? AND deactivated_at IS NULL`,
		string(hash), memberID)
	if err != nil {
		return "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", err
	}
	if n == 0 {
		var deactivated sql.NullString
		err := tx.QueryRowContext(ctx,
			`SELECT deactivated_at FROM members WHERE id = ?`, memberID).Scan(&deactivated)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrMemberNotFound
		}
		if err != nil {
			return "", err
		}
		if nullStringSet(deactivated) {
			return "", ErrMemberInactive
		}
		return "", ErrMemberNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE member_id = ?`, memberID); err != nil {
		return "", err
	}
	if err := m.revokeAllClientCredentialsTx(ctx, tx, memberID); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return password, nil
}

// Login 校验用户名和密码，成功则创建一条会话。
func (m *Manager) Login(ctx context.Context, username, password string) (Session, error) {
	var (
		id           int64
		name         string
		passwordHash string
		isAdmin      int
		deactivated  sql.NullString
	)
	err := m.db.QueryRowContext(ctx, `
		SELECT id, name, password_hash, is_admin, deactivated_at FROM members WHERE username = ?`, username).
		Scan(&id, &name, &passwordHash, &isAdmin, &deactivated)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, err
	}
	if deactivated.Valid {
		return Session{}, ErrMemberDeactivated
	}
	if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) != nil {
		return Session{}, ErrInvalidCredentials
	}
	return m.createSession(ctx, Member{ID: id, Username: username, Name: name, IsAdmin: isAdmin == 1})
}

// createSession 生成随机会话 id 并落库。
func (m *Manager) createSession(ctx context.Context, member Member) (Session, error) {
	buf := make([]byte, 32)
	if _, err := randRead(buf); err != nil {
		return Session{}, err
	}
	id := base64URLEncode(buf)
	now := m.clock.Now()
	sess := Session{ID: id, Member: member, ExpiresAt: now.Add(SessionTTL)}
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO sessions (id, member_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		sess.ID, member.ID, now.Format(time.RFC3339Nano), sess.ExpiresAt.Format(time.RFC3339Nano))
	if err != nil {
		return Session{}, err
	}
	return sess, nil
}

// SessionByID 按会话 id 取会话；id 不存在或已过期时返回 ErrInvalidCredentials。
func (m *Manager) SessionByID(ctx context.Context, id string) (Session, error) {
	var (
		expiresAt   string
		deactivated sql.NullString
		sess        Session
	)
	err := m.db.QueryRowContext(ctx, `
		SELECT s.id, s.expires_at, m.id, m.username, m.name, m.is_admin, m.deactivated_at
		FROM sessions s JOIN members m ON m.id = s.member_id
		WHERE s.id = ?`, id).
		Scan(&sess.ID, &expiresAt, &sess.Member.ID, &sess.Member.Username, &sess.Member.Name, &sess.Member.IsAdmin, &deactivated)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, err
	}
	// 账号停用后其会话视同失效（#42：停用即时生效）。
	if deactivated.Valid {
		return Session{}, ErrInvalidCredentials
	}
	// is_admin 在库里存 0/1，driver 会转成 bool。
	expiry, err := time.ParseInLocation(time.RFC3339Nano, expiresAt, clock.Beijing)
	if err != nil {
		return Session{}, fmt.Errorf("解析会话过期时间失败：%w", err)
	}
	if !m.clock.Now().Before(expiry) {
		_ = m.DeleteSession(ctx, id)
		return Session{}, ErrInvalidCredentials
	}
	sess.ExpiresAt = expiry
	return sess, nil
}

// DeleteSession 注销会话。
func (m *Manager) DeleteSession(ctx context.Context, id string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullStringSet(v sql.NullString) bool {
	return v.Valid && v.String != ""
}
