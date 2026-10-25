-- 开元基线 schema（v0.1）

-- 系统设置 KV：初始化向导与管理后台写入，运行时按 key 读取。
-- value 存整段 JSON（一个 key = 一个设置分区，如 llm / channels.qqbot），
-- 分区内字段增减不需要迁移。
CREATE TABLE system_settings (
    key        VARCHAR(64) PRIMARY KEY,
    value      JSONB       NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE admin_users (
    id            BIGSERIAL PRIMARY KEY,
    username      VARCHAR(64)  NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    last_login_at TIMESTAMPTZ
);

-- 登录会话：只存 token 的 SHA-256，库泄露也拿不到可用 token。
CREATE TABLE admin_sessions (
    token_hash VARCHAR(64) PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES admin_users (id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_admin_sessions_expires ON admin_sessions (expires_at);

-- 会话 = 渠道 × 聊天 × 用户。群里每个用户各一个会话（上下文互不串），
-- 私聊 user_id = 对方。status=handoff 时暂停自动回复。
CREATE TABLE conversations (
    id                 BIGSERIAL PRIMARY KEY,
    channel            VARCHAR(16)  NOT NULL,
    chat_type          VARCHAR(16)  NOT NULL,
    chat_id            VARCHAR(128) NOT NULL,
    user_id            VARCHAR(128) NOT NULL,
    user_name          VARCHAR(255) NOT NULL DEFAULT '',
    title              VARCHAR(255) NOT NULL DEFAULT '',
    status             VARCHAR(16)  NOT NULL DEFAULT 'active',
    -- 上下文起点：/reset 或人工恢复时推进，早于它的消息不再喂给模型
    context_since      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    -- 最近一条入站消息的平台 ID：QQ 官方机器人被动回复必须带它（5 分钟窗口）
    last_inbound_msg_id VARCHAR(128) NOT NULL DEFAULT '',
    last_message_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    created_at         TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (channel, chat_id, user_id)
);
CREATE INDEX idx_conversations_last_message ON conversations (last_message_at DESC);

CREATE TABLE messages (
    id              BIGSERIAL PRIMARY KEY,
    conversation_id BIGINT       NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    -- in: 用户发来；out: 机器人回复；human: 后台人工回复
    direction       VARCHAR(8)   NOT NULL,
    sender_id       VARCHAR(128) NOT NULL DEFAULT '',
    sender_name     VARCHAR(255) NOT NULL DEFAULT '',
    content         TEXT         NOT NULL,
    platform_msg_id VARCHAR(128) NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_messages_conversation ON messages (conversation_id, created_at);
-- 入站去重：平台重投（QQ webhook 超时重推、WS 重连补发）同一条消息只处理一次
CREATE UNIQUE INDEX uq_messages_inbound ON messages (conversation_id, platform_msg_id)
    WHERE direction = 'in' AND platform_msg_id <> '';

-- 转人工工单：一次 handoff 一行，处理完标 resolved 并恢复会话自动回复。
CREATE TABLE handoffs (
    id              BIGSERIAL PRIMARY KEY,
    conversation_id BIGINT      NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    reason          TEXT        NOT NULL DEFAULT '',
    summary         TEXT        NOT NULL DEFAULT '',
    status          VARCHAR(16) NOT NULL DEFAULT 'open',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- resolved_by 为空 + resolved 表示超时自动恢复
    resolved_at     TIMESTAMPTZ,
    resolved_by     BIGINT REFERENCES admin_users (id) ON DELETE SET NULL
);
CREATE INDEX idx_handoffs_status ON handoffs (status, created_at DESC);

CREATE TABLE kb_documents (
    id         BIGSERIAL PRIMARY KEY,
    title      VARCHAR(255) NOT NULL,
    content    TEXT         NOT NULL,
    enabled    BOOLEAN      NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- 文档切块：检索粒度。文档保存时整篇重切（先删后插，同事务）。
CREATE TABLE kb_chunks (
    id          BIGSERIAL PRIMARY KEY,
    document_id BIGINT  NOT NULL REFERENCES kb_documents (id) ON DELETE CASCADE,
    ord         INT     NOT NULL,
    heading     TEXT    NOT NULL DEFAULT '',
    content     TEXT    NOT NULL
);
CREATE INDEX idx_kb_chunks_document ON kb_chunks (document_id, ord);

-- 每次 LLM 调用一行：用量统计与排障。
CREATE TABLE llm_calls (
    id              BIGSERIAL PRIMARY KEY,
    conversation_id BIGINT REFERENCES conversations (id) ON DELETE SET NULL,
    model           VARCHAR(128) NOT NULL,
    input_tokens    INT          NOT NULL DEFAULT 0,
    output_tokens   INT          NOT NULL DEFAULT 0,
    cache_read_tokens INT        NOT NULL DEFAULT 0,
    latency_ms      INT          NOT NULL DEFAULT 0,
    tool_calls      INT          NOT NULL DEFAULT 0,
    error           TEXT         NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_llm_calls_created ON llm_calls (created_at DESC);
