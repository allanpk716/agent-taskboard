-- 工单核心（#29）：标签字典、工单、工单标签与时间线事件。
-- 工单不可删除：工单编号与全局序号在同一个事务内分配
--（序号 = 累计工单数，AUTOINCREMENT 保证不回收）。
CREATE TABLE label_dimensions (
    name TEXT PRIMARY KEY
);

CREATE TABLE label_values (
    dimension TEXT NOT NULL REFERENCES label_dimensions(name),
    value     TEXT NOT NULL,
    PRIMARY KEY (dimension, value)
);

-- 不带维度的标签（#31 提供完整的增删与别名操作；#29 只做「必须在字典中」校验）。
CREATE TABLE plain_labels (
    label TEXT PRIMARY KEY
);

CREATE TABLE tickets (
    seq               INTEGER PRIMARY KEY AUTOINCREMENT, -- 全局序号，连续不跳号
    number            TEXT NOT NULL UNIQUE,              -- YYYYMMDD-NNN，北京时间每天从 001 起，超 999 自动 4 位
    kind              TEXT NOT NULL CHECK (kind IN ('task', 'report')),
    title             TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    close_reason      TEXT,                              -- done | wontfix | duplicate（#30 启用）
    brief_md          TEXT NOT NULL DEFAULT '',          -- 不用模板的整块说明；模板工单用 fields（#34）
    brief_revision    INTEGER NOT NULL DEFAULT 1,
    creator_member_id INTEGER REFERENCES members(id),    -- #35 起访客也能创建（为 null）
    creator_agent_id  INTEGER REFERENCES agent_tokens(id),
    assignee_id       INTEGER REFERENCES members(id),    -- #36 启用
    template_key      TEXT,                              -- #34 启用
    created_at        TEXT NOT NULL,
    last_change       INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE ticket_labels (
    ticket_seq INTEGER NOT NULL REFERENCES tickets(seq),
    label      TEXT NOT NULL,                             -- '维度:值' 或不带维度
    PRIMARY KEY (ticket_seq, label)
);

-- 时间线事件，只追加不修改。
CREATE TABLE timeline_events (
    ticket_seq      INTEGER NOT NULL REFERENCES tickets(seq),
    seq             INTEGER NOT NULL,                     -- 单内递增
    change_seq      INTEGER NOT NULL,                     -- 全局变更序号，与事件写入同事务推进
    type            TEXT NOT NULL,
    actor_member_id INTEGER REFERENCES members(id),
    actor_agent_id  INTEGER REFERENCES agent_tokens(id),
    at              TEXT NOT NULL,
    -- 附加字段（reply 的正文、assigned 的 to 等）由后续工单扩展。
    PRIMARY KEY (ticket_seq, seq)
);

CREATE INDEX idx_timeline_events_change ON timeline_events(change_seq);
