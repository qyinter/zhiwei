"use client"

import { ListOrderedIcon, MergeIcon, MoreHorizontalIcon, PencilIcon, PlayIcon, Trash2Icon, TrendingDownIcon, TrendingUpIcon } from "lucide-react"
import Link from "next/link"
import { useCallback, useEffect, useState } from "react"
import { toast } from "sonner"

import { ChatProgressTable } from "@/components/chat-progress"
import { EmptyState, Field, formatNumber, formatTime, PageHeader, Pager } from "@/components/common"
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
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { SegmentedTabs } from "@/components/ui/segmented-tabs"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"
import { api, type Paged } from "@/lib/api"
import { browserTZ, CHANNEL_LABEL, CHAT_TYPE_LABEL, type TopicOccurrence, type TopicRank, type TopicsData } from "@/lib/types"
import { useDelayedLoading, useDelayedPending } from "@/lib/use-delayed-pending"

type Range = "1" | "7" | "30"

const RANGES: { key: Range; label: string }[] = [
  { key: "1", label: "今日" },
  { key: "7", label: "近 7 天" },
  { key: "30", label: "近 30 天" },
]

const tz = () => encodeURIComponent(browserTZ())

/** 与上一个等长时间窗相比的变化 */
function Trend({ t }: { t: TopicRank }) {
  if (t.prevCount === 0) return <Badge variant="accent">新</Badge>
  const diff = t.count - t.prevCount
  if (diff === 0) return <span className="text-xs text-muted-foreground">持平</span>
  const Icon = diff > 0 ? TrendingUpIcon : TrendingDownIcon
  return (
    <span className="inline-flex items-center gap-1 font-mono text-xs text-muted-foreground tabular-nums">
      <Icon className="size-3.5" />
      {diff > 0 ? "+" : ""}
      {diff}
    </span>
  )
}

