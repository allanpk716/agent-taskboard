-- 标签、字典与别名（#31）：别名表、停用标记与事件的 JSON 附加字段。
-- 标签值与不带维度的标签只能停用、不能删除（历史工单上可能还带着）。
CREATE TABLE label_aliases (
    dimension TEXT NOT NULL,
    alias     TEXT NOT NULL,
    value     TEXT NOT NULL,
    PRIMARY KEY (dimension, alias),
    -- 改名（UPDATE label_values）时别名跟着挪到新值上。
    FOREIGN KEY (dimension, value) REFERENCES label_values(dimension, value) ON UPDATE CASCADE
);

ALTER TABLE label_values ADD COLUMN deactivated INTEGER NOT NULL DEFAULT 0;
ALTER TABLE plain_labels ADD COLUMN deactivated INTEGER NOT NULL DEFAULT 0;

-- 时间线事件的附加字段（label_added/label_removed 的 label 等），存 JSON 文本；
-- 后续工单的事件类型（reply 的正文等）也复用这一列。
ALTER TABLE timeline_events ADD COLUMN payload TEXT;
