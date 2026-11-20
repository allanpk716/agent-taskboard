package outlet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
)

var (
	// ErrInvalidBotID 表示成员提交的 bot_id 不符合白名单字符规则。
	ErrInvalidBotID = errors.New("bot_id 只能包含小写字母和数字，长度 1-64")
	// ErrInvalidWebhookKey 表示成员没有提交 webhook key。
	ErrInvalidWebhookKey = errors.New("webhook key 不能为空")
	// ErrMemberNotFound 表示出口配置指向不存在的成员。
	ErrMemberNotFound = errors.New("成员不存在")
)

var botIDPattern = regexp.MustCompile(`^[a-z0-9]{1,64}$`)

// ResumeCatchupWindow 是显式恢复后首响的计数窗口：只数恢复时刻往前 24 小时
// 内的未读，避免长时间暂停后第一声报出巨大的历史计数。
const ResumeCatchupWindow = 24 * time.Hour

// Configuration 是保存后的非敏感出口信息，永不包含 webhook key。
type Configuration struct {
	MemberID     int64
	BotID        string
	KeyLast4     string
	LastPushedID int64
	Generation   int64
	Status       string
	FailureCount int64
	PauseReason  *string
	// DeliveryState 与两个时间字段只用于界面分级提示，不含错误详情或 key。
	DeliveryState   string
	DeliveryStateAt *string
	TransientSince  *string
}

// Manager 封装通知出口配置的加密存储与基线写入。
type Manager struct {
	db     *sql.DB
	clk    clock.Clock
	cipher *Cipher
}

// NewManager 构造出口配置管理器。
func NewManager(db *sql.DB, clk clock.Clock, cipher *Cipher) *Manager {
	return &Manager{db: db, clk: clk, cipher: cipher}
}

// Save 保存配置；便捷方法拥有事务，复杂界面事务可改用 SaveTx。
func (m *Manager) Save(ctx context.Context, memberID int64, botID, webhookKey string) (Configuration, error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Configuration{}, err
	}
	defer tx.Rollback()
	saved, err := m.SaveTx(ctx, tx, memberID, botID, webhookKey)
	if err != nil {
		return Configuration{}, err
	}
	if err := tx.Commit(); err != nil {
		return Configuration{}, err
	}
	return saved, nil
}

// SaveTx 在调用方事务里加密 key、写入成员通知 MAX(id) 基线并递增代数。
// 保存之后同一事务产生的通知大于基线，留给推送循环处理。
func (m *Manager) SaveTx(ctx context.Context, tx *sql.Tx, memberID int64, botID, webhookKey string) (Configuration, error) {
	if memberID <= 0 {
		return Configuration{}, ErrMemberNotFound
	}
	if !botIDPattern.MatchString(botID) {
		return Configuration{}, ErrInvalidBotID
	}
	if webhookKey == "" {
		return Configuration{}, ErrInvalidWebhookKey
	}
	if m.cipher == nil {
		return Configuration{}, ErrMasterKeyMissing
	}

	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE id=?)`, memberID).Scan(&exists); err != nil {
		return Configuration{}, err
	}
	if !exists {
		return Configuration{}, ErrMemberNotFound
	}
	keyCipher, err := m.cipher.Encrypt(webhookKey)
	if err != nil {
		return Configuration{}, err
	}
	var lastPushedID int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(id),0) FROM notifications WHERE member_id=?`, memberID).Scan(&lastPushedID); err != nil {
		return Configuration{}, err
	}
	var oldGeneration int64
	if err := tx.QueryRowContext(ctx,
		`SELECT generation FROM outlet WHERE member_id=?`, memberID).Scan(&oldGeneration); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Configuration{}, err
	}

	now := m.clk.Now().Format(time.RFC3339Nano)
	generation := oldGeneration + 1
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO outlet (
			member_id,bot_id,key_cipher,last_pushed_id,generation,status,failure_count,pause_reason,
			pending_latest_id,retry_state,attempt_count,next_attempt_at,last_pushed_at,
			delivery_state,delivery_state_at,transient_since,catchup_after,created_at,updated_at
		) VALUES (?,?,?,?,?,'active',0,NULL,NULL,'none',0,NULL,NULL,'ok',?,NULL,NULL,?,?)
		ON CONFLICT(member_id) DO UPDATE SET
			bot_id=excluded.bot_id,
			key_cipher=excluded.key_cipher,
			last_pushed_id=excluded.last_pushed_id,
			generation=excluded.generation,
			status='active',
			failure_count=0,
			pause_reason=NULL,
			pending_latest_id=NULL,
			retry_state='none',
			attempt_count=0,
			next_attempt_at=NULL,
			last_pushed_at=NULL,
			delivery_state='ok',
			delivery_state_at=excluded.delivery_state_at,
			transient_since=NULL,
			catchup_after=NULL,
			updated_at=excluded.updated_at`,
		memberID, botID, keyCipher, lastPushedID, generation, now, now, now); err != nil {
		return Configuration{}, fmt.Errorf("保存通知出口失败：%w", err)
	}
	return Configuration{
		MemberID: memberID, BotID: botID, KeyLast4: keyLast4(webhookKey),
		LastPushedID: lastPushedID,
		Generation:   generation, Status: "active", DeliveryState: "ok",
	}, nil
}
