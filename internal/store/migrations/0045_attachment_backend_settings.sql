-- #314/#318：记录附件实际所在的全局单后端（ADR 0018）。
-- 老库升级时全部视为 local；新库先由迁移落 local，首次按 OSS 配置启动时
-- 再在探测成功后改写为 OSS。
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

INSERT INTO settings (key, value, updated_at) VALUES (
    'attachments_backend', 'local', datetime('now')
);
