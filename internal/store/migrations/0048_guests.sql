-- 访客身份（#328）：访客成为部门级、长期存在的身份，访客链接必须归属一位访客。
-- 访问码、失败计数、锁定与设备继续挂在 #335 的凭证持有者上；访客的持有者
-- 用 owner_type = 'guest' 表示。上线前的老链接保持无主（guest_id 为 NULL），
-- 沿用单条链接自己的认领与访问码语义，不自动聚合。
CREATE TABLE guests (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    name                 TEXT NOT NULL,
    created_by_member_id INTEGER NOT NULL REFERENCES members(id),
    created_by_agent_id  INTEGER REFERENCES agent_tokens(id),
    created_at           TEXT NOT NULL,
    disabled_at          TEXT
);

CREATE INDEX idx_guests_name ON guests(name);

ALTER TABLE guest_links ADD COLUMN guest_id INTEGER REFERENCES guests(id);

CREATE INDEX idx_guest_links_guest ON guest_links(guest_id);

-- #335 用唯一索引保证“一条无主链接只有一个持有者”；访客的持有者会被
-- 名下多条链接共享，唯一性改由持有者表的 UNIQUE(owner_type, owner_id) 保证。
DROP INDEX idx_guest_links_credential_holder;
CREATE INDEX idx_guest_links_credential_holder ON guest_links(credential_holder_id);

-- 访客作者的名字快照（写入当时的原文）：回复与时间线保留链接备注快照的
-- 同时，再记下这位访客当时的名字；历史事件不随后续改名改写。
ALTER TABLE timeline_events ADD COLUMN actor_guest_name TEXT;

-- 说明版本作者同样是历史快照：访客修订说明时一并记下当时的访客名字。
ALTER TABLE brief_versions ADD COLUMN actor_guest_name TEXT;
