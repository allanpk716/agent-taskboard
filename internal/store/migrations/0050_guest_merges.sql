-- 合并访客（#334）：B 永久标记为已并入 A。链接在合并事务里改归 A；
-- B 的主页密钥与访客级凭证同时作废。历史署名快照不随合并改写。
ALTER TABLE guests ADD COLUMN merged_into_guest_id INTEGER REFERENCES guests(id);

ALTER TABLE guests ADD COLUMN merged_at TEXT;

CREATE INDEX idx_guests_merged_into ON guests(merged_into_guest_id);
