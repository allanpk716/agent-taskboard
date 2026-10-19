-- 反馈入口即认领（#165）：提交者丢失访问码时，成功页提示联系本部门，
-- 联系方式由管理员按入口维护。
ALTER TABLE intakes ADD COLUMN department_contact TEXT NOT NULL DEFAULT '';
