-- #242：新访客链接的密钥用独立服务端密钥加密保存；哈希仍用于鉴权。
ALTER TABLE guest_links ADD COLUMN key_ciphertext TEXT;
