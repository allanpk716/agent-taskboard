-- #30：回复、说明修订、关闭与时间线。时间线事件补上各类型的附加字段
--（assigned/label 的附加字段随 #31/#36 加列；本表只追加，不改旧列）。
ALTER TABLE timeline_events ADD COLUMN body_md TEXT;         -- reply 的正文
ALTER TABLE timeline_events ADD COLUMN reply_no INTEGER;     -- 回复在单内的序号
ALTER TABLE timeline_events ADD COLUMN deliver INTEGER NOT NULL DEFAULT 0; -- 交付标记（#36 启用）
ALTER TABLE timeline_events ADD COLUMN revision INTEGER;     -- brief_revised 的版本号
ALTER TABLE timeline_events ADD COLUMN reason TEXT;          -- closed 的原因
ALTER TABLE timeline_events ADD COLUMN duplicate_of TEXT;   -- closed=重复时另一张工单的编号

ALTER TABLE tickets ADD COLUMN duplicate_of_seq INTEGER; -- closed=重复时另一张工单的序号（读取用）

-- 幂等表（spec「幂等」）：按（key、凭证、端点）隔离，快照存首次成功响应
--（含状态码），重放返回首次结果、不产生新内容。
CREATE TABLE idempotency_keys (
    key          TEXT NOT NULL,
    credential   TEXT NOT NULL, -- 凭证指纹（会话 id / Agent token / 访客密钥的哈希）
    endpoint     TEXT NOT NULL, -- 方法 + 路径
    status       INTEGER NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    body         BLOB NOT NULL,
    created_at   TEXT NOT NULL,
    PRIMARY KEY (key, credential, endpoint)
);
