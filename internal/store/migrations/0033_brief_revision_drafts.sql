-- 说明修订草稿（#211/#215）：与回复共用一方一个草稿位。直接改写表定义，
-- 避免 SQLite 重建父表时级联删除起草过程或影响附件的外键。
PRAGMA writable_schema = ON;
UPDATE sqlite_master
SET sql = replace(sql, 'kind IN (''reply'')', 'kind IN (''reply'', ''brief_revision'')')
WHERE type = 'table' AND name = 'drafts';
PRAGMA writable_schema = OFF;
PRAGMA schema_version = 100;
