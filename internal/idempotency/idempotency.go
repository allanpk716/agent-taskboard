// Package idempotency 实现「幂等」（spec）：对会产生新内容的请求按
// Idempotency-Key 去重——（key、凭证、端点）相同的重放返回首次成功响应的快照，
// 不再产生新内容。反馈提交（#35）与附件上传（#33）也复用这张表。
package idempotency

import (
	"context"
	"database/sql"
	"time"
)

// Snapshot 是首次成功响应的快照（含状态码，如回复的 201）。
type Snapshot struct {
	Status      int
	ContentType string
	Body        []byte
}

// Manager 管理幂等记录。
type Manager struct {
	db *sql.DB
}

func NewManager(db *sql.DB) *Manager { return &Manager{db: db} }

// Lookup 查首次响应的快照；没有记录时 found 为 false。
func (m *Manager) Lookup(ctx context.Context, key, credential, endpoint string) (snap Snapshot, found bool, err error) {
	err = m.db.QueryRowContext(ctx, `
		SELECT status, content_type, body FROM idempotency_keys
		WHERE key = ? AND credential = ? AND endpoint = ?`,
		key, credential, endpoint).Scan(&snap.Status, &snap.ContentType, &snap.Body)
	if err == sql.ErrNoRows {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, err
	}
	return snap, true, nil
}

// Save 存首次成功响应的快照。同一（key、凭证、端点）已有记录时静默忽略
// （并发重放下两份响应等价，以先落库的为准）。
func (m *Manager) Save(ctx context.Context, key, credential, endpoint string, snap Snapshot) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO idempotency_keys (key, credential, endpoint, status, content_type, body, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		key, credential, endpoint, snap.Status, snap.ContentType, snap.Body,
		time.Now().Format(time.RFC3339Nano))
	return err
}
