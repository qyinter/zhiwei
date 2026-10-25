"use client"

import { MessageCircleReplyIcon, ThumbsDownIcon, ThumbsUpIcon } from "lucide-react"
import Link from "next/link"
import { useCallback, useEffect, useState } from "react"
import { toast } from "sonner"

import { ChatProgressTable } from "@/components/chat-progress"
import { EmptyState, formatTime, Kicker, PageHeader } from "@/components/common"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { api } from "@/lib/api"
import { type AllSettings, CHANNEL_LABEL, formatDuration, type ProactiveData, type ProactiveReply } from "@/lib/types"
import { useDelayedLoading, useDelayedPending } from "@/lib/use-delayed-pending"
import { cn } from "@/lib/utils"

function Stat({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="rounded-xl border bg-card px-4 py-3">
      <Kicker>{label}</Kicker>
      <div className="mt-1 font-mono text-[15px] font-medium tabular-nums">{value}</div>
    </div>
  )
}

function ReplyCard({ r, onMark }: { r: ProactiveReply; onMark: (q: ProactiveReply["quality"]) => void }) {
  return (
    <Card className="py-0">
      <CardContent className="grid gap-2.5 p-5">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-medium">{r.userName || <span className="font-mono text-xs">{r.userId.slice(0, 12)}</span>}</span>
          <Badge variant="neutral">{CHANNEL_LABEL[r.channel] ?? r.channel}</Badge>
          {r.quality === "ok" && <Badge variant="ok">恰当</Badge>}
          {r.quality === "bad" && <Badge variant="crit">不当</Badge>}
          <span className="ml-auto font-mono text-xs text-muted-foreground tabular-nums">{formatTime(r.createdAt)}</span>
        </div>
        <p className="text-[13.5px] whitespace-pre-wrap text-muted-foreground">问：{r.question}</p>
        <p className="text-[13.5px] whitespace-pre-wrap">答：{r.answer}</p>
        <div className="flex gap-2">
          <Button size="sm" variant={r.quality === "ok" ? "secondary" : "outline"} onClick={() => onMark(r.quality === "ok" ? "" : "ok")}>
            <ThumbsUpIcon />
            恰当
          </Button>
          <Button size="sm" variant={r.quality === "bad" ? "secondary" : "outline"} onClick={() => onMark(r.quality === "bad" ? "" : "bad")}>
            <ThumbsDownIcon />
            不当
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

export default function ProactivePage() {
  const [data, setData] = useState<ProactiveData | null>(null)
  const { loading, track } = useDelayedLoading()
  const toggle = useDelayedPending()

  const load = useCallback(() => track(() => api.get<ProactiveData>("/api/proactive").then(setData)).catch(() => {}), [track])

  useEffect(() => {
    load()
    const t = setInterval(load, 15000)
    return () => clearInterval(t)
  }, [load])

  const setEnabled = (enabled: boolean) =>
    toggle
      .track(async () => {
        const cfg = await api.get<AllSettings>("/api/settings")
        await api.put("/api/settings", { ...cfg, proactive: { ...cfg.proactive, enabled } })
        toast.success(enabled ? "已启用主动回复" : "已关闭主动回复")
        await load()
      })
      .catch(() => {})

  const mark = (r: ProactiveReply, quality: ProactiveReply["quality"]) =>
    api
      .patch(`/api/proactive/${r.id}`, { quality })
      .then(() => setData((d) => d && { ...d, replies: d.replies.map((x) => (x.id === r.id ? { ...x, quality } : x)) }))
      .catch(() => {})

  const cfg = data?.config
  return (
    <>
      <PageHeader
        title="主动回复"
        description="生效群里有人提问且长时间无人应答时，机器人会谨慎补位：先判断是否值得回答，再只依据知识库作答，没把握就不说话。"
        actions={
          cfg && (
            <label className="flex items-center gap-2.5 text-sm">
              全局开关
              <Switch checked={cfg.enabled} disabled={toggle.pending} onCheckedChange={setEnabled} />
            </label>
          )
        }
      />
      {data && !data.qqGroupAll && (
        <div className="mb-5 rounded-xl border border-warn/40 bg-warn-soft px-4 py-3 text-[13px]">
          QQ 未开启「接收群内全部消息」，机器人只收得到 @ 它的消息，主动回复不会在 QQ 群生效。到{" "}
          <Link className="text-primary underline-offset-4 hover:underline" href="/admin/settings">
            系统设置 → QQ 机器人
          </Link>{" "}
          开启（需先在开放平台开通）。
        </div>
      )}
      {loading && !data ? (
        <div className="grid gap-3">
          <Skeleton className="h-20 rounded-2xl" />
          <Skeleton className="h-48 rounded-2xl" />
        </div>
      ) : (
        cfg && (
          <div className="grid gap-6">
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <Stat
                label="状态"
                value={
                  <span className={cn(data.anyEnabled ? "text-ok" : "text-muted-foreground")}>{cfg.enabled ? "已启用" : data.anyEnabled ? "部分群开启" : "已关闭"}</span>
                }
              />
              <Stat label="静默阈值" value={formatDuration(cfg.silenceSec)} />
              <Stat label="扫描周期" value={formatDuration(cfg.scanSec)} />
              <Stat label="单次上限" value={`${cfg.maxPerScan} 人 / 群`} />
            </div>

            <section className="grid gap-3">
              <div>
                <h2 className="text-[15px] font-semibold">每群进度</h2>
                <p className="text-[13px] text-muted-foreground">各群的扫描进度与补位次数。群第一次被扫描时从当前位置开始，上线前的积压不补。</p>
              </div>
              <ChatProgressTable chats={data.chats} extra={{ label: "主动回复数", value: (c) => c.replies }} />
            </section>

            <section className="grid gap-3">
              <div>
                <h2 className="text-[15px] font-semibold">最近主动回复（{data.replies.length}）</h2>
                <p className="text-[13px] text-muted-foreground">可标记回复是否恰当，便于后续优化。</p>
              </div>
              {data.replies.length === 0 ? (
                <Card className="py-0">
                  <EmptyState icon={<MessageCircleReplyIcon className="size-5" />} title="还没有主动回复" />
                </Card>
              ) : (
                <div className="grid gap-3">
                  {data.replies.map((r) => (
                    <ReplyCard key={r.id} r={r} onMark={(q) => mark(r, q)} />
                  ))}
                </div>
              )}
            </section>
          </div>
        )
      )}
    </>
  )
}
