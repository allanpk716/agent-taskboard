-- #42：成员账号管理（网页上创建与停用）。停用不是删除：账号、历史署名
-- 与事件都保留，只是不能再登录（其名下的 Agent token 也一并失效）。
ALTER TABLE members ADD COLUMN deactivated_at TEXT;
