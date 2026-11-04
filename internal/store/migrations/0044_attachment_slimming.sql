-- #281/#283：附件瘦身。附件记录保留，打上移出时间和来源瘦身包；
-- 后续读取、恢复、归档引用都通过这两列识别已瘦身附件。
ALTER TABLE attachments ADD COLUMN slim_removed_at TEXT;
ALTER TABLE attachments ADD COLUMN slim_package_name TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_attachments_slim_package ON attachments(slim_package_name)
    WHERE slim_package_name != '';
