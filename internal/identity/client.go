package identity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
)

const clientTokenPrefix = "tbc_"
const clientCodeTTL = 10 * time.Minute

var ErrClientCredentialNotFound = errors.New("没有这个客户端凭证")
var ErrInvalidClientCode = errors.New("客户端连接 code 无效、已过期或 verifier 不匹配")
var ErrInvalidClientPort = errors.New("客户端回调端口必须是 1-65535")
var ErrInvalidClientConnect = errors.New("客户端连接参数不合法")

// Client 是解析凭证时携带的桌面客户端信息。
type Client struct {
	ID       int64
	Hostname string
}

type clientConnectCode struct {
	memberID      int64
	hostname      string
	port          int
	state         string
	codeChallenge string
	expiresAt     time.Time
}

// ClientCredential 是签发结果；明文 token 只出现一次。
type ClientCredential struct {
	Client
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"created_at"`
}

// ClientCredentialInfo 是「我的」页面展示的一台已连接电脑。
type ClientCredentialInfo struct {
	Client
	CreatedAt     time.Time
	LastUsedAt    time.Time
	ClientVersion string
}

// ClientConnectRequest 校验并保存浏览器确认后的短时 code 参数。
func (m *Manager) CreateClientConnect(ctx context.Context, member Member, port int, state, challenge, hostname string) (string, error) {
	if port < 1 || port > 65535 {
		return "", ErrInvalidClientPort
	}
	state = strings.TrimSpace(state)
	hostname = strings.TrimSpace(hostname)
	if state == "" || len(state) > 2048 || hostname == "" || len(hostname) > 255 {
		return "", ErrInvalidClientConnect
	}
	if !validPKCEChallenge(challenge) {
		return "", ErrInvalidClientConnect
	}
	buf := make([]byte, 32)
	if _, err := randRead(buf); err != nil {
		return "", err
	}
	code := base64URLEncode(buf)
	now := m.clock.Now()
	m.clientMu.Lock()
	defer m.clientMu.Unlock()
	if m.clientCodes == nil {
		m.clientCodes = make(map[string]clientConnectCode)
	}
	for old, meta := range m.clientCodes {
		if !now.Before(meta.expiresAt) {
			delete(m.clientCodes, old)
		}
	}
	m.clientCodes[code] = clientConnectCode{
		memberID: member.ID, hostname: hostname, port: port,
		state: state, codeChallenge: challenge, expiresAt: now.Add(clientCodeTTL),
	}
	return code, nil
}

// ExchangeClientCode 消费一次性 code，校验 S256 PKCE 后签发客户端凭证。
func (m *Manager) ExchangeClientCode(ctx context.Context, code, verifier string) (ClientCredential, error) {
	if !validPKCEVerifier(verifier) {
		return ClientCredential{}, ErrInvalidClientCode
	}
	m.clientMu.Lock()
	meta, exists := m.clientCodes[code]
	if exists {
		delete(m.clientCodes, code)
	}
	m.clientMu.Unlock()
	if !exists || !m.clock.Now().Before(meta.expiresAt) {
		return ClientCredential{}, ErrInvalidClientCode
	}
	sum := sha256.Sum256([]byte(verifier))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != meta.codeChallenge {
		return ClientCredential{}, ErrInvalidClientCode
	}

	buf := make([]byte, 32)
	if _, err := randRead(buf); err != nil {
		return ClientCredential{}, err
	}
	token := clientTokenPrefix + base64URLEncode(buf)
	now := m.clock.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return ClientCredential{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE client_credentials SET revoked_at = ?
		WHERE member_id = ? AND hostname = ? AND revoked_at IS NULL`,
		now.Format(time.RFC3339Nano), meta.memberID, meta.hostname); err != nil {
		return ClientCredential{}, err
	}
	cred := ClientCredential{Client: Client{Hostname: meta.hostname}, Token: token, CreatedAt: now}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO client_credentials (member_id, hostname, token_hash, created_at)
		VALUES (?, ?, ?, ?) RETURNING id`,
		meta.memberID, meta.hostname, hashToken(token), now.Format(time.RFC3339Nano)).Scan(&cred.ID); err != nil {
		return ClientCredential{}, err
	}
	if err := tx.Commit(); err != nil {
		return ClientCredential{}, err
	}
	return cred, nil
}

var clientVersionRE = regexp.MustCompile(`^taskboard-client/([0-9]+\.[0-9]+\.[0-9]+)`)

// ViewerFromClientToken 解析客户端凭证，并按请求的 User-Agent 记录使用痕迹。
func (m *Manager) ViewerFromClientToken(ctx context.Context, token, userAgent string) (Viewer, error) {
	var (
		client      Client
		member      Member
		isAdmin     int
		revokedAt   sql.NullString
		deactivated sql.NullString
	)
	err := m.db.QueryRowContext(ctx, `
		SELECT c.id, c.hostname, c.revoked_at, m.id, m.username, m.name, m.is_admin, m.deactivated_at
		FROM client_credentials c JOIN members m ON m.id = c.member_id
		WHERE c.token_hash = ?`, hashToken(token)).
		Scan(&client.ID, &client.Hostname, &revokedAt, &member.ID, &member.Username,
			&member.Name, &isAdmin, &deactivated)
	if errors.Is(err, sql.ErrNoRows) || revokedAt.Valid || deactivated.Valid {
		return Viewer{}, ErrInvalidCredentials
	}
	if err != nil {
		return Viewer{}, err
	}
	member.IsAdmin = isAdmin == 1
	now := m.clock.Now().Format(time.RFC3339Nano)
	version := clientVersionRE.FindStringSubmatch(userAgent)
	if version != nil {
		_, err = m.db.ExecContext(ctx, `
			UPDATE client_credentials SET last_used_at = ?, client_version = ? WHERE id = ?`,
			now, version[1], client.ID)
	} else {
		_, err = m.db.ExecContext(ctx, `
			UPDATE client_credentials SET last_used_at = ? WHERE id = ?`, now, client.ID)
	}
	if err != nil {
		return Viewer{}, err
	}
	return Viewer{Kind: ViewerMember, Member: member, Client: &client}, nil
}

// ListClientCredentials 列出本人仍连接中的电脑，不包含已撤销的历史凭证。
func (m *Manager) ListClientCredentials(ctx context.Context, member Member) ([]ClientCredentialInfo, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, hostname, created_at, last_used_at, client_version FROM client_credentials
		WHERE member_id = ? AND revoked_at IS NULL ORDER BY id DESC`, member.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClientCredentialInfo{}
	for rows.Next() {
		var (
			info      ClientCredentialInfo
			createdAt string
			lastUsed  sql.NullString
			version   sql.NullString
		)
		if err := rows.Scan(&info.ID, &info.Hostname, &createdAt, &lastUsed, &version); err != nil {
			return nil, err
		}
		if info.CreatedAt, err = parseDBTime(createdAt); err != nil {
			return nil, err
		}
		if lastUsed.Valid {
			if info.LastUsedAt, err = parseDBTime(lastUsed.String); err != nil {
				return nil, err
			}
		}
		if version.Valid {
			info.ClientVersion = version.String
		}
		out = append(out, info)
	}
	return out, rows.Err()
}

