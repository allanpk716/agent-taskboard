-- 访客主页（#329）：主页密钥与单条链接密钥使用不同前缀，鉴权只存哈希；
-- 密文用于成员随时重新复制主页文案。上线前已有的访客先保持空值，
-- 第一次复制主页文案时由服务端生成。
ALTER TABLE guests ADD COLUMN home_key_hash TEXT;

ALTER TABLE guests ADD COLUMN home_key_ciphertext TEXT;

CREATE UNIQUE INDEX idx_guests_home_key_hash ON guests(home_key_hash) WHERE home_key_hash IS NOT NULL;
