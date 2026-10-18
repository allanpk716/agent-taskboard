-- #76：成员通知。id 是所有成员共用的全局递增游标。
CREATE TABLE notifications (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    member_id          INTEGER NOT NULL REFERENCES members(id),
    ticket_seq         INTEGER NOT NULL REFERENCES tickets(seq),
    ticket_number      TEXT NOT NULL,
    ticket_title       TEXT NOT NULL,
    kind               TEXT NOT NULL CHECK (kind IN ('turn', 'fyi')),
    reason             TEXT NOT NULL,
    text               TEXT NOT NULL,
    trigger_seq        INTEGER NOT NULL,
    trigger_type       TEXT NOT NULL,
    actor_kind         TEXT NOT NULL,
    actor_name         TEXT NOT NULL,
    actor_member_id    INTEGER,
    actor_agent_id     INTEGER,
    summary            TEXT,
    created_at         TEXT NOT NULL,
    read_at            TEXT
);

CREATE INDEX idx_notifications_member_id_id ON notifications(member_id, id);
CREATE INDEX idx_notifications_member_unread ON notifications(member_id, read_at, id);
