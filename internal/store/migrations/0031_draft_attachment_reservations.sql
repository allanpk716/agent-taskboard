-- 回复草稿引用的附件（#219）：pending 附件带上 draft_id 即被草稿占用，
-- 不参与 24 小时清理；草稿丢弃时由服务删行并删文件。
ALTER TABLE attachments ADD COLUMN draft_id INTEGER REFERENCES drafts(id);
CREATE INDEX idx_attachments_draft ON attachments(draft_id);
