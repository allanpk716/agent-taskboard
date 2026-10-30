-- #236：从服务器移除归档工单后留下墓碑。通知条目保留，但不再外键约束
-- 工单本体；访客链接的密钥哈希随墓碑保留，旧链接只能得到“已归档”。
CREATE TABLE archived_tickets (
    seq                INTEGER PRIMARY KEY,
    number             TEXT NOT NULL UNIQUE,
    title              TEXT NOT NULL,
    closed_at          TEXT NOT NULL,
    archived_at        TEXT NOT NULL,
    operator_member_id INTEGER NOT NULL REFERENCES members(id),
    package_name       TEXT NOT NULL,
    guest_key_hashes    TEXT NOT NULL DEFAULT '[]',
    guest_device_hashes TEXT NOT NULL DEFAULT '[]'
);

CREATE INDEX idx_archived_tickets_archived_at ON archived_tickets(archived_at DESC);

CREATE TABLE notifications_new (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    member_id          INTEGER NOT NULL REFERENCES members(id),
    ticket_seq         INTEGER,
    ticket_number      TEXT,
    ticket_title       TEXT,
    kind               TEXT NOT NULL CHECK (kind IN ('turn', 'fyi', 'draft')),
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
        (reason IN ('backup_failing', 'backup_recovered', 'intake_paused') AND ticket_seq IS NULL)
        OR
        (reason = 'draft_pending' AND kind = 'draft'
          AND actor_kind IS NOT NULL AND actor_name IS NOT NULL)
        OR
        (reason NOT IN ('backup_failing', 'backup_recovered', 'intake_paused', 'draft_pending')
          AND ticket_seq IS NOT NULL AND ticket_number IS NOT NULL AND ticket_title IS NOT NULL
          AND trigger_seq IS NOT NULL AND trigger_type IS NOT NULL
          AND actor_kind IS NOT NULL AND actor_name IS NOT NULL)
    )
);

INSERT INTO notifications_new (
    id, member_id, ticket_seq, ticket_number, ticket_title, kind, reason, text,
    trigger_seq, trigger_type, actor_kind, actor_name, actor_member_id, actor_agent_id,
    summary, url, created_at, read_at)
SELECT
    id, member_id, ticket_seq, ticket_number, ticket_title, kind, reason, text,
    trigger_seq, trigger_type, actor_kind, actor_name, actor_member_id, actor_agent_id,
    summary, url, created_at, read_at
FROM notifications;

DROP TABLE notifications;
ALTER TABLE notifications_new RENAME TO notifications;

CREATE INDEX idx_notifications_member_id_id ON notifications(member_id, id);
CREATE INDEX idx_notifications_member_unread ON notifications(member_id, read_at, id);
CREATE INDEX idx_notifications_reason_created ON notifications(reason, created_at);
