-- #371：通知出口。key_cipher 由服务端 AES-GCM 加密；last_pushed_id 是
-- 该成员通知流的持久游标，重建配置时在同一事务里写入当前 MAX(id)。
CREATE TABLE outlet (
    member_id      INTEGER PRIMARY KEY REFERENCES members(id),
    bot_id         TEXT NOT NULL,
    key_cipher     TEXT NOT NULL,
    last_pushed_id INTEGER NOT NULL DEFAULT 0 CHECK (last_pushed_id >= 0),
    generation     INTEGER NOT NULL DEFAULT 1 CHECK (generation > 0),
    status         TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused')),
    failure_count  INTEGER NOT NULL DEFAULT 0 CHECK (failure_count >= 0),
    pause_reason   TEXT,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);
