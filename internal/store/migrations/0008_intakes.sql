-- 反馈入口（#35）：部门级的长期地址，内网任何人（人或 Agent）都能通过它
-- 自助提交一张反馈，并拿到这张反馈的专属访客链接。slug 可以轮换（旧 slug
-- 立即失效），入口可以停用；绑定的模板必须是反馈类型，始终使用最新版本。
CREATE TABLE intakes (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    slug                 TEXT NOT NULL UNIQUE,
    name                 TEXT NOT NULL,
    template_key         TEXT NOT NULL REFERENCES templates(key),
    active               INTEGER NOT NULL DEFAULT 1,
    created_by_member_id INTEGER NOT NULL REFERENCES members(id),
    created_by_agent_id  INTEGER REFERENCES agent_tokens(id),
    created_at           TEXT NOT NULL,
    deactivated_at       TEXT  -- 停用时刻；NULL = 活跃
);

-- 通过入口提交的工单：创建者是访客（creator_member_id 为 NULL，0003 预留）。
-- creator_link_id 记录返回给提交者的那条「可填写说明」访客链接——提交者视同
-- 说明的创建者；creator_intake_id 记录从哪个入口提交；creator_author_name 是
-- 提交者自报的署名（ADR 0003：自报信息不经平台核实，存当时的原文）。
ALTER TABLE tickets ADD COLUMN creator_link_id INTEGER REFERENCES guest_links(id);
ALTER TABLE tickets ADD COLUMN creator_intake_id INTEGER REFERENCES intakes(id);
ALTER TABLE tickets ADD COLUMN creator_author_name TEXT;
