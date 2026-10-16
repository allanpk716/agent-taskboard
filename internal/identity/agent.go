package identity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// agentTokenPrefix 让 token 一眼可辨（也避免和将来的访客链接密钥混淆）。
const agentTokenPrefix = "tbag_"

// ErrAgentNameTaken 表示该成员名下已有同名、未撤销的 Agent。
var ErrAgentNameTaken = errors.New("该名字的 Agent 已存在")

// ErrAgentTokenNotFound 表示没有这个属于当前成员的 Agent token。
var ErrAgentTokenNotFound = errors.New("没有这个 Agent token")

// Agent 是挂在某位成员名下的一个具名 Agent（ADR 0003），不是独立账号。
type Agent struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// AgentToken 是一次签发的结果。明文 token 只出现在这里，库与列表都不回显。
type AgentToken struct {
	Agent
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"created_at"`
}

// ViewerKind 是凭证解析后的身份种类。
type ViewerKind string

const (
	// ViewerMember 是成员：网页会话（本人）或 Agent token（具名 Agent）。
	ViewerMember ViewerKind = "member"
	// ViewerGuest 是访客链接（后续工单加入）。
	ViewerGuest ViewerKind = "guest"
)

// Viewer 是把请求上的凭证（网页会话 cookie / Agent token / 访客链接密钥）
// 统一解析后的身份。其余模块只认 Viewer，不碰原始凭证。
type Viewer struct {
	Kind      ViewerKind
	Member    Member     // kind == member 时的成员；Agent 凭证指向其所属成员
	Agent     *Agent     // 凭证是 Agent token 时非 nil；本人（网页会话）为 nil
	GuestLink *GuestLink // 凭证是访客链接密钥时非 nil（#32）
}

// IsWebSession 报告 v 是否为成员本人在网页会话下的操作。
// 创建和撤销 Agent token、创建成员账号只接受这种凭证（ADR 0003）。
func (v Viewer) IsWebSession() bool {
	return v.Kind == ViewerMember && v.Agent == nil
}

// IssueAgentToken 为 member 签发一个具名 Agent token。名字未撤销时不能重复。
func (m *Manager) IssueAgentToken(ctx context.Context, member Member, name string) (AgentToken, error) {
	var exists int
	if err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_tokens WHERE member_id = ? AND name = ? AND revoked_at IS NULL`,
		member.ID, name).Scan(&exists); err != nil {
		return AgentToken{}, err
	}
	if exists > 0 {
		return AgentToken{}, ErrAgentNameTaken
	}

	buf := make([]byte, 32)
	if _, err := randRead(buf); err != nil {
		return AgentToken{}, err
	}
	token := agentTokenPrefix + base64URLEncode(buf)
	now := m.clock.Now()
	tok := AgentToken{
		Agent:     Agent{Name: name},
		Token:     token,
		CreatedAt: now,
	}
	err := m.db.QueryRowContext(ctx, `
		INSERT INTO agent_tokens (member_id, name, token_hash, created_at)
		VALUES (?, ?, ?, ?)
		RETURNING id`,
		member.ID, name, hashToken(token), now.Format(time.RFC3339Nano),
	).Scan(&tok.Agent.ID)
	if err != nil {
		return AgentToken{}, err
	}
	return tok, nil
}

// RevokeAgentToken 撤销 member 名下的一个 Agent token；撤销立即生效。
// 已撤销的再撤销是幂等成功；不属于该成员或不存在的 id 报 ErrAgentTokenNotFound。
func (m *Manager) RevokeAgentToken(ctx context.Context, member Member, id int64) error {
	var revokedAt sql.NullString
	err := m.db.QueryRowContext(ctx,
		`SELECT revoked_at FROM agent_tokens WHERE id = ? AND member_id = ?`, id, member.ID).
		Scan(&revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAgentTokenNotFound
	}
	if err != nil {
		return err
	}
	if revokedAt.Valid {
		return nil // 已撤销，幂等
	}
	_, err = m.db.ExecContext(ctx,
		`UPDATE agent_tokens SET revoked_at = ? WHERE id = ? AND member_id = ?`,
		m.clock.Now().Format(time.RFC3339Nano), id, member.ID)
	return err
}

// ViewerFromAgentToken 把 Agent token 解析成 Viewer；token 未知或已撤销时
// 返回 ErrInvalidCredentials（撤销即时生效，所以每次都实时查库）。
// 成员账号被停用（#42）后，其名下 Agent token 也一并失效。
func (m *Manager) ViewerFromAgentToken(ctx context.Context, token string) (Viewer, error) {
	var (
		agent       Agent
		member      Member
		isAdmin     int
		revokedAt   sql.NullString
		deactivated sql.NullString
	)
	err := m.db.QueryRowContext(ctx, `
		SELECT t.id, t.name, t.revoked_at, m.id, m.username, m.name, m.is_admin, m.deactivated_at
		FROM agent_tokens t JOIN members m ON m.id = t.member_id
		WHERE t.token_hash = ?`, hashToken(token)).
		Scan(&agent.ID, &agent.Name, &revokedAt, &member.ID, &member.Username, &member.Name, &isAdmin, &deactivated)
	if errors.Is(err, sql.ErrNoRows) {
		return Viewer{}, ErrInvalidCredentials
	}
	if err != nil {
		return Viewer{}, err
	}
	if revokedAt.Valid || deactivated.Valid {
		return Viewer{}, ErrInvalidCredentials
	}
	member.IsAdmin = isAdmin == 1
	return Viewer{Kind: ViewerMember, Member: member, Agent: &agent}, nil
}

// ViewerFromSession 把网页会话 id 解析成本人的 Viewer。
func (m *Manager) ViewerFromSession(ctx context.Context, sessionID string) (Viewer, error) {
	sess, err := m.SessionByID(ctx, sessionID)
	if err != nil {
		return Viewer{}, err
	}
	return Viewer{Kind: ViewerMember, Member: sess.Member}, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
