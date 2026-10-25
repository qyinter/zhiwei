"use client"

// 入群欢迎编辑器：QQ Markdown 样式预设 + 实时预览 + 发送到真实群测试。
import { SendIcon } from "lucide-react"
import { Fragment, useEffect, useState } from "react"
import { toast } from "sonner"

import { Field, Kicker } from "@/components/common"
import { ToggleRow } from "@/components/settings-forms"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { api } from "@/lib/api"
import { CHANNEL_LABEL, type WelcomeSettings } from "@/lib/types"
import { useDelayedPending } from "@/lib/use-delayed-pending"
import { cn } from "@/lib/utils"

/**
 * 样式预设。QQ 客户端把列表的圆点 / 序号单独排成一行（2026-10-24 实测），
 * 所以预设一律不用 Markdown 列表，改为「每行一条 + emoji 开头」。卡片与 server 的 DefaultWelcomeQQ 必须一致。
 */
export const WELCOME_PRESETS: { key: string; label: string; hint: string; text: string }[] = [
  {
    key: "card",
    label: "卡片",
    hint: "大标题 + 引用块 + 新人三步走，信息完整（缺省）",
    text: `# 👋 欢迎加入 {brand}
{at} 你好，很高兴在这里见到你～

> 有问题直接 @我，我会先查资料再回答你
> 需要人工处理时，@我 并发送「人工」

***
**🧭 新人三步走**

1️⃣ 先看一眼群公告和群规
2️⃣ 使用问题直接 @我 提问
3️⃣ 账户、订单类问题找人工客服`,
  },
  {
    key: "nav",
    label: "入口导航",
    hint: "常用链接一行一个、动作写在链接上（点我进入 / 查看价格…），把网址换成你们自己的",
    text: `# 👋 欢迎加入 {brand}
{at} 你好，很高兴在这里见到你～

> 有问题直接 @我，我会先查资料再回答你

***
**🧭 常用入口**

🌐 官网 👉 [点我进入](https://example.com)
💰 价格说明 👉 [查看价格](https://example.com/pricing)
📖 使用教程 👉 [查看教程](https://example.com/docs)

***
**🙋 人工客服**

账户、订单、退款等问题请加人工客服 QQ：**10000**`,
  },
  {
    key: "simple",
    label: "简洁",
    hint: "两行搞定，刷屏感最弱，适合入群频繁的大群",
    text: `🎉 欢迎 {at} 加入 **{brand}**！
有问题直接 @我 提问；需要人工请 @我 并发送「人工」。`,
  },
  {
    key: "ask",
    label: "快捷提问",
    hint: "列出常见问题，点一下填入输入框（参数指令），把高频问题改成你们自己的",
    text: `## 🎉 欢迎加入 {brand}
{at} 我是这里的智能客服，这些问题可以直接问我：

👉 {ask:怎么充值}
👉 {ask:API Key 在哪里创建}
👉 {ask:调用报错怎么排查}

> 点一下问题会填进输入框，@我 发送即可
> 找不到答案？@我 并发送「人工」`,
  },
  {
    key: "rules",
    label: "群规公告",
    hint: "先讲群规再讲怎么提问，适合需要秩序的付费 / 官方群",
    text: `# 欢迎来到 {brand} 👋
{at}，入群请先花 1 分钟看看：

**📌 群规**

1️⃣ 友善交流，禁止广告引流
2️⃣ 提问前先看看群公告
3️⃣ 不要在群里发密钥、订单号等隐私

**💬 怎么找我**

有问题直接 @我；账户、订单类问题 @我 并发送「人工」

***
祝你使用愉快 ✨`,
  },
]

// ── 预览：按 QQ Markdown 支持的子集渲染（标题 / 粗斜体 / 删除线 / 链接 / 引用 / 列表 / 分割线 / @ / 参数指令）──

const INLINE = /(<qqbot-at-user[^>]*\/>|<qqbot-cmd-input[^>]*\/>|\*\*\*[^*]+\*\*\*|\*\*[^*]+\*\*|__[^_]+__|~~[^~]+~~|\*[^*\s][^*]*\*|_[^_\s][^_]*_|\[[^\]]+\]\([^)]+\))/g

function attr(tag: string, name: string) {
  const m = tag.match(new RegExp(`${name}="([^"]*)"`))
  if (!m) return ""
  try {
    return decodeURIComponent(m[1])
  } catch {
    return m[1]
  }
}

