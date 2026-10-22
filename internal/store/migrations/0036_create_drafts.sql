-- 建单草稿（#211/#216）：草稿可以不挂工单，通知也允许不挂工单。
CREATE TEMP TABLE draft_attachment_refs AS
    SELECT id, draft_id FROM attachments WHERE draft_id IS NOT NULL;
UPDATE attachments SET draft_id = NULL WHERE draft_id IS NOT NULL;

CREATE TABLE drafts_new (
    id                INTEGER PRIMARY KEY,
    kind              TEXT NOT NULL CHECK (kind IN ('create', 'reply', 'brief_revision')),
    status            TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent')),
    ticket_seq        INTEGER REFERENCES tickets(seq),
    member_id         INTEGER NOT NULL REFERENCES members(id),
    request_json      TEXT NOT NULL,
    version           INTEGER NOT NULL,
    base_timeline_seq INTEGER NOT NULL DEFAULT 0,
    member_edited     INTEGER NOT NULL DEFAULT 0,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    sent_at           TEXT,
    sent_timeline_seq INTEGER
);

INSERT INTO drafts_new (id, kind, status, ticket_seq, member_id, request_json, version,
                        base_timeline_seq, member_edited, created_at, updated_at, sent_at, sent_timeline_seq)
SELECT id, kind, status, ticket_seq, member_id, request_json, version,
       base_timeline_seq, member_edited, created_at, updated_at, sent_at, sent_timeline_seq
FROM drafts;

CREATE TABLE draft_versions_new (
    draft_id         INTEGER NOT NULL REFERENCES drafts_new(id) ON DELETE CASCADE,
    version          INTEGER NOT NULL,
    request_json     TEXT NOT NULL,
    author_member_id INTEGER NOT NULL REFERENCES members(id),
    author_agent_id  INTEGER REFERENCES agent_tokens(id),
    at               TEXT NOT NULL,
    PRIMARY KEY (draft_id, version)
);

INSERT INTO draft_versions_new (draft_id, version, request_json, author_member_id, author_agent_id, at)
SELECT draft_id, version, request_json, author_member_id, author_agent_id, at
FROM draft_versions;

DROP TABLE draft_versions;
DROP TABLE drafts;
ALTER TABLE drafts_new RENAME TO drafts;
ALTER TABLE draft_versions_new RENAME TO draft_versions;

CREATE UNIQUE INDEX idx_drafts_pending_side
    ON drafts(ticket_seq, member_id) WHERE status = 'pending';
UPDATE attachments SET draft_id =
    (SELECT r.draft_id FROM draft_attachment_refs r WHERE r.id = attachments.id)
WHERE id IN (SELECT id FROM draft_attachment_refs);
DROP TABLE draft_attachment_refs;

CREATE TABLE notifications_new (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    member_id          INTEGER NOT NULL REFERENCES members(id),
    ticket_seq         INTEGER REFERENCES tickets(seq),
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
        (reason IN ('backup_failing', 'backup_recovered') AND ticket_seq IS NULL)
        OR
        (reason = 'draft_pending' AND kind = 'draft'
          AND actor_kind IS NOT NULL AND actor_name IS NOT NULL)
        OR
        (reason NOT IN ('backup_failing', 'backup_recovered', 'draft_pending')
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
