-- 反馈入口按北京时间计数的每日提交用量。计数在提交事务内预留，达到上限即暂停。
CREATE TABLE intake_daily_usage (
    intake_id       INTEGER NOT NULL REFERENCES intakes(id),
    day             TEXT NOT NULL,
    submissions     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (intake_id, day)
);