function Inline({ text, atName }: { text: string; atName: string }) {
  const parts = text.split(INLINE)
  return (
    <>
      {parts.map((p, i) => {
        if (i % 2 === 0) return <Fragment key={i}>{p}</Fragment>
        if (p.startsWith("<qqbot-at-user")) return <span key={i} className="rounded px-0.5 font-medium text-[#0099ff]">@{atName}</span>
        if (p.startsWith("<qqbot-cmd-input"))
          return (
            <span key={i} className="mx-0.5 inline-block rounded-md border border-[#0099ff]/40 bg-[#0099ff]/8 px-1.5 text-[#0099ff]">
              /{attr(p, "show") || attr(p, "text")}
            </span>
          )
        if (p.startsWith("***")) return <strong key={i}><em>{p.slice(3, -3)}</em></strong>
        if (p.startsWith("**") || p.startsWith("__")) return <strong key={i}>{p.slice(2, -2)}</strong>
        if (p.startsWith("~~")) return <del key={i}>{p.slice(2, -2)}</del>
        if (p.startsWith("[")) {
          const m = p.match(/^\[([^\]]+)\]\(([^)]+)\)$/)
          return <span key={i} className="text-[#0099ff]">{m?.[1] ?? p}</span>
        }
        return <em key={i}>{p.slice(1, -1)}</em>
      })}
    </>
  )
}

export function MarkdownPreview({ source, atName }: { source: string; atName: string }) {
  const lines = source.replace(/\r\n/g, "\n").split("\n")
  const blocks: React.ReactNode[] = []
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    const t = line.trim()
    if (t === "") {
      blocks.push(<div key={i} className="h-2" />)
    } else if (/^(\*\*\*|---|___)$/.test(t)) {
      blocks.push(<hr key={i} className="my-2 border-black/10" />)
    } else if (t.startsWith("## ")) {
      blocks.push(<div key={i} className="text-[16px] font-semibold"><Inline text={t.slice(3)} atName={atName} /></div>)
    } else if (t.startsWith("# ")) {
      blocks.push(<div key={i} className="text-[19px] font-bold"><Inline text={t.slice(2)} atName={atName} /></div>)
    } else if (t.startsWith(">")) {
      const quote: string[] = []
      while (i < lines.length && lines[i].trim().startsWith(">")) quote.push(lines[i++].trim().replace(/^>\s?/, ""))
      i--
      blocks.push(
        <div key={i} className="my-1 border-l-[3px] border-black/15 pl-2.5 text-black/60">
          {quote.map((q, j) => <div key={j}><Inline text={q} atName={atName} /></div>)}
        </div>
      )
    } else if (/^(\d+\.|-|\*)\s/.test(t)) {
      const m = t.match(/^(\d+\.|-|\*)\s(.*)$/)!
      blocks.push(
        <div key={i} className="flex gap-1.5 pl-1">
          <span className="shrink-0 text-black/50">{/\d/.test(m[1]) ? m[1] : "•"}</span>
          <span><Inline text={m[2]} atName={atName} /></span>
        </div>
      )
    } else {
      blocks.push(<div key={i}><Inline text={line} atName={atName} /></div>)
    }
  }
  return <div className="text-[14px] leading-relaxed break-words text-[#1a1a1a]">{blocks}</div>
}

/** 前端预览用的占位替换（与 server renderWelcome 规则一致） */
function renderPreview(tpl: string, brand: string, markdown: boolean) {
  return tpl
    .replace(/\{ask:([^{}]+)\}/g, (_, q: string) => (markdown ? `<qqbot-cmd-input text="${encodeURIComponent(q.trim())}" show="${encodeURIComponent(q.trim())}" />` : `「${q.trim()}」`))
    .replaceAll("{at}", markdown ? '<qqbot-at-user id="preview" />' : "新朋友")
    .replaceAll("{name}", "新朋友")
    .replaceAll("{brand}", brand || "本群")
}

interface GroupChat {
  channel: string
  chatId: string
  title: string
}

