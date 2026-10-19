-- 访客链接认领（#162）：存量链接一律未认领；第一位访客明确确认后独占。
ALTER TABLE guest_links ADD COLUMN claimed_at TEXT;
ALTER TABLE guest_links ADD COLUMN claim_name TEXT;

-- 已认领设备的同源凭证；只存哈希，随链接撤销或到期失效。
CREATE TABLE guest_devices (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    guest_link_id INTEGER NOT NULL REFERENCES guest_links(id),
    token_hash  TEXT NOT NULL UNIQUE,
    created_at  TEXT NOT NULL
);

CREATE INDEX idx_guest_devices_link ON guest_devices(guest_link_id);
