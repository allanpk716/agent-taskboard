-- 模板适用场景（#193）：随模板版本保存；没写时为空。
ALTER TABLE template_versions ADD COLUMN scenario TEXT NOT NULL DEFAULT '';