function OccurrencesSheet({ topic, range, onClose }: { topic: TopicRank | null; range: Range; onClose: () => void }) {
  const [page, setPage] = useState(1)
  const [data, setData] = useState<Paged<TopicOccurrence> | null>(null)
  useEffect(() => {
    if (!topic) return
    api
      .get<Paged<TopicOccurrence>>(`/api/topics/${topic.id}/occurrences?days=${range}&tz=${tz()}&page=${page}`)
      .then(setData)
      .catch(() => {})
  }, [topic, range, page])
  return (
    <Sheet
      open={topic != null}
      onOpenChange={(o) => {
        if (!o) {
          onClose()
          setPage(1)
          setData(null)
        }
      }}
    >
      <SheetContent className="flex w-full flex-col gap-0 p-0 sm:max-w-xl">
        <SheetHeader className="border-b px-5 py-4">
          <SheetTitle>{topic?.title}</SheetTitle>
          <SheetDescription>客户的原话，最近的在前。</SheetDescription>
        </SheetHeader>
        <div className="flex-1 overflow-y-auto px-5 py-4">
          {!data ? (
            <div className="grid gap-3">
              <Skeleton className="h-14" />
              <Skeleton className="h-14" />
            </div>
          ) : (
            <>
              <div className="divide-y">
                {data.list.map((o) => (
                  <div key={o.id} className="grid gap-1 py-3">
                    <div className="flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
                      <span className="font-medium text-foreground">{o.userName || o.userId.slice(0, 10)}</span>
                      <span>
                        {CHANNEL_LABEL[o.channel] ?? o.channel} · {o.chatType === "group" || o.chatType === "guild" ? o.chatTitle || o.chatId.slice(0, 10) : (CHAT_TYPE_LABEL[o.chatType] ?? "私聊")}
                      </span>
                      <span className="ml-auto font-mono tabular-nums">{formatTime(o.askedAt)}</span>
                    </div>
                    <p className="text-[13.5px] whitespace-pre-wrap">{o.text}</p>
                  </div>
                ))}
              </div>
              <Pager page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />
            </>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}

export default function TopicsPage() {
  const [range, setRange] = useState<Range>("7")
  const [data, setData] = useState<TopicsData | null>(null)
  const { loading, track } = useDelayedLoading()
  const run = useDelayedPending()
  const act = useDelayedPending()
  const [viewing, setViewing] = useState<TopicRank | null>(null)
  const [renaming, setRenaming] = useState<{ t: TopicRank; title: string } | null>(null)
  const [merging, setMerging] = useState<{ t: TopicRank; into: string } | null>(null)
  const [deleting, setDeleting] = useState<TopicRank | null>(null)

  const load = useCallback(() => track(() => api.get<TopicsData>(`/api/topics?days=${range}&tz=${tz()}`).then(setData)).catch(() => {}), [track, range])
  useEffect(() => {
    load()
  }, [load])

  const runNow = () =>
    run
      .track(async () => {
        const r = await api.post<{ message: string }>("/api/topics/run")
        toast.success(r.message)
        await load()
      })
      .catch(() => {})

  const done = (fn: () => Promise<unknown>, ok: string, close: () => void) =>
    act
      .track(async () => {
        await fn()
        toast.success(ok)
        close()
        await load()
      })
      .catch(() => {})

  const cfg = data?.config
  const max = data?.ranking[0]?.count ?? 1

  return (
    <>
      <PageHeader
        title="问题排行"
        description="定期把群里群友的发言、私聊里客户的消息交给模型归类，看看大家最常问什么。与产品无关的闲聊不计入。适合拿来补知识库、改 FAQ。"
        actions={
          <>
            <SegmentedTabs<Range> value={range} onChange={setRange} items={RANGES} />
            <Button variant="outline" size="sm" onClick={runNow} disabled={run.pending || !data?.anyEnabled}>
              <PlayIcon />
              {run.pending ? "归类中…" : "立即归类"}
            </Button>
          </>
        }
      />
      {data && !data.anyEnabled && (
        <div className="mb-5 rounded-xl border border-warn/40 bg-warn-soft px-4 py-3 text-[13px]">
          问题排行已关闭，不再归类新消息。到{" "}
          <Link className="text-primary underline-offset-4 hover:underline" href="/admin/settings?tab=groupops">
            系统设置 → 群运营
          </Link>{" "}
          开启。
        </div>
      )}
      {data && data.anyEnabled && !data.qqGroupAll && (
        <div className="mb-5 rounded-xl border px-4 py-3 text-[13px] text-muted-foreground">
          QQ 未开启「接收群内全部消息」，QQ 群里只统计得到 @ 机器人的提问。
        </div>
      )}
      {loading && !data ? (
        <div className="grid gap-3">
          <Skeleton className="h-16 rounded-2xl" />
          <Skeleton className="h-16 rounded-2xl" />
          <Skeleton className="h-16 rounded-2xl" />
        </div>
      ) : (
        data &&
        cfg && (
          <div className="grid gap-6">
            {data.ranking.length === 0 ? (
              <Card className="py-0">
                <EmptyState
                  icon={<ListOrderedIcon className="size-5" />}
                  title="这段时间还没有归类到的问题"
                  description={`每 ${cfg.scanMin} 分钟归类一次，消息要先静置 2 分钟。也可以点右上角「立即归类」。`}
                />
              </Card>
            ) : (
              <Card className="gap-0 py-0">
                <div className="divide-y">
                  {data.ranking.map((t, i) => (
                    <div key={t.id} className="grid grid-cols-[28px_minmax(0,1fr)_auto] items-start gap-3 px-5 py-3.5">
                      <span className="pt-0.5 text-right font-mono text-[13px] text-muted-foreground tabular-nums">{i + 1}</span>
                      <button type="button" className="min-w-0 text-left" onClick={() => setViewing(t)}>
                        <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
                          <span className="text-[14.5px] font-medium hover:underline">{t.title}</span>
                          <Trend t={t} />
                        </div>
                        {/* 次数条：相对榜首的比例 */}
                        <div className="mt-1.5 flex items-center gap-2">
                          <div className="h-1.5 max-w-64 flex-1 rounded-full bg-muted">
                            <div className="h-full rounded-full bg-chart-1" style={{ width: `${(t.count / max) * 100}%` }} />
                          </div>
                          <span className="font-mono text-xs text-muted-foreground tabular-nums">
                            {formatNumber(t.count)} 次 · {t.users} 人{t.chats > 1 ? ` · ${t.chats} 个会话` : ""} · 最近 {formatTime(t.lastAt)}
                          </span>
                        </div>
                        <div className="mt-1.5 grid gap-0.5">
                          {t.samples.slice(0, 2).map((s, j) => (
                            <p key={j} className="truncate text-[13px] text-muted-foreground">
                              「{s}」
                            </p>
                          ))}
                        </div>
                      </button>
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button size="icon-sm" variant="ghost" aria-label={`管理「${t.title}」`}>
                            <MoreHorizontalIcon />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem onClick={() => setRenaming({ t, title: t.title })}>
                            <PencilIcon />
                            改名
                          </DropdownMenuItem>
                          <DropdownMenuItem onClick={() => setMerging({ t, into: "" })} disabled={data.ranking.length < 2}>
                            <MergeIcon />
                            合并到…
                          </DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem variant="destructive" onClick={() => setDeleting(t)}>
                            <Trash2Icon />
                            删除
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </div>
                  ))}
                </div>
              </Card>
            )}

            <section className="grid gap-3">
              <div>
                <h2 className="text-[15px] font-semibold">归类进度</h2>
                <p className="text-[13px] text-muted-foreground">
                  每 {cfg.scanMin} 分钟一轮，每批最多 {cfg.windowMax} 条，记录保留 {cfg.retentionDays} 天。私聊消息
                  {cfg.enabled ? (data.privateCursor ? `已归类到 ${formatTime(data.privateCursor)}` : "还没开始归类") : "不统计（全局开关已关闭）"}。
                </p>
              </div>
              <ChatProgressTable chats={data.chats} />
            </section>
          </div>
        )
      )}

      <OccurrencesSheet topic={viewing} range={range} onClose={() => setViewing(null)} />

      <Dialog open={renaming != null} onOpenChange={(o) => !o && setRenaming(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>主题改名</DialogTitle>
            <DialogDescription>新名称会作为之后归类的参考；想把它和另一个主题合在一起，请用「合并到…」。</DialogDescription>
          </DialogHeader>
          <Field label="主题名称" htmlFor="topic-title">
            <Input id="topic-title" value={renaming?.title ?? ""} onChange={(e) => renaming && setRenaming({ ...renaming, title: e.target.value })} />
          </Field>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setRenaming(null)}>
              取消
            </Button>
            <Button
              disabled={act.pending || !renaming?.title.trim()}
              onClick={() => renaming && done(() => api.patch(`/api/topics/${renaming.t.id}`, { title: renaming.title.trim() }), "已改名", () => setRenaming(null))}
            >
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={merging != null} onOpenChange={(o) => !o && setMerging(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>合并主题</DialogTitle>
            <DialogDescription>把「{merging?.t.title}」的全部记录并入另一个主题，之后它就不再单独出现。</DialogDescription>
          </DialogHeader>
          <Field label="并入">
            <Select value={merging?.into ?? ""} onValueChange={(into) => merging && setMerging({ ...merging, into })}>
              <SelectTrigger className="w-full">
                <SelectValue placeholder="选择主题" />
              </SelectTrigger>
              <SelectContent>
                {data?.ranking
                  .filter((x) => x.id !== merging?.t.id)
                  .map((x) => (
                    <SelectItem key={x.id} value={String(x.id)}>
                      {x.title}（{x.count}）
                    </SelectItem>
                  ))}
              </SelectContent>
            </Select>
          </Field>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setMerging(null)}>
              取消
            </Button>
            <Button
              disabled={act.pending || !merging?.into}
              onClick={() => merging && done(() => api.post(`/api/topics/${merging.t.id}/merge`, { into: Number(merging.into) }), "已合并", () => setMerging(null))}
            >
              合并
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={deleting != null} onOpenChange={(o) => !o && setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>删除主题</AlertDialogTitle>
            <AlertDialogDescription>删除「{deleting?.title}」及它的全部记录（不只是当前时间窗）。之后有人再问，会重新归类。</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={() => deleting && done(() => api.del(`/api/topics/${deleting.id}`), "已删除", () => setDeleting(null))}>
              删除
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
