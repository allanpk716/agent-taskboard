-- #309：通知新增结构化一句话，旧记录为 NULL，桌面客户端按旧弹窗格式回落。
ALTER TABLE notifications ADD COLUMN headline TEXT;
