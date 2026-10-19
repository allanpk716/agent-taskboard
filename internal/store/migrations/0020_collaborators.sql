-- #158：协作者关系与时间线事件对象。
CREATE TABLE ticket_collaborators (
    ticket_seq INTEGER NOT NULL REFERENCES tickets(seq),
    member_id  INTEGER NOT NULL REFERENCES members(id),
    added_at   TEXT NOT NULL,
    PRIMARY KEY (ticket_seq, member_id)
);

ALTER TABLE timeline_events ADD COLUMN collaborator_member_id INTEGER REFERENCES members(id);
ALTER TABLE timeline_events ADD COLUMN collaborator_name TEXT;
ALTER TABLE timeline_events ADD COLUMN actor_system INTEGER NOT NULL DEFAULT 0;

-- 第 2 批迁移：历史回复者补为协作者。创建者、当前指派人和历史被指派过的人
-- 不补；每补一位记一条「系统」操作的 collaborator_added 事件，不发通知。
INSERT INTO ticket_collaborators (ticket_seq, member_id, added_at)
SELECT DISTINCT e.ticket_seq, e.actor_member_id, datetime('now')
FROM timeline_events e
JOIN tickets t ON t.seq = e.ticket_seq
WHERE e.type = 'reply'
  AND e.actor_member_id IS NOT NULL
  AND e.actor_member_id != t.creator_member_id
  AND e.actor_member_id != t.assignee_id
  AND NOT EXISTS (
      SELECT 1
      FROM timeline_events a
      JOIN members h ON h.name = a.assign_to
      WHERE a.ticket_seq = e.ticket_seq
        AND a.type = 'assigned'
        AND h.id = e.actor_member_id
  );

WITH backfill AS (
  SELECT c.ticket_seq, c.member_id, m.name,
         ROW_NUMBER() OVER (
           PARTITION BY c.ticket_seq ORDER BY c.member_id
         ) AS ticket_row,
         ROW_NUMBER() OVER (ORDER BY c.ticket_seq, c.member_id) AS global_row
  FROM ticket_collaborators c
  JOIN members m ON m.id = c.member_id
)
INSERT INTO timeline_events (
  ticket_seq, seq, change_seq, type, actor_system, at,
  collaborator_member_id, collaborator_name
)
SELECT b.ticket_seq,
       (SELECT MAX(e.seq) FROM timeline_events e WHERE e.ticket_seq = b.ticket_seq) + b.ticket_row,
       (SELECT MAX(e.change_seq) FROM timeline_events e) + b.global_row,
       'collaborator_added', 1, datetime('now'),
       b.member_id, b.name
FROM backfill b;

UPDATE tickets
SET last_change = (
  SELECT MAX(e.change_seq) FROM timeline_events e WHERE e.ticket_seq = tickets.seq
)
WHERE EXISTS (SELECT 1 FROM timeline_events e WHERE e.ticket_seq = tickets.seq AND e.actor_system = 1);
