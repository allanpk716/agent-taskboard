-- #373：门铃推送状态。pending_latest_id 固定失败重试的幂等键边界；
-- last_pushed_at 让 60 秒最小间隔跨进程重启仍然成立。
ALTER TABLE outlet ADD COLUMN pending_latest_id INTEGER;

ALTER TABLE outlet ADD COLUMN retry_state TEXT NOT NULL DEFAULT 'none'
    CHECK (retry_state IN ('none', 'retryable', 'permanent'));

ALTER TABLE outlet ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0);

ALTER TABLE outlet ADD COLUMN next_attempt_at TEXT;

ALTER TABLE outlet ADD COLUMN last_pushed_at TEXT;
