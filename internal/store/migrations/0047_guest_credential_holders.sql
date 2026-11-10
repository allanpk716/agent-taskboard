-- 访客凭证（#335）：访问码与设备不再属于单条访客链接。凭证持有者可以先由
-- 上线前的无主链接充当，后续也可以由长期访客充当；owner_type/owner_id
-- 保持这个扩展点。本迁移只搬家，不改变任何对外行为。
CREATE TABLE guest_credential_holders (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    owner_type              TEXT NOT NULL CHECK (owner_type IN ('guest_link', 'guest')),
    owner_id                INTEGER NOT NULL,
    claimed_at              TEXT,
    claim_name              TEXT,
    access_code_hash        TEXT,
    access_code_failures    INTEGER NOT NULL DEFAULT 0,
    access_code_locked_at   TEXT,
    created_at              TEXT NOT NULL,
    UNIQUE(owner_type, owner_id)
);

CREATE INDEX idx_guest_credential_holders_owner
    ON guest_credential_holders(owner_type, owner_id);

INSERT INTO guest_credential_holders
    (owner_type, owner_id, claimed_at, claim_name,
     access_code_hash, access_code_failures, access_code_locked_at, created_at)
SELECT 'guest_link', id, claimed_at, claim_name,
       access_code_hash, access_code_failures, access_code_locked_at, created_at
FROM guest_links;

ALTER TABLE guest_links ADD COLUMN credential_holder_id INTEGER REFERENCES guest_credential_holders(id);

UPDATE guest_links
SET credential_holder_id = (
    SELECT h.id FROM guest_credential_holders h
    WHERE h.owner_type = 'guest_link' AND h.owner_id = guest_links.id
);

CREATE UNIQUE INDEX idx_guest_links_credential_holder ON guest_links(credential_holder_id);

CREATE TABLE guest_credential_devices (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    credential_holder_id  INTEGER NOT NULL REFERENCES guest_credential_holders(id),
    token_hash            TEXT NOT NULL UNIQUE,
    entry_method          TEXT NOT NULL DEFAULT 'device',
    created_at            TEXT NOT NULL
);

CREATE INDEX idx_guest_credential_devices_holder
    ON guest_credential_devices(credential_holder_id);

INSERT INTO guest_credential_devices (credential_holder_id, token_hash, entry_method, created_at)
SELECT l.credential_holder_id, d.token_hash, d.entry_method, d.created_at
FROM guest_devices d
JOIN guest_links l ON l.id = d.guest_link_id;

DROP TABLE guest_devices;
ALTER TABLE guest_links DROP COLUMN claimed_at;
ALTER TABLE guest_links DROP COLUMN claim_name;
ALTER TABLE guest_links DROP COLUMN access_code_hash;
ALTER TABLE guest_links DROP COLUMN access_code_failures;
ALTER TABLE guest_links DROP COLUMN access_code_locked_at;
