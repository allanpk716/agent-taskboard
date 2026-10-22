-- 回复草稿与起草过程（#211/#213）：草稿发出前不产生工单事件。
ALTER TABLE agent_tokens ADD COLUMN draft_mode TEXT NOT NULL DEFAULT 'direct';

CREATE TABLE drafts (
    id                INTEGER PRIMARY KEY,
    kind              TEXT NOT NULL CHECK (kind IN ('reply')),
    status            TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent')),
    ticket_seq        INTEGER NOT NULL REFERENCES tickets(seq),
    member_id         INTEGER NOT NULL REFERENCES members(id),
    request_json      TEXT NOT NULL,
    version           INTEGER NOT NULL,
    base_timeline_seq INTEGER NOT NULL,
    member_edited     INTEGER NOT NULL DEFAULT 0,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    sent_at           TEXT,
    sent_timeline_seq INTEGER
);

CREATE UNIQUE INDEX idx_drafts_pending_side
    ON drafts(ticket_seq, member_id) WHERE status = 'pending';

CREATE TABLE draft_versions (
    draft_id        INTEGER NOT NULL REFERENCES drafts(id) ON DELETE CASCADE,
    version         INTEGER NOT NULL,
    request_json    TEXT NOT NULL,
    author_member_id INTEGER NOT NULL REFERENCES members(id),
    author_agent_id  INTEGER REFERENCES agent_tokens(id),
    at              TEXT NOT NULL,
    PRIMARY KEY (draft_id, version)
);
