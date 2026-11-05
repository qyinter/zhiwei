-- Telegram 入群审核（私聊验证）与群消息治理。

-- 入群申请新增两种结果：awaiting（已私聊申请人，等待回复）由后续回复 / 超时更新为最终结果。
-- 记录在等待期间会被更新，补 updated_at。
ALTER TABLE join_requests ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- 等待申请人私聊回答的入群申请（TG 的申请事件不带答案，机器人私聊索取）。
-- 同一人同时申请多个群各一行；回复按 (channel, user_id) 取最近一条。
CREATE TABLE join_pending (
    channel    VARCHAR(32)  NOT NULL,
    user_id    VARCHAR(128) NOT NULL,
    chat_id    VARCHAR(128) NOT NULL,
    chat_title VARCHAR(255) NOT NULL DEFAULT '',
    user_name  VARCHAR(255) NOT NULL DEFAULT '',
    request_id TEXT         NOT NULL DEFAULT '',
    -- 申请时的个人简介（TG bio），交给模型审核时一并参考
    bio        TEXT         NOT NULL DEFAULT '',
    record_id  BIGINT       NOT NULL REFERENCES join_requests (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (channel, user_id, chat_id)
);
CREATE INDEX idx_join_pending_expires ON join_pending (expires_at);

-- 群成员入群时间：群消息治理据此判断「新人」。只记机器人见到的入群事件；更早的成员没有记录，按老成员对待。
CREATE TABLE group_members (
    channel   VARCHAR(32)  NOT NULL,
    chat_id   VARCHAR(128) NOT NULL,
    user_id   VARCHAR(128) NOT NULL,
    user_name VARCHAR(255) NOT NULL DEFAULT '',
    joined_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (channel, chat_id, user_id)
);

-- 群消息治理处置记录：规则层 / 模型层判定为广告后的处置与执行结果，后台可「误判恢复」。
CREATE TABLE moderation_actions (
    id          BIGSERIAL    PRIMARY KEY,
    channel     VARCHAR(32)  NOT NULL,
    chat_id     VARCHAR(128) NOT NULL,
    chat_title  VARCHAR(255) NOT NULL DEFAULT '',
    user_id     VARCHAR(128) NOT NULL,
    user_name   VARCHAR(255) NOT NULL DEFAULT '',
    message_id  TEXT         NOT NULL DEFAULT '',
    -- 被处置消息的正文（截到 500 字，图片以占位表示）
    content     TEXT         NOT NULL DEFAULT '',
    -- rule 规则层 / llm 模型层
    source      VARCHAR(16)  NOT NULL,
    reason      TEXT         NOT NULL DEFAULT '',
    confidence  REAL         NOT NULL DEFAULT 0,
    -- none 仅记录（模型置信度不足）/ delete 删消息 / mute 删消息并禁言 / ban 删消息并封禁
    action      VARCHAR(16)  NOT NULL,
    -- 执行结果：成功为空，失败记错误原因
    detail      TEXT         NOT NULL DEFAULT '',
    reverted_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_moderation_actions_created ON moderation_actions (created_at DESC);
CREATE INDEX idx_moderation_actions_user ON moderation_actions (channel, chat_id, user_id, created_at DESC);
