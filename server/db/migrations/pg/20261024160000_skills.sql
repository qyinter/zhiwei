-- 技能包（SKILL.md 格式）。
--
-- system prompt 只列启用技能的 name + description；模型判断用得上时调 read_skill 读 content 正文与附属文件。
-- content 存 SKILL.md 原文（含 frontmatter），name / description 是从中解析出的冗余列，便于列表与唯一约束。
CREATE TABLE skills (
    id           BIGSERIAL PRIMARY KEY,
    name         VARCHAR(64)  NOT NULL UNIQUE,
    description  TEXT         NOT NULL,
    content      TEXT         NOT NULL,
    enabled      BOOLEAN      NOT NULL DEFAULT true,
    use_count    INTEGER      NOT NULL DEFAULT 0,
    last_used_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- 附属参考文件（路径相对技能根目录，如 references/pricing.md）。只存文本，脚本不入库。
CREATE TABLE skill_files (
    skill_id BIGINT NOT NULL REFERENCES skills (id) ON DELETE CASCADE,
    path     TEXT   NOT NULL,
    content  TEXT   NOT NULL,
    PRIMARY KEY (skill_id, path)
);
