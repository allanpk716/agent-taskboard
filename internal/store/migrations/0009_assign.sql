-- #36：成员间交接与收件箱。tickets.assignee_id 已在 0003 预留，这里启用；
-- assigned 事件的附加列存改派前后的成员名（from 为 NULL 表示创建时指派）。
ALTER TABLE timeline_events ADD COLUMN assign_from TEXT;
ALTER TABLE timeline_events ADD COLUMN assign_to TEXT;
