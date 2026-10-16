-- 初始结构：成员与网页会话。
CREATE TABLE members (
    id            INTEGER PRIMARY KEY,
    username      TEXT    NOT NULL UNIQUE,
    name          TEXT    NOT NULL,
    password_hash TEXT    NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT    NOT NULL
);

CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,
    member_id  INTEGER NOT NULL REFERENCES members(id),
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);

CREATE INDEX idx_sessions_member ON sessions(member_id);
