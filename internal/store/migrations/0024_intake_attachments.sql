-- #47：反馈入口可以在建单前匿名上传附件。ticket_seq 改为可空，
-- 未认领的入口附件用 uploader_intake_id + intake_upload_key 归属到入口和提交。
-- 迁移在一个事务里执行，事务内不能关外键。子表 access_records 引用
-- attachments(id)，DROP 父表会触发外键失败，所以先把引用暂存并置空，
-- 重建完再写回（id 原样保留）。
CREATE TEMP TABLE access_attachment_refs AS
    SELECT id, attachment_id FROM access_records WHERE attachment_id IS NOT NULL;
UPDATE access_records SET attachment_id = NULL WHERE attachment_id IS NOT NULL;

CREATE TABLE attachments_new (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    ticket_seq          INTEGER REFERENCES tickets(seq),
    filename            TEXT NOT NULL,
    size                INTEGER NOT NULL,
    state               TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'attached')),
    attached_to         TEXT,
    uploader_member_id  INTEGER REFERENCES members(id),
    uploader_agent_id   INTEGER REFERENCES agent_tokens(id),
    uploader_link_id    INTEGER REFERENCES guest_links(id),
    uploader_intake_id  INTEGER REFERENCES intakes(id),
    intake_upload_key   TEXT,
    uploaded_at         TEXT NOT NULL,
    claimed_at          TEXT,
    CHECK (ticket_seq IS NOT NULL OR uploader_intake_id IS NOT NULL)
);

INSERT INTO attachments_new (id, ticket_seq, filename, size, state, attached_to,
                             uploader_member_id, uploader_agent_id, uploader_link_id,
                             uploaded_at, claimed_at)
SELECT id, ticket_seq, filename, size, state, attached_to,
       uploader_member_id, uploader_agent_id, uploader_link_id, uploaded_at, claimed_at
FROM attachments;

DROP TABLE attachments;
ALTER TABLE attachments_new RENAME TO attachments;
CREATE INDEX idx_attachments_ticket ON attachments(ticket_seq, state);
CREATE INDEX idx_attachments_intake_pending ON attachments(uploader_intake_id, state);

UPDATE access_records SET attachment_id =
    (SELECT r.attachment_id FROM access_attachment_refs r WHERE r.id = access_records.id)
WHERE id IN (SELECT id FROM access_attachment_refs);
DROP TABLE access_attachment_refs;
