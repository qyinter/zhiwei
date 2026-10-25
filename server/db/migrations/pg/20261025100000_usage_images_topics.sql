-- 用量统计细化、消息图片、问题排行。

-- 缓存写入 token（Anthropic cache_creation_input_tokens；OpenAI 兼容协议不报，恒为 0）。
-- input_tokens 一直是「全部输入」（含缓存命中与缓存写入），命中率 = cache_read_tokens / input_tokens。
ALTER TABLE llm_calls ADD COLUMN cache_write_tokens INT NOT NULL DEFAULT 0;
-- 本轮依次调用过的工具名（可重复），按工具统计调用次数用
ALTER TABLE llm_calls ADD COLUMN tool_names TEXT[] NOT NULL DEFAULT '{}';
-- 本轮随消息交给模型的图片张数
ALTER TABLE llm_calls ADD COLUMN images INT NOT NULL DEFAULT 0;

-- 客户随消息发来的图片：下载、缩放后的字节，供后台查看与当轮识别。
-- 只保留 30 天（反思循环里清理），过期后消息里仍留「[图片]」占位。
CREATE TABLE message_images (
    id         BIGSERIAL PRIMARY KEY,
    message_id BIGINT      NOT NULL REFERENCES messages (id) ON DELETE CASCADE,
    ord        INT         NOT NULL DEFAULT 0,
    mime       VARCHAR(32) NOT NULL,
    width      INT         NOT NULL DEFAULT 0,
    height     INT         NOT NULL DEFAULT 0,
    data       BYTEA       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_message_images_message ON message_images (message_id, ord);
CREATE INDEX idx_message_images_created ON message_images (created_at);

-- 问题排行：模型把客户提问归到主题，每次归类记一条出现记录。
CREATE TABLE question_topics (
    id         BIGSERIAL PRIMARY KEY,
    title      VARCHAR(255) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    -- 最近一次被归入的时间：越活跃越靠前，优先交给模型做归并参考
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_question_topics_updated ON question_topics (updated_at DESC);

CREATE TABLE question_occurrences (
    id         BIGSERIAL PRIMARY KEY,
    topic_id   BIGINT       NOT NULL REFERENCES question_topics (id) ON DELETE CASCADE,
    channel    VARCHAR(32)  NOT NULL,
    chat_type  VARCHAR(16)  NOT NULL DEFAULT '',
    chat_id    VARCHAR(128) NOT NULL,
    user_id    VARCHAR(128) NOT NULL,
    user_name  VARCHAR(255) NOT NULL DEFAULT '',
    text       TEXT         NOT NULL,
    -- 原消息的时间（排行按它划时间窗）
    asked_at   TIMESTAMPTZ  NOT NULL,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_question_occurrences_asked ON question_occurrences (asked_at);
CREATE INDEX idx_question_occurrences_topic ON question_occurrences (topic_id, asked_at DESC);
