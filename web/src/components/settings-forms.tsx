"use client"

// 初始化向导与设置页共用的分区表单：受控组件，value + onChange(整段分区)。
import { CheckCircle2Icon, PlugZapIcon } from "lucide-react"
import { useState } from "react"

import { Field } from "@/components/common"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { api } from "@/lib/api"
import type {
  AgentSettings,
  Brand,
  EmbeddingSettings,
  JoinReviewSettings,
  LLMSettings,
  ModelCatalogSettings,
  ProactiveSettings,
  QQBotSettings,
  ReflectSettings,
  TelegramSettings,
  TopicsSettings,
} from "@/lib/types"
import { useDelayedPending } from "@/lib/use-delayed-pending"

type FormProps<T> = { value: T; onChange: (v: T) => void }

/** 数字输入：本地 string 草稿 + 失焦解析（直接 Number() 会吞掉输入中的小数点） */
function NumberInput({ value, onChange, step, id }: { value: number; onChange: (n: number) => void; step?: string; id?: string }) {
  const [draft, setDraft] = useState<string | null>(null)
  return (
    <Input
      id={id}
      inputMode="decimal"
      step={step}
      value={draft ?? String(value)}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={() => {
        if (draft !== null) {
          const n = Number(draft)
          if (!Number.isNaN(n)) onChange(n)
          setDraft(null)
        }
      }}
      className="font-mono tabular-nums"
    />
  )
}

/** 每行一项的列表输入：编辑时保留原文（含空行），失焦再解析，否则回车出的空行会被立刻吞掉 */
function LinesInput({ value, onChange }: { value: string[]; onChange: (v: string[]) => void }) {
  const [draft, setDraft] = useState<string | null>(null)
  return (
    <Textarea
      rows={4}
      className="font-mono"
      value={draft ?? value.join("\n")}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={() => {
        if (draft !== null) {
          onChange(draft.split("\n").map((s) => s.trim()).filter(Boolean))
          setDraft(null)
        }
      }}
    />
  )
}

export function ToggleRow({ label, hint, checked, onChange }: { label: string; hint?: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="flex cursor-pointer items-start justify-between gap-4 rounded-lg py-1">
      <span className="min-w-0">
        <span className="block text-[13.5px] font-medium text-foreground">{label}</span>
        {hint && <span className="block text-xs text-muted-foreground">{hint}</span>}
      </span>
      <Switch checked={checked} onCheckedChange={onChange} />
    </label>
  )
}

export function BrandForm({ value, onChange }: FormProps<Brand>) {
  const set = (patch: Partial<Brand>) => onChange({ ...value, ...patch })
  return (
    <div className="grid gap-5">
      <Field label="品牌名称" hint="机器人自称与回复口径都会用它，例如「知微」" htmlFor="brand-name">
        <Input id="brand-name" value={value.name} onChange={(e) => set({ name: e.target.value })} placeholder="知微" />
      </Field>
      <Field label="一句话简介" hint="告诉模型你们是做什么的，写进系统提示词" htmlFor="brand-desc">
        <Input id="brand-desc" value={value.description} onChange={(e) => set({ description: e.target.value })} placeholder="面向开发者的 AI API 网关" />
      </Field>
      <Field label="官网 / 客服入口" hint="出错兜底与帮助文案里给用户的链接，可留空" htmlFor="brand-url">
        <Input id="brand-url" value={value.supportUrl} onChange={(e) => set({ supportUrl: e.target.value })} placeholder="https://example.com" />
      </Field>
    </div>
  )
}

const LLM_PRESETS: { label: string; protocol: LLMSettings["protocol"]; baseUrl: string; model: string }[] = [
  { label: "OpenAI", protocol: "openai", baseUrl: "https://api.openai.com/v1", model: "gpt-4.1-mini" },
  { label: "DeepSeek", protocol: "openai", baseUrl: "https://api.deepseek.com/v1", model: "deepseek-chat" },
  { label: "通义千问", protocol: "openai", baseUrl: "https://dashscope.aliyuncs.com/compatible-mode/v1", model: "qwen-plus" },
  { label: "Anthropic", protocol: "anthropic", baseUrl: "https://api.anthropic.com", model: "claude-haiku-5-5" },
]

