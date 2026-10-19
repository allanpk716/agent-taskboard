-- 访问记录（#157 第一批）：按工单存放、只对外暴露读取。
-- view 是同访问者一小时窗口内的聚合行；download 与 denied 每次一行。
-- 署名与链接备注写入当时的快照，访问记录不随后续改名或改备注变化。
CREATE TABLE access_records (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    ticket_seq     INTEGER NOT NULL REFERENCES tickets(seq),
    actor_kind     TEXT NOT NULL CHECK (actor_kind IN ('member', 'agent', 'admin', 'guest')),
    actor_name     TEXT NOT NULL,
    actor_member_id INTEGER REFERENCES members(id),
    actor_agent_id INTEGER REFERENCES agent_tokens(id),
    actor_link_id INTEGER REFERENCES guest_links(id),
    actor_link_note TEXT,
    entry_method  TEXT,
    action        TEXT NOT NULL CHECK (action IN ('view', 'download', 'denied')),
    attachment_id INTEGER REFERENCES attachments(id),
    attachment_ref TEXT,
    attachment_filename TEXT,
    first_at       TEXT NOT NULL,
    last_at        TEXT NOT NULL,
    count          INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX idx_access_records_ticket_time ON access_records(ticket_seq, first_at, id);
