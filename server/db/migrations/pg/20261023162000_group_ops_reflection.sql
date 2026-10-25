-- 群运营（主动回复 / 入群欢迎 / 入群审核）与反思沉淀。

-- 群消息时间线：群里收到的每条消息（含未 @ 机器人的），按群而非按用户组织。
-- 主动回复找「无人应答的提问」、反思沉淀找「群管理员的有效答复」都从这里读。
-- 只有渠道能收到全量群消息时才有未 @ 的消息（QQ 开通「接收所有消息」、TG 关闭 privacy mode）。
CREATE TABLE group_messages (
    id              BIGSERIAL PRIMARY KEY,
    channel         VARCHAR(32)  NOT NULL,
    chat_type       VARCHAR(16)  NOT NULL DEFAULT 'group',
    chat_id         VARCHAR(128) NOT NULL,
    user_id         VARCHAR(128) NOT NULL,
    user_name       VARCHAR(255) NOT NULL DEFAULT '',
    -- owner / admin 视为客服发言；member 或空为普通群友
    role            VARCHAR(16)  NOT NULL DEFAULT '',
    content         TEXT         NOT NULL,
    platform_msg_id TEXT         NOT NULL DEFAULT '',
    mentioned       BOOLEAN      NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_group_messages_chat ON group_messages (channel, chat_id, created_at);
CREATE INDEX idx_group_messages_created ON group_messages (created_at);
CREATE UNIQUE INDEX uq_group_messages_platform ON group_messages (channel, chat_id, platform_msg_id)
    WHERE platform_msg_id <> '';

-- 后台扫描器的游标（主动回复 / 反思按群推进；整理 / 升格记上次执行时间）。
CREATE TABLE poller_cursors (
    key        VARCHAR(255) PRIMARY KEY,
    at         TIMESTAMPTZ  NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- 主动回复记录：管理员可标记恰当 / 不当，用于回看补位质量。
CREATE TABLE proactive_replies (
    id         BIGSERIAL PRIMARY KEY,
    channel    VARCHAR(32)  NOT NULL,
    chat_id    VARCHAR(128) NOT NULL,
    user_id    VARCHAR(128) NOT NULL,
    user_name  VARCHAR(255) NOT NULL DEFAULT '',
    question   TEXT         NOT NULL,
    answer     TEXT         NOT NULL,
    -- ok / bad / ''（未标记）
    quality    VARCHAR(8)   NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_proactive_replies_created ON proactive_replies (created_at DESC);

-- 入群申请处理记录。
CREATE TABLE join_requests (
    id           BIGSERIAL PRIMARY KEY,
    channel      VARCHAR(32)  NOT NULL,
    chat_id      VARCHAR(128) NOT NULL,
    user_id      VARCHAR(128) NOT NULL,
    user_name    VARCHAR(255) NOT NULL DEFAULT '',
    -- 申请人填写的验证消息 / 问答答案（原文）
    answer       TEXT         NOT NULL DEFAULT '',
    -- 从答案里提取出的待核对 ID
    extracted_id VARCHAR(255) NOT NULL DEFAULT '',
    -- approved 已同意 / declined 已拒绝 / pending 留给管理员 / auto 平台策略已自动通过 / failed 审批接口出错
    decision     VARCHAR(16)  NOT NULL,
    detail       TEXT         NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_join_requests_created ON join_requests (created_at DESC);

-- 反思沉淀：从群管理员的答复里提炼的问答知识。approved 条目进入检索索引；
-- promoted 已升格为正式知识库文档（不再单独检索，避免重复命中）；rejected 被驳回。
CREATE TABLE kb_reflections (
    id              BIGSERIAL PRIMARY KEY,
    channel         VARCHAR(32)  NOT NULL DEFAULT '',
    chat_id         VARCHAR(128) NOT NULL DEFAULT '',
    question        TEXT         NOT NULL DEFAULT '',
    answer          TEXT         NOT NULL DEFAULT '',
    faq             TEXT         NOT NULL,
    status          VARCHAR(16)  NOT NULL DEFAULT 'approved',
    -- 整理（合并）产生的条目为 true，没有单条来源问答
    compacted       BOOLEAN      NOT NULL DEFAULT false,
    promoted_doc_id BIGINT REFERENCES kb_documents (id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_kb_reflections_status ON kb_reflections (status, created_at DESC);

-- 反思整理记录：整理前后的全量快照，便于回看合并结果。
CREATE TABLE reflect_compactions (
    id           BIGSERIAL PRIMARY KEY,
    before_count INT         NOT NULL,
    after_count  INT         NOT NULL,
    before_json  JSONB       NOT NULL,
    after_json   JSONB       NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- LLM 调用用途：reply 客服回复 / judge 可答性判断 / reflect 反思提炼 / compact 整理 / promote 升格
ALTER TABLE llm_calls ADD COLUMN purpose VARCHAR(16) NOT NULL DEFAULT 'reply';
