-- 备份（#43）：每次备份的结果写入数据库——时间、大小、是否通过完整性校验、
-- 附件新增文件数。kind：daily（每日）/ monthly（每月 1 日那份）/ preupdate
-- （更新前备份，#45 的更新流程复用同一段代码）。失败（目标连不上、导出或
-- 校验不通过）也记一行，error 写原因；成功行 error 为 NULL。
-- 校验不通过的副本改名为 .bad 留在目标里，不算成功、不参与轮换。
CREATE TABLE backup_records (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    at            TEXT NOT NULL,                  -- 备份时刻（北京时间，RFC3339Nano）
    kind          TEXT NOT NULL CHECK (kind IN ('daily','monthly','preupdate')),
    filename      TEXT NOT NULL DEFAULT '',       -- 副本文件名；没产出副本的失败行为空
    size          INTEGER NOT NULL DEFAULT 0,     -- 副本字节数
    integrity_ok  INTEGER NOT NULL DEFAULT 0,     -- PRAGMA integrity_check 是否通过
    files_added   INTEGER NOT NULL DEFAULT 0,     -- 本次附件镜像新增文件数
    error         TEXT                            -- 失败原因；成功为 NULL
);

CREATE INDEX idx_backup_records_at ON backup_records(at);
