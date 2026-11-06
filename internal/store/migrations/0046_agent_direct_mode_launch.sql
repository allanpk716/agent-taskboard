-- ADR 0017：成员 Agent 默认改为可直接发（本地预览、主人确认后提交）。
-- 已有 token（含已撤销）一律改为 direct；draft_mode_switched_to_direct 驱动
-- 一次性网页提示；旧上线标记清零，#217 的旧提示不再出现。已有 pending 草稿不动。
ALTER TABLE agent_tokens ADD COLUMN draft_mode_switched_to_direct INTEGER NOT NULL DEFAULT 0;

UPDATE agent_tokens
SET draft_mode = 'direct',
    draft_mode_switched_to_direct = 1,
    draft_mode_switched_by_launch = 0;