// RevokeClientCredential 由网页会话撤销本人某台电脑。
func (m *Manager) RevokeClientCredential(ctx context.Context, member Member, id int64) error {
	res, err := m.db.ExecContext(ctx, `
		UPDATE client_credentials SET revoked_at = ?
		WHERE id = ? AND member_id = ? AND revoked_at IS NULL`,
		m.clock.Now().Format(time.RFC3339Nano), id, member.ID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var exists int
		if err := m.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM client_credentials WHERE id = ? AND member_id = ?`, id, member.ID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrClientCredentialNotFound
		}
	}
	return nil
}

// RevokeCurrentClientCredential 供客户端退出登录时撤销自己。
func (m *Manager) RevokeCurrentClientCredential(ctx context.Context, client Client) error {
	res, err := m.db.ExecContext(ctx, `
		UPDATE client_credentials SET revoked_at = ?
		WHERE id = ? AND revoked_at IS NULL`,
		m.clock.Now().Format(time.RFC3339Nano), client.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrClientCredentialNotFound
	}
	return nil
}

func validPKCEVerifier(v string) bool {
	if len(v) < 43 || len(v) > 128 {
		return false
	}
	return validPKCEChallenge(v)
}

func validPKCEChallenge(v string) bool {
	if len(v) < 43 || len(v) > 128 {
		return false
	}
	_, err := base64.RawURLEncoding.WithPadding(base64.NoPadding).DecodeString(v)
	return err == nil
}

func parseDBTime(raw string) (time.Time, error) {
	return time.ParseInLocation(time.RFC3339Nano, raw, clock.Beijing)
}
