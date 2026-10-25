// 与 server/internal 的 JSON 结构一一对应（字段名 camelCase）。

export interface Brand {
  name: string
  description: string
  supportUrl: string
}

export interface LLMSettings {
  protocol: "openai" | "anthropic"
  baseUrl: string
  apiKey: string
  model: string
  temperature: number
  maxTokens: number
  timeoutSec: number
}

export interface AgentSettings {
  extraPrompt: string
  historyLimit: number
  idleResetMin: number
  maxToolRounds: number
  runTimeoutSec: number
  maxReplyChars: number
  kbPrefetch: boolean
  kbTopK: number
  handoffTimeoutMin: number
  allowAllChats: boolean
  chats: string[]
  ackEnabled: boolean
  replyMarkdown: boolean
  vision: boolean
}

export interface WelcomeSettings {
  enabled: boolean
  formatQq: "markdown" | "text"
  messageQq: string
  messageTg: string
}

export interface JoinReviewSettings {
  enabled: boolean
  checkUrl: string
  rejectReason: string
  idPattern: string
}

export interface ProactiveSettings {
  enabled: boolean
  silenceSec: number
  scanSec: number
  maxPerScan: number
}

export interface ReflectSettings {
  enabled: boolean
  scanMin: number
  lookbackMin: number
  settleMin: number
  windowMax: number
  compactHours: number
  compactMinEntries: number
  promoteHours: number
  promoteMinEntries: number
  promoteMaxPerRun: number
}

export interface EmbeddingSettings {
  enabled: boolean
  baseUrl: string
  apiKey: string
  model: string
  minScore: number
}

export interface ModelCatalogSettings {
  enabled: boolean
  url: string
  cacheMin: number
}

export interface TopicsSettings {
  enabled: boolean
  scanMin: number
  windowMax: number
  retentionDays: number
}

/** 单群策略；null = 跟随全局 */
export interface GroupPolicy {
  chat: string
  note: string
  proactive: boolean | null
  silenceSec: number | null
  welcome: boolean | null
  joinReview: boolean | null
  reflect: boolean | null
  topics: boolean | null
}

export interface QQBotSettings {
  enabled: boolean
  appId: string
  appSecret: string
  mode: "websocket" | "webhook"
  sandbox: boolean
  group: boolean
  c2c: boolean
  guild: boolean
  guildDm: boolean
  groupAll: boolean
  atUser: boolean
}

export interface TelegramSettings {
  enabled: boolean
  botToken: string
  apiBase: string
  private: boolean
}

export interface WeComSettings {
  enabled: boolean
  corpId: string
  agentId: string
  secret: string
  token: string
  encodingAesKey: string
}

export interface AllSettings {
  setup: { completed: boolean }
  brand: Brand
  llm: LLMSettings
  agent: AgentSettings
  qqbot: QQBotSettings
  telegram: TelegramSettings
  wecom: WeComSettings
  welcome: WelcomeSettings
  joinReview: JoinReviewSettings
  proactive: ProactiveSettings
  reflect: ReflectSettings
  embedding: EmbeddingSettings
  modelCatalog: ModelCatalogSettings
  topics: TopicsSettings
  groups: { policies: GroupPolicy[] }
}

export interface ChannelStatus {
  channel: string
  enabled: boolean
  connected: boolean
  detail: string
  lastError: string
}

export interface Overview {
  stats: {
    conversations: number
    openHandoffs: number
    todayInbound: number
    todayReplies: number
    todayLlmCalls: number
    todayLlmErrors: number
    todayInputTokens: number
    todayOutputTokens: number
    todayCacheReadTokens: number
    kbDocuments: number
  }
  channels: ChannelStatus[]
  llm: { protocol: string; model: string; error: string }
  kbChunks: number
  brand: Brand
}

export interface Conversation {
  id: number
  channel: string
  chatType: string
  chatId: string
  userId: string
  userName: string
  title: string
  status: "active" | "handoff"
  contextSince: string
  lastMessageAt: string
  createdAt: string
  messageCount: number
}

export interface Message {
  id: number
  conversationId: number
  direction: "in" | "out" | "human"
  senderId: string
  senderName: string
  content: string
  createdAt: string
  images?: { id: number; width: number; height: number }[]
}

export interface Handoff {
  id: number
  conversationId: number
  reason: string
  summary: string
  status: "open" | "resolved"
  createdAt: string
  resolvedAt: string | null
  channel: string
  chatId: string
  userId: string
  userName: string
  title: string
}

export interface KBDocument {
  id: number
  /** 所在目录，「/」分隔，空串 = 根目录 */
  path: string
  title: string
  content?: string
  enabled: boolean
  chunkCount: number
  createdAt: string
  updatedAt: string
}

export interface KBHit {
  chunkId: number
  documentId: number
  docPath?: string
  docTitle: string
  heading: string
  content: string
  score: number
  /** 关键词（BM25）得分，未命中为 0 */
  keyword: number
  /** 向量余弦相似度，未命中为 0 */
  vector: number
  /** 命中的是反思沉淀条目时非 0（此时 documentId 为 0） */
  reflectionId?: number
}

export interface EmbedStatus {
  enabled: boolean
  model: string
  total: number
  embedded: number
  lastError?: string
  lastErrorAt?: string
}

export interface KBImportResult {
  created: number
  updated: number
  skipped: number
  ignored: { name: string; reason: string }[]
}

export interface Skill {
  id: number
  name: string
  description: string
  enabled: boolean
  useCount: number
  lastUsedAt: string | null
  fileCount: number
  size: number
  createdAt: string
  updatedAt: string
}