/**
 * 模型配置 + 测试连接。testUrl/testHeaders 区分向导（带初始化口令）与设置页（带登录态、密钥可为掩码）。
 */
export function LLMForm({ value, onChange, testUrl, testHeaders }: FormProps<LLMSettings> & { testUrl: string; testHeaders?: Record<string, string> }) {
  const set = (patch: Partial<LLMSettings>) => onChange({ ...value, ...patch })
  const { pending, track } = useDelayedPending()
  const [result, setResult] = useState<{ reply: string; latencyMs: number } | null>(null)

  const test = () =>
    track(async () => {
      setResult(null)
      setResult(await api.post<{ reply: string; latencyMs: number }>(testUrl, value, { headers: testHeaders }))
    }).catch(() => {})

  return (
    <div className="grid gap-5">
      <div className="flex flex-wrap gap-2">
        {LLM_PRESETS.map((p) => (
          <Button key={p.label} size="sm" variant="outline" type="button" onClick={() => set({ protocol: p.protocol, baseUrl: p.baseUrl, model: p.model })}>
            {p.label}
          </Button>
        ))}
      </div>
      <div className="grid gap-5 sm:grid-cols-2">
        <Field label="接口协议" hint={value.protocol === "openai" ? "POST {Base URL}/chat/completions" : "POST {Base URL}/v1/messages"}>
          <Select value={value.protocol} onValueChange={(v) => set({ protocol: v as LLMSettings["protocol"] })}>
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="openai">OpenAI 兼容（Chat Completions）</SelectItem>
              <SelectItem value="anthropic">Anthropic（Messages）</SelectItem>
            </SelectContent>
          </Select>
        </Field>
        <Field label="模型" htmlFor="llm-model">
          <Input id="llm-model" className="font-mono" value={value.model} onChange={(e) => set({ model: e.target.value })} placeholder="gpt-4.1-mini" />
        </Field>
      </div>
      <Field label="Base URL" hint={value.protocol === "openai" ? "填到 /v1 为止，例如 https://api.deepseek.com/v1" : "不带 /v1，例如 https://api.anthropic.com"} htmlFor="llm-base">
        <Input id="llm-base" className="font-mono" value={value.baseUrl} onChange={(e) => set({ baseUrl: e.target.value })} />
      </Field>
      <Field label="API Key" hint="只存在你的数据库里；页面回显为掩码" htmlFor="llm-key">
        <Input id="llm-key" className="font-mono" type="password" autoComplete="off" value={value.apiKey} onChange={(e) => set({ apiKey: e.target.value })} placeholder="sk-…" />
      </Field>
      <div className="grid gap-5 sm:grid-cols-3">
        <Field label="Temperature">
          <NumberInput step="0.1" value={value.temperature} onChange={(n) => set({ temperature: n })} />
        </Field>
        <Field label="最大输出 token">
          <NumberInput value={value.maxTokens} onChange={(n) => set({ maxTokens: n })} />
        </Field>
        <Field label="请求超时（秒）">
          <NumberInput value={value.timeoutSec} onChange={(n) => set({ timeoutSec: n })} />
        </Field>
      </div>
      <div className="flex flex-wrap items-center gap-3">
        <Button type="button" variant="secondary" onClick={test} disabled={pending || !value.apiKey || !value.model}>
          <PlugZapIcon />
          {pending ? "测试中…" : "测试连接"}
        </Button>
        {result && (
          <span className="inline-flex items-center gap-1.5 text-[13px] text-ok">
            <CheckCircle2Icon className="size-4" />
            连接正常 · <span className="font-mono tabular-nums">{result.latencyMs}ms</span> · 「{result.reply.slice(0, 30)}」
          </span>
        )}
      </div>
    </div>
  )
}