function TestSend({ value }: { value: WelcomeSettings }) {
  const [groups, setGroups] = useState<GroupChat[]>([])
  const [target, setTarget] = useState("")
  const { pending, track } = useDelayedPending()
  useEffect(() => {
    api
      .get<{ list: GroupChat[] }>("/api/groups")
      .then((r) => {
        setGroups(r.list)
        if (r.list[0]) setTarget(`${r.list[0].channel}|${r.list[0].chatId}`)
      })
      .catch(() => {})
  }, [])
  const send = () =>
    track(async () => {
      const [channel, chatId] = target.split("|")
      const res = await api.post<{ at: string }>("/api/welcome/test", { channel, chatId, welcome: value })
      toast.success(res.at ? `已发送，@ 了最近发言的 ${res.at}` : "已发送")
    }).catch(() => {})
  return (
    <div className="flex flex-wrap items-end gap-2">
      <Field label="发送测试" hint="按当前草稿发到所选群（不用先保存），@ 该群最近发过言的人；占用一条主动消息额度" className="min-w-64 flex-1">
        <Select value={target} onValueChange={setTarget} disabled={groups.length === 0}>
          <SelectTrigger className="w-full">
            <SelectValue placeholder="还没有收到过群消息" />
          </SelectTrigger>
          <SelectContent>
            {groups.map((g) => (
              <SelectItem key={`${g.channel}|${g.chatId}`} value={`${g.channel}|${g.chatId}`}>
                {CHANNEL_LABEL[g.channel] ?? g.channel} · {g.title || g.chatId.slice(0, 12)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      <Button variant="outline" onClick={send} disabled={pending || !target} className="mb-[22px]">
        <SendIcon />
        {pending ? "发送中…" : "发送测试"}
      </Button>
    </div>
  )
}

export function WelcomeForm({ value, onChange, brand }: { value: WelcomeSettings; onChange: (v: WelcomeSettings) => void; brand: string }) {
  const set = (patch: Partial<WelcomeSettings>) => onChange({ ...value, ...patch })
  const markdown = value.formatQq === "markdown"
  // 模板与某个预设逐字一致才算「在用预设」；否则是自定义，套用预设前先确认，免得覆盖改好的内容
  const activePreset = WELCOME_PRESETS.find((p) => p.text === value.messageQq)
  const [pending, setPending] = useState<(typeof WELCOME_PRESETS)[number] | null>(null)
  return (
    <div className="grid gap-5">
      <ToggleRow
        label="开启入群欢迎"
        hint="新成员加入生效群时自动发送欢迎语。QQ 的入群事件不支持被动回复，欢迎语走主动消息（单群每天 1000 条额度）；QQ 需在开放平台开通群成员事件权限"
        checked={value.enabled}
        onChange={(enabled) => set({ enabled })}
      />
      <Field label="QQ 欢迎语格式" hint={markdown ? "自定义 Markdown 消息（群聊已对所有机器人开放）：支持标题、粗体、引用、列表、分割线、@ 与快捷提问" : "纯文本：{at} 退化为昵称，Markdown 符号原样显示"}>
        <Select value={value.formatQq} onValueChange={(v) => set({ formatQq: v as WelcomeSettings["formatQq"] })}>
          <SelectTrigger className="w-full sm:w-64">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="markdown">Markdown（推荐）</SelectItem>
            <SelectItem value="text">纯文本</SelectItem>
          </SelectContent>
        </Select>
      </Field>
      {markdown && (
        <div className="grid gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <Kicker>样式预设（点击替换下方模板）</Kicker>
            {!activePreset && value.messageQq.trim() !== "" && (
              <span className="rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">当前：自定义模板</span>
            )}
          </div>
          <div className="grid gap-2 sm:grid-cols-2">
            {WELCOME_PRESETS.map((p) => (
              <button
                key={p.key}
                type="button"
                onClick={() => (activePreset || value.messageQq.trim() === "" ? set({ messageQq: p.text }) : setPending(p))}
                className={cn(
                  "rounded-xl border px-3.5 py-2.5 text-left transition-colors hover:border-primary/50 hover:bg-accent-soft",
                  value.messageQq === p.text && "border-primary bg-accent-soft"
                )}
              >
                <div className="text-[13.5px] font-medium">{p.label}</div>
                <div className="text-xs text-muted-foreground">{p.hint}</div>
              </button>
            ))}
          </div>
        </div>
      )}
      <div className="grid gap-4 lg:grid-cols-2">
        <Field label="QQ 欢迎语" hint="占位：{at} @新成员（QQ 会显示对方昵称，推荐）· {brand} 品牌名 · {ask:问题} 快捷提问 · {name} 昵称（QQ 入群事件不带昵称，多数时候是「新朋友」，不建议和 {at} 同时用）">
          <Textarea rows={14} className="font-mono text-[13px]" value={value.messageQq} onChange={(e) => set({ messageQq: e.target.value })} />
          {(value.messageQq.includes("example.com") || value.messageQq.includes("**10000**")) && (
            <p className="rounded-lg bg-crit-soft px-3 py-2 text-xs text-crit">模板里还有预设的示例内容（example.com 网址 / QQ 10000），换成你们自己的再保存；开启入群欢迎时不允许原样保存。</p>
          )}
        </Field>
        <div className="flex flex-col gap-1.75">
          <span className="text-[13px] font-medium text-foreground/80">预览（近似 QQ 气泡效果）</span>
          <div className="rounded-2xl bg-[#f1f2f4] p-4 dark:bg-[#2a2c31]">
            <div className="max-w-[360px] rounded-2xl rounded-tl-sm bg-white px-4 py-3 shadow-sm">
              {markdown ? (
                <MarkdownPreview source={renderPreview(value.messageQq, brand, true)} atName="新朋友" />
              ) : (
                <div className="text-[14px] whitespace-pre-wrap text-[#1a1a1a]">{renderPreview(value.messageQq, brand, false)}</div>
              )}
            </div>
          </div>
        </div>
      </div>
      <TestSend value={value} />
      <AlertDialog open={pending !== null} onOpenChange={(open) => !open && setPending(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>替换为「{pending?.label}」样式？</AlertDialogTitle>
            <AlertDialogDescription>当前是自定义模板，替换后改动会丢失（保存设置前刷新页面可恢复已保存的版本）。</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (pending) set({ messageQq: pending.text })
                setPending(null)
              }}
            >
              替换
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      <Field label="Telegram 欢迎语" hint="TG 群用户以海外为主，建议英文；按纯文本发送。{name} 替换为新成员昵称。">
        <Textarea rows={3} value={value.messageTg} onChange={(e) => set({ messageTg: e.target.value })} />
      </Field>
    </div>
  )
}
