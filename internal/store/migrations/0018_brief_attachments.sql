-- #130：说明每个修订版本的整单附件列表。旧工单为 NULL，读取时按空列表解释。
ALTER TABLE tickets ADD COLUMN brief_attachment_ids TEXT;
