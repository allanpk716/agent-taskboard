-- #75：管理员显式指定初筛人。没有有效显式初筛人时，所有管理员兜底。
ALTER TABLE members ADD COLUMN triager INTEGER NOT NULL DEFAULT 0;
