-- 访客链接（#32）：密钥只存哈希（明文只在创建响应里出现一次）；撤销与过期
-- 即时生效——每个请求都实时查库判定，不用无状态凭证。
CREATE TABLE guest_links (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    ticket_seq     INTEGER NOT NULL REFERENCES tickets(seq),
    key_hash       TEXT NOT NULL UNIQUE,        -- SHA-256(密钥)，与 Agent token 同一方案
    note           TEXT NOT NULL,               -- 成员写的备注，如「检验科-张三」
    expires_at     TEXT NOT NULL,               -- 到期时刻（北京时间，RFC3339）；没有永久选项
    revoked_at     TEXT,                        -- 撤销时刻；NULL = 未撤销
    can_edit_brief INTEGER NOT NULL DEFAULT 0,  -- 「可填写说明」权限
    created_by_member_id INTEGER NOT NULL REFERENCES members(id),
    created_by_agent_id  INTEGER REFERENCES agent_tokens(id),
    created_at     TEXT NOT NULL
);

CREATE INDEX idx_guest_links_ticket ON guest_links(ticket_seq);

-- 时间线事件的访客作者附加字段（ADR 0003：访客的自报信息不经平台核实）：
-- actor_link_id 记下经哪条链接操作（溯源）；备注与自报信息存当时的原文。
ALTER TABLE timeline_events ADD COLUMN actor_link_id INTEGER REFERENCES guest_links(id);
ALTER TABLE timeline_events ADD COLUMN actor_link_note TEXT;    -- 链接备注（当时的原文）
ALTER TABLE timeline_events ADD COLUMN actor_author_name TEXT; -- 自报署名
ALTER TABLE timeline_events ADD COLUMN actor_via_agent INTEGER NOT NULL DEFAULT 0; -- 自报由 Agent 写成

-- guest_link_created / guest_link_revoked / guest_link_extended 事件的附加字段。
ALTER TABLE timeline_events ADD COLUMN note TEXT;            -- 链接备注（当时的原文）
ALTER TABLE timeline_events ADD COLUMN link_expires_at TEXT; -- guest_link_extended 的新到期时刻