export function QQBotForm({ value, onChange }: FormProps<QQBotSettings>) {
  const set = (patch: Partial<QQBotSettings>) => onChange({ ...value, ...patch })
  return (
    <div className="grid gap-5">
      <ToggleRow label="启用 QQ 官方机器人" hint="在 QQ 开放平台（q.qq.com）创建机器人后获取 AppID 与 AppSecret" checked={value.enabled} onChange={(enabled) => set({ enabled })} />
      <div className="grid gap-5 sm:grid-cols-2">
        <Field label="AppID" htmlFor="qq-appid">
          <Input id="qq-appid" className="font-mono" value={value.appId} onChange={(e) => set({ appId: e.target.value })} />
        </Field>
        <Field label="AppSecret" htmlFor="qq-secret">
          <Input id="qq-secret" className="font-mono" type="password" autoComplete="off" value={value.appSecret} onChange={(e) => set({ appSecret: e.target.value })} />
        </Field>
      </div>
      <Field
        label="事件接收方式"
        hint={
          value.mode === "webhook"
            ? "在开放平台「回调配置」填 https://你的域名/webhook/qqbot（需公网 HTTPS）"
            : "主动连接 QQ 网关，无需公网地址，适合开发与内网部署"
        }
      >
        <Select value={value.mode} onValueChange={(v) => set({ mode: v as QQBotSettings["mode"] })}>
          <SelectTrigger className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="websocket">WebSocket 长连接</SelectItem>
            <SelectItem value="webhook">Webhook 回调</SelectItem>
          </SelectContent>
        </Select>
      </Field>
      <div className="grid gap-3 rounded-xl border bg-muted/40 p-4">
        <ToggleRow label="QQ 群" checked={value.group} onChange={(group) => set({ group })} />
        {value.group && (
          <>
            <ToggleRow label="接收群内全部消息" hint="需先在开放平台为机器人开通「接收所有消息」；关闭时只收 @机器人 的消息。主动回复与反思沉淀依赖它" checked={value.groupAll} onChange={(groupAll) => set({ groupAll })} />
            <ToggleRow label="回复时 @ 提问人（实验）" hint="按官方「文本交互」格式发送 <qqbot-at-user />；实测主动消息里会显示成原文，建议保持关闭" checked={value.atUser} onChange={(atUser) => set({ atUser })} />
          </>
        )}
        <ToggleRow label="QQ 单聊" checked={value.c2c} onChange={(c2c) => set({ c2c })} />
        <ToggleRow label="频道（@机器人）" hint="需在开放平台开通公域频道消息权限" checked={value.guild} onChange={(guild) => set({ guild })} />
        <ToggleRow label="频道私信" checked={value.guildDm} onChange={(guildDm) => set({ guildDm })} />
        <ToggleRow label="沙箱环境" hint="使用 sandbox.api.sgroup.qq.com（机器人未上线前的调试环境）" checked={value.sandbox} onChange={(sandbox) => set({ sandbox })} />
      </div>
    </div>
  )
}

export function TelegramForm({ value, onChange }: FormProps<TelegramSettings>) {
  const set = (patch: Partial<TelegramSettings>) => onChange({ ...value, ...patch })
  return (
    <div className="grid gap-5">
      <ToggleRow label="启用 Telegram Bot" hint="在 @BotFather 创建机器人获取 Token；群里需 @机器人 或回复机器人才会触发" checked={value.enabled} onChange={(enabled) => set({ enabled })} />
      <Field label="Bot Token" htmlFor="tg-token">
        <Input id="tg-token" className="font-mono" type="password" autoComplete="off" value={value.botToken} onChange={(e) => set({ botToken: e.target.value })} placeholder="123456:ABC-…" />
      </Field>
      <Field label="API 地址" hint="服务器无法直连 api.telegram.org 时，可填自建 Bot API Server 或反代地址" htmlFor="tg-base">
        <Input id="tg-base" className="font-mono" value={value.apiBase} onChange={(e) => set({ apiBase: e.target.value })} />
      </Field>
      <ToggleRow label="响应私聊" checked={value.private} onChange={(v) => set({ private: v })} />
    </div>
  )
}

