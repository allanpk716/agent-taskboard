// Package idempotency 实现「幂等」（spec）：对会产生新内容的请求按
// Idempotency-Key 去重——（key、凭证、端点）相同的重放返回首次成功响应的快照，
// 不再产生新内容。反馈提交（#35）与附件上传（#33）也复用这张表。
//
// 执行前先原子地预留一条 pending 记录（评审修正）：同键并发重放拿到
// 409 idempotency_in_progress，而不是各自执行一遍。记录保留 24 小时
// （created_at 来自注入时钟）：超龄的重放当作新请求，过期行由周期任务清除
// （快照里可能带着访客链接密钥，不能无限期留在库里）。
package idempotency

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
)

// Snapshot 是首次成功响应的快照（含状态码，如回复的 201）。
type Snapshot struct {
	Status      int
	ContentType string
	Body        []byte
}

// ErrReservationLost 表示预留时占位与查询反复交错（正常不会发生：清除任务
// 恰好删掉了同一条记录）。调用方把它当 500 处理即可。
var ErrReservationLost = errors.New("幂等记录预留未能完成")

// Retention 是幂等记录的保留时长：超过后重放当作新请求，记录被清除。
const Retention = 24 * time.Hour

// Manager 管理幂等记录。
type Manager struct {
	db  *sql.DB
	clk clock.Clock
}

func NewManager(db *sql.DB, c clock.Clock) *Manager { return &Manager{db: db, clk: c} }

// Lookup 查首次响应的快照；没有记录（或记录已超过保留时长）时 found 为 false。
func (m *Manager) Lookup(ctx context.Context, key, credential, endpoint string) (snap Snapshot, found bool, err error) {
	return m.lookupBefore(ctx, key, credential, endpoint, m.clk.Now().Add(-Retention))
}

func (m *Manager) lookupBefore(ctx context.Context, key, credential, endpoint string, cutoff time.Time) (Snapshot, bool, error) {
	var snap Snapshot
	err := m.db.QueryRowContext(ctx, `
		SELECT status, content_type, body FROM idempotency_keys
		WHERE key = ? AND credential = ? AND endpoint = ?
		  AND state = 'completed' AND created_at > ?`,
		key, credential, endpoint, cutoff.Format(time.RFC3339Nano)).Scan(&snap.Status, &snap.ContentType, &snap.Body)
	if err == sql.ErrNoRows {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, err
	}
	return snap, true, nil
}

// Reserve 原子地预留执行权：没有记录或记录已过期时插入一条 pending 记录并返回
// reserved=true；已有未过期的完成态记录时 found=true 并带回其快照（直接重放）；
// 已有未过期的 pending 记录（同键请求正在执行）时 inProgress=true。
func (m *Manager) Reserve(ctx context.Context, key, credential, endpoint string) (reserved bool, snap Snapshot, found bool, inProgress bool, err error) {
	now := m.clk.Now()
	cutoff := now.Add(-Retention)
	for attempt := 0; attempt < 3; attempt++ {
		res, execErr := m.db.ExecContext(ctx, `
			INSERT INTO idempotency_keys (key, credential, endpoint, state, status, content_type, body, created_at)
			VALUES (?, ?, ?, 'pending', 0, '', x'', ?)
			ON CONFLICT(key, credential, endpoint) DO UPDATE SET
			    state = 'pending', created_at = excluded.created_at
			WHERE idempotency_keys.created_at <= ?`,
			key, credential, endpoint, now.Format(time.RFC3339Nano), cutoff.Format(time.RFC3339Nano))
		if execErr != nil {
			return false, Snapshot{}, false, false, execErr
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return true, Snapshot{}, false, false, nil
		}
		// 占位失败：要么已有未过期的完成态记录（直接重放），要么 pending 正在执行。
		// 极小概率下记录刚被清除任务删掉——重试预留。
		snap, found, err = m.lookupBefore(ctx, key, credential, endpoint, cutoff)
		if err != nil {
			return false, Snapshot{}, false, false, err
		}
		if found {
			return false, snap, true, false, nil
		}
		var pending string
		err = m.db.QueryRowContext(ctx, `
			SELECT state FROM idempotency_keys
			WHERE key = ? AND credential = ? AND endpoint = ?`,
			key, credential, endpoint).Scan(&pending)
		if err == sql.ErrNoRows {
			continue // 被清除任务删掉了：再试一次预留
		}
		if err != nil {
			return false, Snapshot{}, false, false, err
		}
		if pending == "pending" {
			return false, Snapshot{}, false, true, nil
		}
		// 完成态但 lookupBefore 没查到（窗口边界）：当作在执行中，保守不重放。
		return false, Snapshot{}, false, true, nil
	}
	return false, Snapshot{}, false, false, ErrReservationLost
}

// Complete 把预留的 pending 记录补成完成态快照（2xx 才存）。
func (m *Manager) Complete(ctx context.Context, key, credential, endpoint string, snap Snapshot) error {
	_, err := m.db.ExecContext(ctx, `
		UPDATE idempotency_keys
		SET state = 'completed', status = ?, content_type = ?, body = ?
		WHERE key = ? AND credential = ? AND endpoint = ? AND state = 'pending'`,
		snap.Status, snap.ContentType, snap.Body, key, credential, endpoint)
	return err
}

// Release 放弃预留（执行没有成功，例如 4xx/5xx）：删掉 pending 记录，
// 下一次同键请求重新执行。
func (m *Manager) Release(ctx context.Context, key, credential, endpoint string) error {
	_, err := m.db.ExecContext(ctx, `
		DELETE FROM idempotency_keys
		WHERE key = ? AND credential = ? AND endpoint = ? AND state = 'pending'`,
		key, credential, endpoint)
	return err
}

// PurgeExpired 删除超过保留时长的幂等记录，返回删除的行数（周期任务调用；
// created_at 来自注入时钟，测试里推进假时钟后直接调）。
func (m *Manager) PurgeExpired(ctx context.Context) (int64, error) {
	cutoff := m.clk.Now().Add(-Retention)
	res, err := m.db.ExecContext(ctx,
		`DELETE FROM idempotency_keys WHERE created_at <= ?`, cutoff.Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
