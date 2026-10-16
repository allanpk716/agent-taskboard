// Package identity 是「身份与凭证」模块：成员账号、密码校验和网页会话。
// 其余模块只认这里产出的身份，不关心凭证是 cookie 还是 token（token 由后续工单加入）。
package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// Login 校验用户名和密码，成功则创建一条会话。
func (m *Manager) Login(ctx context.Context, username, password string) (Session, error) {
	var (
		id           int64
		name         string
		passwordHash string
		isAdmin      int
	)
	err := m.db.QueryRowContext(ctx, `SELECT id, name, password_hash, is_admin FROM members WHERE username = ?`, username).
		Scan(&id, &name, &passwordHash, &isAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, err
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
		expiresAt string
		sess      Session
	)
	err := m.db.QueryRowContext(ctx, `
		SELECT s.id, s.expires_at, m.id, m.username, m.name, m.is_admin
		FROM sessions s JOIN members m ON m.id = s.member_id
		WHERE s.id = ?`, id).
		Scan(&sess.ID, &expiresAt, &sess.Member.ID, &sess.Member.Username, &sess.Member.Name, &sess.Member.IsAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, err
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