export function AgentForm({ value, onChange }: FormProps<AgentSettings>) {
  const set = (patch: Partial<AgentSettings>) => onChange({ ...value, ...patch })
  return (
    <div className="grid gap-5">
      <Field label="业务口径补充" hint="追加到系统提示词末尾：价格规则、禁止承诺事项、常用话术等。保持稳定，频繁改动会降低缓存命中" htmlFor="agent-extra">
        <Textarea id="agent-extra" rows={6} value={value.extraPrompt} onChange={(e) => set({ extraPrompt: e.target.value })} placeholder="例如：退款统一引导用户提交工单，不承诺到账时间。" />
      </Field>
      <div className="grid gap-5 sm:grid-cols-3">
        <Field label="历史消息条数" hint="0 = 不带历史">
          <NumberInput value={value.historyLimit} onChange={(n) => set({ historyLimit: n })} />
        </Field>
        <Field label="空闲重置（分钟）" hint="隔多久算新对话；0 不重置">
          <NumberInput value={value.idleResetMin} onChange={(n) => set({ idleResetMin: n })} />
        </Field>
        <Field label="单条回复字数" hint="超出按标点拆条；0 不拆">
          <NumberInput value={value.maxReplyChars} onChange={(n) => set({ maxReplyChars: n })} />
        </Field>
        <Field label="工具往返上限">
          <NumberInput value={value.maxToolRounds} onChange={(n) => set({ maxToolRounds: n })} />
        </Field>
        <Field label="单轮超时（秒）">
          <NumberInput value={value.runTimeoutSec} onChange={(n) => set({ runTimeoutSec: n })} />
        </Field>
        <Field label="人工超时（分钟）" hint="无人处理自动恢复；0 不恢复">
          <NumberInput value={value.handoffTimeoutMin} onChange={(n) => set({ handoffTimeoutMin: n })} />
        </Field>
      </div>
      <div className="grid gap-3 rounded-xl border bg-muted/40 p-4">
        <ToggleRow label="@ 后先回「收到，查询中…」" hint="群里推理要几秒到几十秒，先回一句让用户知道机器人接到了。走被动回复，不占主动消息额度（占同一条消息 5 次回复中的 1 次）" checked={value.ackEnabled} onChange={(ackEnabled) => set({ ackEnabled })} />
        <ToggleRow
          label="回复使用 Markdown 排版"
          hint="重点加粗、步骤列表、命令放代码块。QQ 群聊 / 单聊发 Markdown 消息（不支持代码，代码块显示为引用），Telegram 转为富文本；关闭则要求模型只写纯文本"
          checked={value.replyMarkdown}
          onChange={(replyMarkdown) => set({ replyMarkdown })}
        />
        <ToggleRow
          label="识别图片"
          hint="客户 @ 机器人或私聊时发来的截图交给模型一起看（每轮最多 4 张，缩到长边 1568 像素）。需要模型支持识图；模型拒收时自动去掉图片重试。关闭后只告诉模型「客户发了图片」，由它请客户用文字描述。图片在后台会话里保留 30 天"
          checked={value.vision}
          onChange={(vision) => set({ vision })}
        />
        <ToggleRow label="知识库预检索" hint="调模型前先检索并注入候选片段，减少模型不查资料就作答" checked={value.kbPrefetch} onChange={(kbPrefetch) => set({ kbPrefetch })} />
        {value.kbPrefetch && (
          <Field label="预检索条数">
            <NumberInput value={value.kbTopK} onChange={(n) => set({ kbTopK: n })} />
          </Field>
        )}
      </div>
      <div className="grid gap-3 rounded-xl border bg-muted/40 p-4">
        <ToggleRow label="响应所有群" hint="关闭后只响应下方白名单里的群 / 频道；私聊不受限制" checked={value.allowAllChats} onChange={(allowAllChats) => set({ allowAllChats })} />
        {!value.allowAllChats && (
          <Field label="群白名单" hint="每行一个，格式 渠道:群ID，例如 qqbot:8F3A…（群 openid）或 telegram:-100123456。可在会话列表里查到 ID">
            <LinesInput value={value.chats} onChange={(chats) => set({ chats })} />
          </Field>
        )}
      </div>
    </div>
  )
}

// ── 群运营 ──

