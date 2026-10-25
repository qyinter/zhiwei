# CLAUDE.md

知微（Zhiwei）：多渠道 AI 客服。Go 后端（`server/`）+ Next.js 16 管理后台（`web/`）+ PostgreSQL。本项目**不引入任何 agent SDK / CLI**，LLM 一律 HTTP 直连 tool calling。代码注释与文档用简体中文。

## 命令

- 数据库：`docker compose up -d postgres`；可选嵌入服务 `docker compose up -d embed`（Apple Silicon 设 `TEI_IMAGE=…:cpu-arm64-latest`）
- 后端：`cd server && ZHIWEI_DB_DSN=… go run ./cmd/zhiwei`；检查 `go vet ./... && go test ./...`
- 数据库测试（store / kb 混合检索 / agent 流程）：`ZHIWEI_TEST_DSN=…/zhiwei_test go test -p 1 ./...`。会 DROP 测试库的 public schema，**绝不能指向正式库**；`-p 1` 必须带，几个包共用同一个测试库。改了 SQL 必须跑
- 前端：`cd web && pnpm dev`；检查 `pnpm exec tsc --noEmit && pnpm lint && pnpm build`
- 声称完成前两端检查都要跑。

## 关键约定

- **env 只有 `ZHIWEI_DB_DSN`（+ 可选 `ZHIWEI_HTTP_ADDR` / web 的 `ZHIWEI_API_ORIGIN`）。** 新配置项一律加到 `server/internal/settings` 的分区结构体（带缺省值、Validate、密钥加进 Masked/Unmask），不要加 env。前端同步 `web/src/lib/types.ts` 与 `components/settings-forms.tsx`。
- **system prompt 必须恒定**（只依赖品牌 + 业务口径），动态内容放 user 侧——为了 prompt cache，别把时间 / 检索结果塞进 system。
- **迁移**：`server/db/migrations/pg/<14位UTC时间戳>_<snake>.sql`，已应用文件永不修改（checksum 校验，连注释都不能改；所以首个迁移的注释里还是项目旧名「开元」），不写 down，seed 用 `ON CONFLICT DO NOTHING`。
- **新渠道** = 实现 `channel.Adapter`（需要回调再实现 `WebhookAdapter`，支持入群审批再实现 `JoinReviewer`）+ 在 `runtime.Apply` 装配；编排器、会话、后台不应需要改动。入群 / 入群申请以 `Inbound.Kind` 区分。
- **QQ 用户 ID 一律取 `author.id`**（实测同一机器人下群聊 / 单聊相同）。QQ 订阅位按需加：未开通权限的 intents 会让网关直接断开。
- 后台扫描器（主动回复 / 反思 / 问题排行）每轮从 `snapshot()` 读设置，改设置不重启；游标在 `poller_cursors`。后台一次性 LLM 调用走 `askJSON`（记 `llm_calls.purpose`）。群级功能一律用 `settings.All` 的 `XxxOn(channel, chatId)` / `AnyXxx()` 判断（按群设置覆盖全局），别直接读 `.Enabled`。
- **统计按浏览器时区切日**：库的时区是 UTC，涉及「今日 / 按天」的接口带 `?tz=`，别用 `date_trunc('day', now())`。
- **图片**：渠道只给 `channel.Image.Open` 下载入口，下载 / 缩放（`internal/media`）在会话 worker 里做，不能阻塞渠道事件循环；历史与群时间线只存「[图片]」占位。
- **知识库检索**在进程内：BM25 + 可选向量（外部 OpenAI 兼容 `/embeddings`，向量存 `REAL[]`，不用 pgvector），RRF 融合；嵌入服务挂了必须能退回纯关键词。向量以 `embed_model` 标版本，改向量化文本的构成要考虑已有向量是否失效。
- **技能**（`internal/skill`，SKILL.md 格式）：system prompt 只列启用技能的 name + description，正文由模型调 `read_skill` 按需读；增删改技能后必须调 `Orchestrator.ReloadSkills` 重建大脑。不执行脚本，没有沙箱就别加。
- 持久层只有 `internal/store`（pgx 手写 SQL，不引 ORM）。
- 前端：页面都是客户端组件、经 `lib/api.ts` 同源请求；UI 基础组件在 `components/ui`，令牌在 `globals.css`，规范见 `docs/设计规范/知微设计系统.html`。加载态用 `useDelayedLoading/useDelayedPending`，分页每页 20，数字输入用 string 草稿。
- Next.js 16 与训练数据不同，写前先读 `web/node_modules/next/dist/docs/`（见 web/AGENTS.md）。

## 文档

功能实装 / 变更后同步更新 `docs/设计书/FA-DD-XXX_*.html`（FA-STD-001 七章格式，纯语义 HTML、零 class 零脚本，错误信息照抄实现，改订履历追加一行）；新增设计书在 `assets/manifest.js` 与 `index.html` 登记。

## Git

Conventional Commits（`feat/fix(scope): …`）+ 中文正文；功能分支开发，不直接提交 main。
