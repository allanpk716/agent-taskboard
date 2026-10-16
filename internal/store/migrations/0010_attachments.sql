-- 附件（#33）：两步上传。文件本体放 data_dir/attachments/<id>，库里只存元数据；
-- state: pending（待用，计入配额、24 小时未引用清除）→ attached（挂到说明或回复上）。
-- 上传者归属：成员（含其 Agent，记 member/agent）或访客链接（记 link），
-- 认领（claim）时校验待用状态与上传者归属。
CREATE TABLE attachments (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    ticket_seq          INTEGER NOT NULL REFERENCES tickets(seq),
    filename            TEXT NOT NULL,
    size                INTEGER NOT NULL,                -- 字节数
    state               TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'attached')),
    attached_to         TEXT,                            -- '说明' 或 '回复 N'；pending 为 NULL
    uploader_member_id  INTEGER REFERENCES members(id),  -- 成员上传（含其 Agent）
    uploader_agent_id   INTEGER REFERENCES agent_tokens(id),
    uploader_link_id    INTEGER REFERENCES guest_links(id), -- 访客链接上传
    uploaded_at         TEXT NOT NULL,
    claimed_at          TEXT
);

CREATE INDEX idx_attachments_ticket ON attachments(ticket_seq, state);

-- 回复事件携带引用的附件（attachment_ids 与正文 att: 引用合并去重后的结果，
-- JSON 数组文本；#33）。
ALTER TABLE timeline_events ADD COLUMN attachment_ids TEXT;
