-- 幂等表的原子预留与 24 小时保留（评审修正）：执行前先插入 pending 占位行，
-- 同（key、凭证、端点）的并发重放拿到 409 idempotency_in_progress；记录的
-- created_at 来自注入时钟，重放超过 24 小时当作新请求，过期行由周期任务清除。
-- 旧记录只有完成态快照，默认 completed。
ALTER TABLE idempotency_keys ADD COLUMN state TEXT NOT NULL DEFAULT 'completed';

CREATE INDEX idx_idempotency_keys_created_at ON idempotency_keys(created_at);
