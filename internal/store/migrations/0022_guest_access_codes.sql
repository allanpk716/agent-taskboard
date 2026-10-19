-- 访客访问码（#163）：明文只在认领或重置响应里出现一次，库里只存哈希。
-- 连续输错计数由浏览器输入和访客 Agent（#164）共用。
ALTER TABLE guest_links ADD COLUMN access_code_hash TEXT;
ALTER TABLE guest_links ADD COLUMN access_code_failures INTEGER NOT NULL DEFAULT 0;
ALTER TABLE guest_links ADD COLUMN access_code_locked_at TEXT;

-- 每台设备记录进入方式（device=认领时这台设备；access_code=换设备凭码进入）。
ALTER TABLE guest_devices ADD COLUMN entry_method TEXT NOT NULL DEFAULT 'device';
