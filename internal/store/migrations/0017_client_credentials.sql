-- 桌面客户端凭证（ADR 0004）：只保存哈希，撤销与账号事件联动。
CREATE TABLE client_credentials (
    id              INTEGER PRIMARY KEY,
    member_id       INTEGER NOT NULL REFERENCES members(id),
    hostname        TEXT    NOT NULL,
    token_hash      TEXT    NOT NULL UNIQUE,
    created_at      TEXT    NOT NULL,
    last_used_at    TEXT,
    client_version  TEXT,
    revoked_at      TEXT
);

CREATE INDEX idx_client_credentials_member ON client_credentials(member_id);

-- 同一台电脑只保留一把有效凭证；重新连接时旧凭证会被撤销。
CREATE UNIQUE INDEX idx_client_credentials_active_hostname
    ON client_credentials(member_id, hostname) WHERE revoked_at IS NULL;
