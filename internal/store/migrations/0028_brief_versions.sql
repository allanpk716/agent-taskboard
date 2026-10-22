-- 说明版本（#211）：每张工单当前版在升级时回填一行；此后建单和修订在
-- 业务事务里追加完整快照。更早版本没有行，即「上线前未存档」。
CREATE TABLE brief_versions (
    ticket_seq          INTEGER NOT NULL REFERENCES tickets(seq),
    revision            INTEGER NOT NULL,
    title               TEXT NOT NULL,
    brief_md            TEXT NOT NULL,
    fields_json         TEXT,
    attachment_ids      TEXT,
    actor_member_id     INTEGER REFERENCES members(id),
    actor_agent_id      INTEGER REFERENCES agent_tokens(id),
    actor_link_id       INTEGER REFERENCES guest_links(id),
    actor_link_note     TEXT,
    actor_author_name   TEXT,
    actor_via_agent     INTEGER NOT NULL DEFAULT 0,
    at                  TEXT NOT NULL,
    PRIMARY KEY (ticket_seq, revision)
);

INSERT INTO brief_versions (
    ticket_seq, revision, title, brief_md, fields_json, attachment_ids,
    actor_member_id, actor_agent_id, actor_link_id, actor_link_note,
    actor_author_name, actor_via_agent, at
)
SELECT t.seq, t.brief_revision, t.title, t.brief_md, t.fields_json, t.brief_attachment_ids,
       COALESCE(r.actor_member_id, c.actor_member_id),
       COALESCE(r.actor_agent_id, c.actor_agent_id),
       COALESCE(r.actor_link_id, c.actor_link_id),
       COALESCE(r.actor_link_note, c.actor_link_note),
       COALESCE(r.actor_author_name, c.actor_author_name),
       COALESCE(r.actor_via_agent, c.actor_via_agent, 0),
       COALESCE(r.at, c.at, t.created_at)
FROM tickets t
LEFT JOIN timeline_events r
  ON r.ticket_seq = t.seq AND r.type = 'brief_revised' AND r.revision = t.brief_revision
LEFT JOIN timeline_events c
  ON c.ticket_seq = t.seq AND c.type = 'created'
WHERE NOT EXISTS (
    SELECT 1 FROM brief_versions v WHERE v.ticket_seq = t.seq AND v.revision = t.brief_revision
);
