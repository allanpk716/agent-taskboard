-- #235：通用管理操作留痕。归档移除与导入（#236/#237）继续追加新的
-- operation_type，不在包名上做隐式约定。
CREATE TABLE admin_operations (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    operation_type    TEXT NOT NULL,
    operator_member_id INTEGER NOT NULL REFERENCES members(id),
    at                TEXT NOT NULL,
    ticket_numbers    TEXT NOT NULL, -- JSON 数组，保持操作时的选择顺序
    package_name      TEXT NOT NULL
);

CREATE INDEX idx_admin_operations_at_id ON admin_operations(at DESC, id DESC);