export function JoinReviewForm({ value, onChange }: FormProps<JoinReviewSettings>) {
  const set = (patch: Partial<JoinReviewSettings>) => onChange({ ...value, ...patch })
  return (
    <div className="grid gap-5">
      <ToggleRow
        label="开启入群审核"
        hint="QQ 生效群收到加群申请时，取验证消息 / 入群问题的答案调核对接口：存在则自动同意，不存在则自动拒绝；接口不可用时不处理，留给管理员。机器人需是群管理员，并在开放平台开通群成员事件权限"
        checked={value.enabled}
        onChange={(enabled) => set({ enabled })}
      />
      <Field label="核对接口" hint={'调用时追加 ?id=，只认 {"status":"ok"} / {"status":"error"}。'} htmlFor="jr-url">
        <Input id="jr-url" className="font-mono" value={value.checkUrl} onChange={(e) => set({ checkUrl: e.target.value })} placeholder="https://example.com/api/member-check" />
      </Field>
      <Field label="ID 提取规则" hint="正则：从答案里取第一个前后不紧贴字母数字的匹配；留空则整段答案作为 ID。缺省 [A-Za-z0-9]{10}（10 位用户 ID）" htmlFor="jr-pattern">
        <Input id="jr-pattern" className="font-mono" value={value.idPattern} onChange={(e) => set({ idPattern: e.target.value })} />
      </Field>
      <Field label="拒绝理由" hint="QQ 会把这段话展示给被拒绝的申请人。" htmlFor="jr-reason">
        <Input id="jr-reason" value={value.rejectReason} onChange={(e) => set({ rejectReason: e.target.value })} />
      </Field>
    </div>
  )
}

export function ProactiveForm({ value, onChange }: FormProps<ProactiveSettings>) {
  const set = (patch: Partial<ProactiveSettings>) => onChange({ ...value, ...patch })
  return (
    <div className="grid gap-5">
      <ToggleRow
        label="启用主动回复（全局）"
        hint="群里有人提问且长时间无人应答时谨慎补位。需要收到未 @ 机器人的群消息：QQ 开启「接收群内全部消息」，TG 在 BotFather 关闭 privacy mode。个别群可在「群管理」里单独开关、单独设静默阈值"
        checked={value.enabled}
        onChange={(enabled) => set({ enabled })}
      />
      <div className="grid gap-5 sm:grid-cols-3">
        <Field label="静默阈值（秒）" hint="默认 180 秒无人应答才主动补位。需即时补位可填几秒。">
          <NumberInput value={value.silenceSec} onChange={(n) => set({ silenceSec: n })} />
        </Field>
        <Field label="扫描间隔（秒）" hint="默认 60 秒，最小 1 秒。">
          <NumberInput value={value.scanSec} onChange={(n) => set({ scanSec: n })} />
        </Field>
        <Field label="单次最多补位数" hint="每个群每次扫描最多补位几人，默认 2。">
          <NumberInput value={value.maxPerScan} onChange={(n) => set({ maxPerScan: n })} />
        </Field>
      </div>
    </div>
  )
}

export function TopicsForm({ value, onChange }: FormProps<TopicsSettings>) {
  const set = (patch: Partial<TopicsSettings>) => onChange({ ...value, ...patch })
  return (
    <div className="grid gap-5">
      <ToggleRow
        label="启用问题排行（全局）"
        hint="定期把群友发言与私聊里客户的消息交给模型归到问题主题，在「问题排行」页看大家最常问什么。与产品无关的闲聊不计入。个别群可在「群管理」里单独开关"
        checked={value.enabled}
        onChange={(enabled) => set({ enabled })}
      />
      <div className="grid gap-5 sm:grid-cols-3">
        <Field label="扫描间隔（分钟）" hint="默认 10 分钟">
          <NumberInput value={value.scanMin} onChange={(n) => set({ scanMin: n })} />
        </Field>
        <Field label="单批消息数" hint="每次交给模型归类的条数，默认 50">
          <NumberInput value={value.windowMax} onChange={(n) => set({ windowMax: n })} />
        </Field>
        <Field label="保留天数" hint="归类记录保留多久，默认 90 天">
          <NumberInput value={value.retentionDays} onChange={(n) => set({ retentionDays: n })} />
        </Field>
      </div>
    </div>
  )
}

