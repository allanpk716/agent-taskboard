-- #374：通知出口失败分级与生命周期联动。delivery_state 供「我的」页区分
-- 黄/红；transient_since 判断临时故障是否持续超过约 4 小时；catchup_after
-- 是显式恢复后首响的 created_at 下界（恢复时刻往前 24 小时）。自动暂停要给
-- 成员本人发站内提醒，outlet_paused 加入不挂工单系统通知白名单。
ALTER TABLE outlet ADD COLUMN delivery_state TEXT NOT NULL DEFAULT 'ok'
    CHECK (delivery_state IN ('ok', 'transient', 'ignored', 'permanent'));

ALTER TABLE outlet ADD COLUMN delivery_state_at TEXT;

ALTER TABLE outlet ADD COLUMN transient_since TEXT;

ALTER TABLE outlet ADD COLUMN catchup_after TEXT;

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
    headline           TEXT,
    url                TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL,
    read_at            TEXT,
    CHECK (
        (reason IN ('backup_failing', 'backup_recovered', 'intake_paused', 'outlet_paused')
          AND ticket_seq IS NULL)
        OR
        (reason = 'draft_pending' AND kind = 'draft'
          AND actor_kind IS NOT NULL AND actor_name IS NOT NULL)
        OR
        (reason NOT IN (
            'backup_failing', 'backup_recovered', 'intake_paused', 'outlet_paused', 'draft_pending'
        ) AND ticket_seq IS NOT NULL AND ticket_number IS NOT NULL AND ticket_title IS NOT NULL
          AND trigger_seq IS NOT NULL AND trigger_type IS NOT NULL
          AND actor_kind IS NOT NULL AND actor_name IS NOT NULL)
    )
);

INSERT INTO notifications_new (
    id, member_id, ticket_seq, ticket_number, ticket_title, kind, reason, text,
    trigger_seq, trigger_type, actor_kind, actor_name, actor_member_id, actor_agent_id,
    summary, headline, url, created_at, read_at)
SELECT
    id, member_id, ticket_seq, ticket_number, ticket_title, kind, reason, text,
    trigger_seq, trigger_type, actor_kind, actor_name, actor_member_id, actor_agent_id,
    summary, headline, url, created_at, read_at
FROM notifications;

DROP TABLE notifications;
ALTER TABLE notifications_new RENAME TO notifications;

CREATE INDEX idx_notifications_member_id_id ON notifications(member_id, id);
CREATE INDEX idx_notifications_member_unread ON notifications(member_id, read_at, id);
CREATE INDEX idx_notifications_reason_created ON notifications(reason, created_at);
