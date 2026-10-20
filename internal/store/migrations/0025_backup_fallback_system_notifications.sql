-- #177：备份可记录本机兜底；成员通知支持不挂工单的系统通知。
ALTER TABLE backup_records ADD COLUMN location TEXT NOT NULL DEFAULT 'target'
    CHECK (location IN ('target', 'local'));

CREATE TABLE notifications_new (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    member_id          INTEGER NOT NULL REFERENCES members(id),
    ticket_seq         INTEGER REFERENCES tickets(seq),
    ticket_number      TEXT,
    ticket_title       TEXT,
    kind               TEXT NOT NULL CHECK (kind IN ('turn', 'fyi')),
    reason             TEXT NOT NULL,
    text               TEXT NOT NULL,
    trigger_seq        INTEGER,
    trigger_type       TEXT,
    actor_kind         TEXT,
    actor_name         TEXT,
    actor_member_id    INTEGER,
    actor_agent_id     INTEGER,
    summary            TEXT,
    url                TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL,
    read_at            TEXT,
    CHECK (
        (reason IN ('backup_failing', 'backup_recovered') AND ticket_seq IS NULL)
        OR
        (reason NOT IN ('backup_failing', 'backup_recovered')
          AND ticket_seq IS NOT NULL AND ticket_number IS NOT NULL AND ticket_title IS NOT NULL
          AND trigger_seq IS NOT NULL AND trigger_type IS NOT NULL
          AND actor_kind IS NOT NULL AND actor_name IS NOT NULL)
    )
);

INSERT INTO notifications_new (
    id, member_id, ticket_seq, ticket_number, ticket_title, kind, reason, text,
    trigger_seq, trigger_type, actor_kind, actor_name, actor_member_id, actor_agent_id,
    summary, url, created_at, read_at
)
SELECT
    id, member_id, ticket_seq, ticket_number, ticket_title, kind, reason, text,
    trigger_seq, trigger_type, actor_kind, actor_name, actor_member_id, actor_agent_id,
    summary, '/t/' || ticket_number, created_at, read_at
FROM notifications;

-- 通知 id 是对外的增量游标，必须单调不复用：沿用旧表的自增序列。
UPDATE sqlite_sequence
SET seq = MAX(seq, COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'notifications'), 0))
WHERE name = 'notifications_new';

DROP TABLE notifications;
ALTER TABLE notifications_new RENAME TO notifications;

CREATE INDEX idx_notifications_member_id_id ON notifications(member_id, id);
CREATE INDEX idx_notifications_member_unread ON notifications(member_id, read_at, id);
CREATE INDEX idx_notifications_reason_created ON notifications(reason, created_at);
