-- 模板（#34）：版本化的模板与模板工单的字段快照。
-- 模板只能停用（templates.active），不能删除；每次修改产生一个新版本
--（template_versions 追加），记录修改人。用模板建的工单保存创建时那个版本的
-- 字段定义（tickets.template_snapshot，含引导文字与示例）。
CREATE TABLE templates (
    key             TEXT PRIMARY KEY,
    active          INTEGER NOT NULL DEFAULT 1,
    current_version INTEGER NOT NULL
);

CREATE TABLE template_versions (
    template_key          TEXT NOT NULL REFERENCES templates(key),
    version               INTEGER NOT NULL,
    name                  TEXT NOT NULL,
    kind                  TEXT NOT NULL CHECK (kind IN ('task', 'report')),
    preset_labels         TEXT NOT NULL DEFAULT '[]', -- JSON 数组
    agent_note            TEXT NOT NULL DEFAULT '',
    fields                TEXT NOT NULL,               -- 解析后的字段定义（JSON）
    markdown              TEXT NOT NULL,               -- 模板原文
    updated_by_member_id  INTEGER REFERENCES members(id),
    updated_by_agent_id   INTEGER REFERENCES agent_tokens(id),
    updated_at            TEXT NOT NULL,
    PRIMARY KEY (template_key, version)
);

ALTER TABLE tickets ADD COLUMN template_version INTEGER;
ALTER TABLE tickets ADD COLUMN template_snapshot TEXT; -- 创建时版本的模板定义（JSON，含 key/version）
ALTER TABLE tickets ADD COLUMN fields_json TEXT;       -- 模板工单的字段值 map

-- Bug 反馈模板的预设标签（spec「初始数据」：预置三份模板，Bug 反馈预设 待初筛）。
INSERT OR IGNORE INTO plain_labels (label) VALUES ('待初筛');
