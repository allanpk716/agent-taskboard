-- #217：草稿功能上线，已有成员 Agent 一律改为必须走草稿。
-- member_notice_seen 记录一次性网页提示；本迁移不产生事件或通知。
CREATE TABLE member_notice_seen (
    member_id INTEGER NOT NULL REFERENCES members(id),
    notice    TEXT NOT NULL,
    seen_at   TEXT NOT NULL,
    PRIMARY KEY (member_id, notice)
);

UPDATE agent_tokens SET draft_mode = 'required';
