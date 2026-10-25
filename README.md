# 知微 Zhiwei

知微是一个多渠道 AI 客服。它接入 QQ 官方机器人和 Telegram，企业微信的接口已经预留。机器人依据知识库回答问题，需要人工时转入人工队列，后台可以查看全部会话，也能以机器人身份人工回复。

- **直连大模型**：不依赖 Claude Code、agent SDK 或任何 CLI。服务端自己实现 tool calling 循环，支持 OpenAI 兼容接口（DeepSeek、通义、MiniMax、Kimi、vLLM、Ollama 等）和 Anthropic Messages。
- **最小化 env**：环境变量只需要一个 `ZHIWEI_DB_DSN`。管理员、模型、渠道凭据和回复策略都在首次启动的初始化向导里填写，存进 PostgreSQL，之后在后台修改，保存即生效，不用重启。
- **识别图片**：客户 @ 机器人或私聊时发来的截图会交给模型一起看（模型不支持识图时自动退回文字），后台会话里也能看到原图。
- **群运营**：群里长时间没人答的问题会谨慎补位（主动回复），新成员入群发欢迎语，QQ 入群申请按核对接口自动审批。每个群可以单独开关这些功能。
- **问题排行**：定期把群里和私聊里的提问交给模型归类，看看大家最常问什么、哪些问题在变多。
- **知识库**：Markdown 文档按目录分类，支持多文件 / 文件夹 / zip 批量导入。检索同时走关键词（BM25）和语义向量，「怎么充钱」也能找到「充值方法」；向量模型单独部署，不配时自动退回纯关键词。
- **技能**：上传 Claude Skills 格式的 SKILL.md 技能包（文件夹 / zip / 单个 .md），教机器人处理特定类型的问题。模型先看技能的适用场景，用得上才读取完整说明和参考文件。脚本不会执行。
- **实时模型目录**（可选）：客户问模型价格、有哪些模型、支不支持某能力时，机器人直接查官网公开目录接口作答，报价带币种、不会自己乘倍率。
- **反思沉淀**：从群主 / 管理员的答复里提炼可复用问答，自动进入检索；定期整理合并，评审通过的升格为正式知识库文档。
- **用量统计与运行日志**：按天、按用途、按模型看 token 和缓存命中率；后台能直接看最近的服务日志。
- **技术栈**：后端 Go（gin + pgx），前端 Next.js 16 + Tailwind v4 + Radix，数据库 PostgreSQL（不需要任何扩展）。

## 快速开始

```bash
# 1. 数据库（+ 可选的向量嵌入服务；Apple Silicon 先 export TEI_IMAGE=ghcr.io/huggingface/text-embeddings-inference:cpu-arm64-latest）
docker compose up -d postgres
docker compose up -d embed          # 可选，启动后到「系统设置 → 向量检索」开启

# 2. 后端
cd server
export ZHIWEI_DB_DSN='postgres://zhiwei:zhiwei@127.0.0.1:5432/zhiwei?sslmode=disable'
go run ./cmd/zhiwei        # 启动日志里会打印 setupCode=xxxxxxxx

# 3. 前端
cd web
pnpm install
pnpm dev                    # 或 pnpm build && pnpm start
```

打开 http://localhost:3000 后会自动进入初始化向导：

1. 输入初始化口令
2. 创建管理员账号
3. 填写品牌信息
4. 配置大模型并测试连接
5. 接入渠道（可以跳过，之后在后台配置）

## 渠道配置

| 渠道 | 准备 | 说明 |
| --- | --- | --- |
| QQ 官方机器人 | 在 [QQ 开放平台](https://q.qq.com) 创建机器人，取得 AppID 和 AppSecret | 有两种接入方式。WebSocket 不需要公网地址；Webhook 需要在平台上填回调地址 `https://你的域名/webhook/qqbot`。缺省只收 @ 它的群消息；在开放平台开通「接收所有消息」后，可在设置里打开「接收群内全部消息」。回复优先用被动回复（5 分钟内有效），超时后自动改发主动消息（受频控） |
| Telegram | 找 @BotFather 创建机器人，取得 Token | 使用 long polling。群里要 @ 机器人或回复机器人才会触发；主动回复与反思沉淀需要在 BotFather 关闭 privacy mode；服务器连不上 api.telegram.org 时，可以把 API 地址换成反代 |
| 企业微信 | 预留 | 接口契约见 `server/internal/channel/wecom` |

## 目录

```
server/   Go 服务（cmd/zhiwei 入口，internal/* 各模块，db/migrations 内嵌迁移）
web/      Next.js 管理后台（/setup、/login、/admin/*）
docs/     设计书（FA-DD-001~010，打开 docs/设计书/index.html）与设计规范
```

## 开发命令

| 目的 | 命令 |
| --- | --- |
| 后端测试 | `cd server && go vet ./... && go test ./...` |
| 后端数据库测试 | 先建空库 `zhiwei_test`，再 `ZHIWEI_TEST_DSN='postgres://zhiwei:zhiwei@127.0.0.1:5432/zhiwei_test?sslmode=disable' go test -p 1 ./...`（会清空该库） |
| 前端检查 | `cd web && pnpm exec tsc --noEmit && pnpm lint && pnpm build` |

## 生产部署要点

- Go 服务默认只监听 `127.0.0.1:8080`，对外只暴露 Next（默认 3000 端口）。Next 通过 rewrites 把 `/api/*` 和 `/webhook/*` 转给 Go；后端地址不在本机时，设置 `ZHIWEI_API_ORIGIN`。
- 向量检索的嵌入服务（compose 的 `embed`，约 200MB 内存）只监听本机 8081；知微也跑在容器里时，把设置里的地址改成 `http://embed/v1`。国内服务器首次拉模型可设 `HF_ENDPOINT=https://hf-mirror.com`。
- 只能单实例运行。原因有两个：Telegram 同一个 token 只允许一个轮询者；会话队列放在进程内存里。
- 建议在前面加 Nginx 或 Caddy 提供 HTTPS，会话 cookie 会自动带上 `Secure`。

## 许可证

[MIT](LICENSE)