export interface SkillDetail extends Skill {
  content: string
  files: { path: string; content: string }[]
}

export interface BuiltinTool {
  name: string
  description: string
  proactive: boolean
  onlyWithSkills: boolean
  /** 由该设置分区的 enabled 控制 */
  setting?: "modelCatalog"
}

export interface SkillImportResult extends KBImportResult {
  names: string[]
}

export const CHANNEL_LABEL: Record<string, string> = {
  qqbot: "QQ 机器人",
  telegram: "Telegram",
  wecom: "企业微信",
  playground: "体验台",
}

export const CHAT_TYPE_LABEL: Record<string, string> = {
  group: "群聊",
  private: "私聊",
  guild: "频道",
  guild_dm: "频道私信",
}

/** 群扫描进度（主动回复 / 反思共用）。lagSec 为 null 表示还没扫过 */
export interface ChatProgress {
  channel: string
  chatType: string
  chatId: string
  title: string
  messages: number
  lastAt: string
  allowed: boolean
  /** 该功能对这个群开着（按群设置覆盖全局） */
  enabled: boolean
  cursor: string | null
  scanAt: string | null
  lagSec: number | null
  replies: number
  sediment: number
}

export interface ProactiveReply {
  id: number
  channel: string
  chatId: string
  userId: string
  userName: string
  question: string
  answer: string
  quality: "" | "ok" | "bad"
  createdAt: string
}

export interface ProactiveData {
  config: ProactiveSettings
  anyEnabled: boolean
  qqGroupAll: boolean
  chats: ChatProgress[]
  replies: ProactiveReply[]
}

export interface JoinRequestRecord {
  id: number
  channel: string
  chatId: string
  chatTitle: string
  userId: string
  userName: string
  answer: string
  extractedId: string
  decision: "approved" | "declined" | "pending" | "auto" | "failed"
  detail: string
  createdAt: string
}

export interface Reflection {
  id: number
  channel: string
  chatId: string
  question: string
  answer: string
  faq: string
  status: "approved" | "rejected" | "promoted"
  compacted: boolean
  promotedDocId: number | null
  createdAt: string
  updatedAt: string
}

export interface Compaction {
  id: number
  beforeCount: number
  afterCount: number
  before?: { id: number; faq: string }[]
  after?: string[]
  createdAt: string
}

export interface ReflectionData {
  config: ReflectSettings
  anyEnabled: boolean
  qqGroupAll: boolean
  counts: Record<"approved" | "rejected" | "promoted", number>
  chats: ChatProgress[]
  entries: Reflection[]
  compactions: Compaction[]
  compactAt?: string
  promoteAt?: string
}

/** 秒数 → 「3 分 20 秒」类的人读时长 */
export function formatDuration(sec: number) {
  if (sec < 60) return `${sec} 秒`
  if (sec < 3600) return `${Math.floor(sec / 60)} 分${sec % 60 ? ` ${sec % 60} 秒` : ""}`
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  return `${h} 小时${m ? ` ${m} 分` : ""}`
}

// ── 用量统计 ──

export interface UsageTotals {
  calls: number
  errors: number
  inputTokens: number
  outputTokens: number
  cacheReadTokens: number
  cacheWriteTokens: number
  images: number
  avgLatencyMs: number
}

export interface UsageData {
  from: string
  days: number
  usage: {
    totals: UsageTotals
    days: (UsageTotals & { day: string })[]
    byPurpose: (UsageTotals & { key: string })[]
    byModel: (UsageTotals & { key: string })[]
    tools: { tool: string; calls: number; runs: number }[]
    errors: { at: string; purpose: string; model: string; error: string; latencyMs: number }[]
  }
}

export const PURPOSE_LABEL: Record<string, string> = {
  reply: "客服回复",
  proactive: "主动回复",
  judge: "可答性判断",
  reflect: "反思提炼",
  compact: "反思整理",
  promote: "升格评审",
  topic: "问题归类",
}

/** 浏览器所在时区：统计按它切日（库的时区通常是 UTC） */
export function browserTZ() {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC"
}

// ── 运行日志 ──

export interface LogEntry {
  seq: number
  time: string
  level: "DEBUG" | "INFO" | "WARN" | "ERROR"
  msg: string
  attrs: { key: string; value: string }[]
}

// ── 问题排行 ──

export interface TopicRank {
  id: number
  title: string
  count: number
  prevCount: number
  users: number
  chats: number
  lastAt: string
  samples: string[]
}

export interface TopicOccurrence {
  id: number
  channel: string
  chatType: string
  chatId: string
  chatTitle: string
  userId: string
  userName: string
  text: string
  askedAt: string
}

export interface TopicsData {
  config: TopicsSettings
  anyEnabled: boolean
  qqGroupAll: boolean
  from: string
  days: number
  ranking: TopicRank[]
  chats: ChatProgress[]
  privateCursor?: string
}

// ── 群管理 ──

export interface GroupEffective {
  proactive: boolean
  silenceSec: number
  welcome: boolean
  joinReview: boolean
  reflect: boolean
  topics: boolean
}

export interface GroupRow {
  channel: string
  chatType: string
  chatId: string
  title: string
  messages: number
  lastAt: string | null
  allowed: boolean
  policy: GroupPolicy | null
  effective: GroupEffective
}

export interface GroupsData {
  list: GroupRow[]
  global: GroupEffective
  allowAllChats: boolean
  qqGroupAll: boolean
}
