<div align="center">

# 知微 Zhiwei

**见微知著的多渠道 AI 客服**

接入 QQ 官方机器人与 Telegram，依据知识库作答、看懂截图、照看群聊，<br>
需要人时转人工；配置全在后台，一个连接串就能跑起来。

[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](server/go.mod)
[![Next.js](https://img.shields.io/badge/Next.js-16-000000?logo=nextdotjs&logoColor=white)](web/package.json)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-无需扩展-4169E1?logo=postgresql&logoColor=white)](docker-compose.yml)
[![License](https://img.shields.io/badge/License-MIT-green)](LICENSE)

[功能](#功能) · [快速开始](#快速开始) · [渠道配置](#渠道配置) · [部署](#生产部署) · [架构](#架构) · [文档](#文档)

</div>

---

## 为什么是知微

- **直连大模型，不套壳。** 服务端自己实现 tool calling 循环，不依赖 Claude Code、agent SDK 或任何 CLI。OpenAI 兼容接口（DeepSeek、通义、MiniMax、Kimi、vLLM、Ollama……）和 Anthropic Messages 都能用。
- **环境变量只有一个。** 只要 `ZHIWEI_DB_DSN`。管理员、模型、渠道凭据、回复策略都在首次启动的向导里填，存进 PostgreSQL；之后在后台改，保存即生效，不用重启。
- **为 prompt cache 设计。** system prompt 恒定不变，检索结果、时间等动态内容全放在 user 侧，让模型的提示词缓存真正命中；后台能看到每天的缓存命中率。
- **部署轻。** 一个 Go 二进制 + 一个 Next.js 后台 + 原生 PostgreSQL，不需要 pgvector、Redis 或消息队列。

## 功能

<table>
<tr>
<td width="50%" valign="top">

### 💬 智能应答
- 依据知识库回答，答不了就转人工队列
- 客户 @ 机器人或私聊发来的截图，交给模型一起看；模型不支持识图时自动退回文字
- 后台可查看全部会话和原图，也能以机器人身份人工回复

</td>
<td width="50%" valign="top">

### 👥 群运营
- **主动回复**：群里有人提问、长时间没人答时谨慎补位
- **入群欢迎**：新成员入群发欢迎语
- **入群审核**：QQ 入群申请按核对接口自动同意 / 拒绝
- 每个群都能单独开关以上功能，QQ 群名自动获取

</td>
</tr>
<tr>
<td valign="top">

### 📚 知识库与技能
- Markdown 文档按目录分类，支持多文件 / 文件夹 / zip 批量导入
- 关键词（BM25）+ 语义向量混合检索，RRF 融合，「怎么充钱」也能找到「充值方法」；向量服务没配或挂了自动退回纯关键词
- 上传 Claude Skills 格式的 `SKILL.md` 技能包，模型用得上才读取完整说明（脚本不会执行）

</td>
<td valign="top">

### 🧠 自我沉淀
- **反思沉淀**：从群主 / 管理员的答复里提炼可复用问答，自动进入检索；定期合并整理，评审通过的升格为正式文档
- **问题排行**：定期把提问交给模型归类，看大家最常问什么、哪些问题在变多
- **实时模型目录**（可选）：查官网公开目录接口回答价格与能力问题

</td>
</tr>
<tr>
<td colspan="2" valign="top">

### 📊 可观测
按天、按用途、按模型统计 token 用量与缓存命中率（按浏览器时区切日）；后台直接看最近的服务日志；调试台用和线上同一套提示词与工具模拟对话，逐轮查看检索命中与工具调用。

</td>
</tr>
</table>

## 快速开始

需要 Go 1.25+、Node.js 20+（pnpm）、Docker。

```bash
git clone https://github.com/qyinter/zhiwei.git && cd zhiwei

# 1. 数据库
docker compose up -d postgres

# 2. 后端（启动日志会打印 setupCode=xxxxxxxx，初始化向导要用）
cd server
export ZHIWEI_DB_DSN='postgres://zhiwei:zhiwei@127.0.0.1:5432/zhiwei?sslmode=disable'
go run ./cmd/zhiwei

# 3. 前端（另开一个终端）
cd web
pnpm install
pnpm dev
```

打开 <http://localhost:3000>，会自动进入初始化向导：

| 步骤 | 内容 |
| :-: | --- |
| 1 | 输入启动日志里的初始化口令 |
| 2 | 创建管理员账号 |
| 3 | 填写品牌信息：品牌名称、一句话简介、官网入口 |
| 4 | 配置大模型并测试连接 |
| 5 | 接入渠道（可跳过，之后在后台配置） |

> [!TIP]
> **想要语义检索？** 再起一个嵌入服务，然后到「系统设置 → 向量检索」开启：
> ```bash
> # Apple Silicon / ARM 先 export TEI_IMAGE=ghcr.io/huggingface/text-embeddings-inference:cpu-arm64-latest
> docker compose up -d embed
> ```
> 它是 HuggingFace TEI + `BAAI/bge-small-zh-v1.5`，常驻内存约 200MB。国内网络可设 `HF_ENDPOINT=https://hf-mirror.com`。

## 渠道配置

| 渠道 | 准备 | 说明 |
| --- | --- | --- |
| **QQ 官方机器人** | 在 [QQ 开放平台](https://q.qq.com) 创建机器人，取得 AppID 和 AppSecret | 支持 WebSocket（不需要公网地址）和 Webhook（回调地址 `https://你的域名/webhook/qqbot`）。缺省只收 @ 它的群消息；在开放平台开通「接收所有消息」后，可在设置里打开「接收群内全部消息」。回复优先用被动回复（5 分钟内有效），超时自动改发主动消息 |
| **Telegram** | 找 [@BotFather](https://t.me/BotFather) 创建机器人，取得 Token | long polling。群里 @ 机器人或回复机器人才会触发；主动回复和反思沉淀需要在 BotFather 关闭 privacy mode。连不上 `api.telegram.org` 时可以把 API 地址换成反代 |
| **企业微信** | 预留 | 接口契约见 [`server/internal/channel/wecom`](server/internal/channel/wecom) |

新增渠道只需实现 `channel.Adapter` 接口并在 `runtime.Apply` 装配，编排器、会话和后台都不用改。

## 生产部署

```bash
# 后端
cd server && go build -o bin/zhiwei ./cmd/zhiwei
ZHIWEI_DB_DSN='postgres://…' ./bin/zhiwei

# 前端
cd web && pnpm install && pnpm build && pnpm start
```

- **只暴露 Next。** Go 服务默认只监听 `127.0.0.1:8080`（可用 `ZHIWEI_HTTP_ADDR` 修改），Next（默认 3000 端口）通过 rewrites 把 `/api/*` 和 `/webhook/*` 转给它。后端不在本机时设置 `ZHIWEI_API_ORIGIN`。
- **加 HTTPS。** 建议前面放 Nginx 或 Caddy；识别到 HTTPS 时会话 cookie 自动带上 `Secure`。
- **嵌入服务**只监听本机 8081；知微也跑在容器里时，把设置里的地址改成 `http://embed/v1`。

> [!IMPORTANT]
> **只能单实例运行。** Telegram 同一个 Token 只允许一个轮询者，会话队列也放在进程内存里。

## 架构

```mermaid
flowchart LR
    subgraph 渠道
        QQ[QQ 官方机器人<br/>WebSocket / Webhook]
        TG[Telegram<br/>long polling]
    end

    subgraph Go 服务
        REG[渠道注册表]
        ORC[编排器<br/>按会话排队]
        BRAIN[客服 Agent<br/>tool calling 循环]
        BG[后台扫描<br/>主动回复 · 反思 · 问题排行]
        API[管理 API]
    end

    LLM[(大模型<br/>OpenAI 兼容 / Anthropic)]
    EMB[(嵌入服务<br/>可选)]
    PG[(PostgreSQL)]
    WEB[Next.js 管理后台]

    QQ <--> REG
    TG <--> REG
    REG <--> ORC --> BRAIN
    BRAIN <--> LLM
    BRAIN -- kb_search · read_skill · transfer_to_human --> PG
    BRAIN -.-> EMB
    BG --> LLM
    BG --> PG
    WEB -- /api rewrites --> API --> PG
```

| 目录 | 内容 |
| --- | --- |
| `server/cmd/zhiwei` | 入口 |
| `server/internal/agent` | 编排器、客服 Agent、主动回复 / 反思 / 问题排行 |
| `server/internal/channel` | 渠道适配（`qqbot`、`telegram`、`wecom`） |
| `server/internal/llm` | OpenAI 兼容与 Anthropic 两种协议的客户端 |
| `server/internal/kb` · `embed` | 知识库混合检索与向量化 |
| `server/internal/skill` | `SKILL.md` 技能包 |
| `server/internal/store` | 持久层（pgx 手写 SQL，不用 ORM） |
| `server/db/migrations` | 内嵌迁移，启动时自动执行 |
| `web/` | Next.js 16 + Tailwind v4 + Radix 管理后台 |
| `docs/` | 设计书与设计规范 |

## 文档

- **设计书**：[`docs/设计书/index.html`](docs/设计书/index.html)，共 10 册（FA-DD-001~010），从总体设计到渠道、Agent、知识库、群运营、反思沉淀、技能逐一展开。
- **设计规范**：[`docs/设计规范/知微设计系统.html`](docs/设计规范/知微设计系统.html)，后台用到的颜色、组件与交互规则，亮暗两套主题。

都是纯静态 HTML，克隆下来用浏览器直接打开即可。

## 开发

| 目的 | 命令 |
| --- | --- |
| 后端检查 | `cd server && go vet ./... && go test ./...` |
| 后端数据库测试 | 先建空库 `zhiwei_test`，再执行 `ZHIWEI_TEST_DSN='postgres://zhiwei:zhiwei@127.0.0.1:5432/zhiwei_test?sslmode=disable' go test -p 1 ./...` |
| 前端检查 | `cd web && pnpm exec tsc --noEmit && pnpm lint && pnpm build` |

> [!WARNING]
> 数据库测试会清空 `ZHIWEI_TEST_DSN` 指向的库，**千万别指向正式库**。

欢迎提 Issue 和 PR。提交信息遵循 [Conventional Commits](https://www.conventionalcommits.org/zh-hans/)（`feat(scope): …`），正文用中文；改了功能请同步更新对应的设计书。更多约定见 [`CLAUDE.md`](CLAUDE.md)。

## 许可证

[MIT](LICENSE) © 2026 千羽