export function ReflectForm({ value, onChange }: FormProps<ReflectSettings>) {
  const set = (patch: Partial<ReflectSettings>) => onChange({ ...value, ...patch })
  return (
    <div className="grid gap-5">
      <ToggleRow
        label="启用反思沉淀"
        hint="从群主 / 管理员的答复里提炼可复用问答，自动进入知识检索。只在区间内有管理员发言时才调用模型"
        checked={value.enabled}
        onChange={(enabled) => set({ enabled })}
      />
      <div className="grid gap-5 sm:grid-cols-2">
        <Field label="扫描间隔（分钟）" hint="默认 5 分钟。">
          <NumberInput value={value.scanMin} onChange={(n) => set({ scanMin: n })} />
        </Field>
        <Field label="回看窗口（分钟）" hint="默认 120 分钟（2 小时）。群消息时间线只保留这么久（至少 1 天）。">
          <NumberInput value={value.lookbackMin} onChange={(n) => set({ lookbackMin: n })} />
        </Field>
        <Field label="静置阈值（分钟）" hint="默认 10 分钟。消息至少过这么久才参与提炼，等后续追问 / 确认落定。">
          <NumberInput value={value.settleMin} onChange={(n) => set({ settleMin: n })} />
        </Field>
        <Field label="单窗最大消息数" hint="默认 60。">
          <NumberInput value={value.windowMax} onChange={(n) => set({ windowMax: n })} />
        </Field>
        <Field label="整理周期（小时）" hint="默认 1 小时。设 0 关闭自动整理。超 30 条时自动分批整理。">
          <NumberInput value={value.compactHours} onChange={(n) => set({ compactHours: n })} />
        </Field>
        <Field label="整理最少条目" hint="默认 10。">
          <NumberInput value={value.compactMinEntries} onChange={(n) => set({ compactMinEntries: n })} />
        </Field>
        <Field label="自动升格周期（小时）" hint="模型评审高质量沉淀并固化为正式知识库文档。默认 24 小时。设 0 关闭。">
          <NumberInput value={value.promoteHours} onChange={(n) => set({ promoteHours: n })} />
        </Field>
        <Field label="升格最少候选" hint="候选（已入库未升格）达到此数才调用模型。默认 1。">
          <NumberInput value={value.promoteMinEntries} onChange={(n) => set({ promoteMinEntries: n })} />
        </Field>
        <Field label="单轮最多升格" hint="防止一次升格过多。默认 5。">
          <NumberInput value={value.promoteMaxPerRun} onChange={(n) => set({ promoteMaxPerRun: n })} />
        </Field>
      </div>
    </div>
  )
}

type EmbedTest = { latencyMs: number; dims: number; related: number; unrelated: number }

export function EmbeddingForm({ value, onChange }: FormProps<EmbeddingSettings>) {
  const set = (patch: Partial<EmbeddingSettings>) => onChange({ ...value, ...patch })
  const { pending, track } = useDelayedPending()
  const [result, setResult] = useState<EmbedTest | null>(null)
  const test = () =>
    track(async () => {
      setResult(null)
      setResult(await api.post<EmbedTest>("/api/settings/embedding-test", value))
    }).catch(() => {})
  return (
    <div className="grid gap-5">
      <ToggleRow
        label="启用向量检索"
        hint="把知识块转成向量，按语义召回（「怎么充钱」也能找到「充值方法」），与关键词检索融合排序。嵌入服务不可用时自动退回纯关键词"
        checked={value.enabled}
        onChange={(enabled) => set({ enabled })}
      />
      <p className="rounded-lg bg-muted px-3 py-2.5 text-xs leading-relaxed text-muted-foreground">
        缺省对接 docker-compose 里的 <code className="font-mono">embed</code> 服务（text-embeddings-inference + bge-small-zh-v1.5，常驻内存约 200MB）：
        <code className="mt-1 block font-mono">docker compose up -d embed</code>
        也可以填任意 OpenAI 兼容的 <code className="font-mono">/embeddings</code> 接口。开启后已有文档在后台自动向量化，进度见知识库页。
      </p>
      <Field label="Base URL" hint="填到 /v1 为止；知微跑在容器里时用服务名，例如 http://embed/v1" htmlFor="emb-base">
        <Input id="emb-base" className="font-mono" value={value.baseUrl} onChange={(e) => set({ baseUrl: e.target.value })} placeholder="http://127.0.0.1:8081/v1" />
      </Field>
      <div className="grid gap-5 sm:grid-cols-2">
        <Field label="模型" hint="改模型后全部知识块会重新向量化" htmlFor="emb-model">
          <Input id="emb-model" className="font-mono" value={value.model} onChange={(e) => set({ model: e.target.value })} placeholder="BAAI/bge-small-zh-v1.5" />
        </Field>
        <Field label="API Key" hint="本地服务留空" htmlFor="emb-key">
          <Input id="emb-key" className="font-mono" type="password" autoComplete="off" value={value.apiKey} onChange={(e) => set({ apiKey: e.target.value })} />
        </Field>
      </div>
      <Field label="相似度下限" hint="余弦相似度低于它的块不参与排序，0~1。默认 0.5；召回太杂调高，同义问法召不回调低">
        <NumberInput step="0.05" value={value.minScore} onChange={(n) => set({ minScore: n })} />
      </Field>
      <div className="flex flex-wrap items-center gap-3">
        <Button type="button" variant="secondary" onClick={test} disabled={pending || !value.baseUrl}>
          <PlugZapIcon />
          {pending ? "测试中…" : "测试连接"}
        </Button>
        {result && (
          <span className="inline-flex flex-wrap items-center gap-1.5 text-[13px] text-ok">
            <CheckCircle2Icon className="size-4" />
            连接正常 · <span className="font-mono tabular-nums">{result.latencyMs}ms · {result.dims} 维</span>
            <span className="text-muted-foreground">
              「怎么充值」与「充值方法」<span className="font-mono tabular-nums">{result.related.toFixed(2)}</span>，与「API Key」
              <span className="font-mono tabular-nums">{result.unrelated.toFixed(2)}</span>
            </span>
          </span>
        )}
      </div>
    </div>
  )
}

