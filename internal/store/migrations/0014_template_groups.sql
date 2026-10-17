-- 模板分组（#63）：`# 组名` 开一组（副标题是组标题下第一段非空文字），
-- 分组随模板版本保存（template_versions.groups，JSON 数组 [{title, subtitle}]）；
-- 字段所属的组序号存在解析后的字段定义里（template_versions.fields 的
-- 每个字段带 group）。工单保存的创建时版本快照（tickets.template_snapshot
-- 的 JSON）自带 groups，无需改列。旧版本行的 groups 为空数组。
ALTER TABLE template_versions ADD COLUMN groups TEXT NOT NULL DEFAULT '[]';
