-- #135：成员按工单记录已读到的全局变更序号。上线前的历史工单全部视为
-- 已读：迁移时记一次基线，不回填所有成员 × 所有工单。
CREATE TABLE ticket_reads (
    member_id     INTEGER NOT NULL REFERENCES members(id),
    ticket_seq    INTEGER NOT NULL REFERENCES tickets(seq),
    last_read_seq INTEGER NOT NULL,
    PRIMARY KEY (member_id, ticket_seq)
);

CREATE TABLE ticket_read_baseline (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    last_read_seq INTEGER NOT NULL
);

INSERT INTO ticket_read_baseline (id, last_read_seq)
VALUES (1, COALESCE((SELECT MAX(change_seq) FROM timeline_events), 0));

CREATE INDEX idx_timeline_events_ticket_unread
ON timeline_events(ticket_seq, change_seq) WHERE type IN (
    'reply', 'brief_revised', 'assigned', 'closed', 'reopened',
    'collaborator_added', 'collaborator_removed', 'collaborator_left'
);