type CatalogTest = { latencyMs: number; models: number; families: string[]; sample: string }

export function ModelCatalogForm({ value, onChange }: FormProps<ModelCatalogSettings>) {
  const set = (patch: Partial<ModelCatalogSettings>) => onChange({ ...value, ...patch })
  const { pending, track } = useDelayedPending()
  const [result, setResult] = useState<CatalogTest | null>(null)
  const test = () =>
    track(async () => {
      setResult(null)
      setResult(await api.post<CatalogTest>("/api/settings/catalog-test", { url: value.url }))
    }).catch(() => {})
  return (
    <div className="grid gap-5">
      <ToggleRow
        label="启用实时模型目录"
        hint="给机器人一个 model_catalog 工具：客户问价格、有哪些模型、某模型支不支持某能力、现在能不能用时，直接查官网实时目录作答，不再依赖知识库里写死的价格"
        checked={value.enabled}
        onChange={(enabled) => set({ enabled })}
      />
      <p className="rounded-lg bg-muted px-3 py-2.5 text-xs leading-relaxed text-muted-foreground">
        接口需返回模型目录 JSON，格式见仓库里的 <code className="font-mono">server/internal/catalog/testdata/models.json</code>。机器人拿到的是筛选后的几行摘要，不是整包 JSON；报价规则（价格已是终价不再乘倍率、币种逐模型区分）已写进工具说明。
      </p>
      <Field label="目录接口地址" htmlFor="cat-url">
        <Input id="cat-url" className="font-mono" value={value.url} onChange={(e) => set({ url: e.target.value })} placeholder="https://example.com/api/public/models" />
      </Field>
      <Field label="缓存时间（分钟）" hint="1~60。缓存期内的提问不再请求接口；接口临时故障时继续用 1 小时内的旧数据">
        <NumberInput value={value.cacheMin} onChange={(n) => set({ cacheMin: Math.round(n) })} />
      </Field>
      <div className="flex flex-wrap items-center gap-3">
        <Button type="button" variant="secondary" onClick={test} disabled={pending || !value.url}>
          <PlugZapIcon />
          {pending ? "测试中…" : "测试连接"}
        </Button>
        {result && (
          <span className="inline-flex flex-wrap items-center gap-1.5 text-[13px] text-ok">
            <CheckCircle2Icon className="size-4" />
            连接正常 · <span className="font-mono tabular-nums">{result.latencyMs}ms · {result.models} 个模型</span>
            <span className="text-muted-foreground">厂商 {result.families.join("、")}</span>
          </span>
        )}
      </div>
      {result?.sample && (
        <Field label="示例报价（机器人看到的格式）">
          <pre className="overflow-x-auto rounded-lg bg-muted px-3 py-2.5 font-mono text-[12px] whitespace-pre-wrap">{result.sample}</pre>
        </Field>
      )}
    </div>
  )
}
