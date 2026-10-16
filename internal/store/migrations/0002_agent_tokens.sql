-- 成员 Agent 的具名 token（ADR 0003）：只存哈希，撤销即时生效。
CREATE TABLE agent_tokens (
    id          INTEGER PRIMARY KEY,
    member_id   INTEGER NOT NULL REFERENCES members(id),
    name        TEXT    NOT NULL,
    token_hash  TEXT    NOT NULL UNIQUE,
    created_at  TEXT    NOT NULL,
    revoked_at  TEXT
);

CREATE INDEX idx_agent_tokens_member ON agent_tokens(member_id);

-- 未撤销的 Agent 名字在成员内唯一；撤销后名字可以复用（历史署名按 id 关联，不受影响）。
CREATE UNIQUE INDEX idx_agent_tokens_active_name
    ON agent_tokens(member_id, name) WHERE revoked_at IS NULL;
