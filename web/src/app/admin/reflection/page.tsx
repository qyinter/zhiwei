"use client"

import { ArrowUpFromLineIcon, BrainCircuitIcon, ChevronDownIcon, CombineIcon, RotateCcwIcon, XIcon } from "lucide-react"
import Link from "next/link"
import { useCallback, useEffect, useState } from "react"
import { toast } from "sonner"

import { ChatProgressTable } from "@/components/chat-progress"
import { EmptyState, formatTime, Kicker, PageHeader } from "@/components/common"
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
import { Card, CardContent } from "@/components/ui/card"
import { SegmentedTabs } from "@/components/ui/segmented-tabs"
import { Skeleton } from "@/components/ui/skeleton"
import { api } from "@/lib/api"
import { CHANNEL_LABEL, type Compaction, formatDuration, type Reflection, type ReflectionData } from "@/lib/types"
import { useDelayedLoading, useDelayedPending } from "@/lib/use-delayed-pending"

type Filter = "" | "approved" | "rejected" | "promoted"

function Stat({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="rounded-xl border bg-card px-4 py-3">
      <Kicker>{label}</Kicker>
      <div className="mt-1 font-mono text-[15px] font-medium tabular-nums">{value}</div>
    </div>
  )
}

function StatusBadge({ r }: { r: Reflection }) {
  if (r.status === "rejected") return <Badge variant="crit">已驳回</Badge>
  if (r.status === "promoted") return <Badge variant="accent">已升格</Badge>
  return <Badge variant="ok">已入库</Badge>
}

function EntryCard({ r, onAction }: { r: Reflection; onAction: (action: "approve" | "reject" | "promote") => void }) {
  return (
    <Card className="py-0">
      <CardContent className="grid gap-2.5 p-5">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-mono text-xs text-muted-foreground">#{r.id}</span>
          <StatusBadge r={r} />
          {r.compacted && <Badge variant="neutral">已整理</Badge>}
          {r.channel && <Badge variant="neutral">{CHANNEL_LABEL[r.channel] ?? r.channel}</Badge>}
          <span className="ml-auto font-mono text-xs text-muted-foreground tabular-nums">{formatTime(r.createdAt)}</span>
        </div>
        <p className="text-[13.5px] whitespace-pre-wrap">{r.faq}</p>
        {(r.question || r.answer) && (
          <details className="text-[13px] text-muted-foreground">
            <summary className="cursor-pointer select-none">来源问答</summary>
            <p className="mt-1.5 whitespace-pre-wrap">问：{r.question || "—"}</p>
            <p className="mt-1 whitespace-pre-wrap">答：{r.answer || "—"}</p>
          </details>
        )}
        <div className="flex flex-wrap gap-2">
          {r.status === "approved" && (
            <>
              <Button size="sm" variant="outline" onClick={() => onAction("promote")}>
                <ArrowUpFromLineIcon />
                升格为正式文档
              </Button>
              <Button size="sm" variant="outline" onClick={() => onAction("reject")}>
                <XIcon />
                驳回
              </Button>
            </>
          )}
          {r.status === "rejected" && (
            <Button size="sm" variant="outline" onClick={() => onAction("approve")}>
              <RotateCcwIcon />
              恢复入库
            </Button>
          )}
          {r.status === "promoted" && r.promotedDocId && (
            <Button size="sm" variant="ghost" asChild>
              <Link href="/admin/kb">查看知识库文档 #{r.promotedDocId}</Link>
            </Button>
          )}
        </div>
      </CardContent>
    </Card>
  )
}

function CompactionRow({ c }: { c: Compaction }) {
  const [detail, setDetail] = useState<Compaction | null>(null)
  const [open, setOpen] = useState(false)
  const toggle = () => {
    setOpen(!open)
    if (!detail) api.get<Compaction>(`/api/reflection/compactions/${c.id}`).then(setDetail).catch(() => {})
  }
  return (
    <div className="border-b last:border-b-0">
      <button type="button" className="flex w-full items-center gap-3 px-5 py-3 text-left text-[13.5px] hover:bg-muted/50" onClick={toggle}>
        <span className="font-mono tabular-nums">
          {c.beforeCount} → {c.afterCount} 条
        </span>
        <span className="ml-auto font-mono text-xs text-muted-foreground tabular-nums">{formatTime(c.createdAt)}</span>
        <ChevronDownIcon className={open ? "size-4 rotate-180 transition-transform" : "size-4 transition-transform"} />
      </button>
      {open && (
        <div className="grid gap-4 px-5 pb-4 text-[13px] sm:grid-cols-2">
          <div className="grid content-start gap-2">
            <Kicker>整理前</Kicker>
            {detail?.before?.map((b) => (
              <p key={b.id} className="whitespace-pre-wrap text-muted-foreground">
                #{b.id} {b.faq}
              </p>
            )) ?? <Skeleton className="h-16" />}
          </div>
          <div className="grid content-start gap-2">
            <Kicker>整理后</Kicker>
            {detail?.after?.map((a, i) => (
              <p key={i} className="whitespace-pre-wrap">
                {a}
              </p>
            )) ?? <Skeleton className="h-16" />}
          </div>
        </div>
      )}
    </div>
  )
}

export default function ReflectionPage() {
  const [filter, setFilter] = useState<Filter>("")
  const [data, setData] = useState<ReflectionData | null>(null)
  const [confirmCompact, setConfirmCompact] = useState(false)
  const { loading, track } = useDelayedLoading()
  const job = useDelayedPending()

  const load = useCallback(() => {
    const q = filter ? `?status=${filter}` : ""
    return track(() => api.get<ReflectionData>(`/api/reflection${q}`).then(setData)).catch(() => {})
  }, [filter, track])

  useEffect(() => {
    load()
    const t = setInterval(load, 30000)
    return () => clearInterval(t)
  }, [load])

  const act = (r: Reflection, action: "approve" | "reject" | "promote") =>
    api
      .patch<{ documentId?: number } | undefined>(`/api/reflection/${r.id}`, { action })
      .then((res) => {
        toast.success(
          action === "promote" ? `已升格 #${r.id} → 知识库文档 #${res?.documentId ?? ""}` : action === "reject" ? `已驳回 #${r.id}，该条已从检索中移除` : `已恢复入库 #${r.id}`
        )
        return load()
      })
      .catch(() => {})

  const run = (name: "compact" | "promote") =>
    job
      .track(async () => {
        const res = await api.post<{ message: string }>(`/api/reflection/run/${name}`)
        toast.success(res.message)
        await load()
      })
      .catch(() => {})

  const cfg = data?.config
  const approved = data?.counts.approved ?? 0
  return (
    <>
      <PageHeader
        title="反思沉淀"
        description="从群主 / 管理员的答复中提炼可复用的问答，自动进入知识检索；定期整理合并，评审通过的升格为正式知识库文档。"
        actions={
          cfg && (
            <>
              <Button variant="outline" disabled={job.pending || approved < cfg.compactMinEntries} onClick={() => setConfirmCompact(true)}>
                <CombineIcon />
                立即整理
              </Button>
              <Button disabled={job.pending || approved < cfg.promoteMinEntries || approved === 0} onClick={() => run("promote")}>
                <ArrowUpFromLineIcon />
                {job.pending ? "处理中…" : "立即升格评审"}
              </Button>
            </>
          )
        }
      />
      {data && !data.qqGroupAll && (
        <div className="mb-5 rounded-xl border border-warn/40 bg-warn-soft px-4 py-3 text-[13px]">
          QQ 未开启「接收群内全部消息」，收不到群管理员的答复，QQ 群不会产生沉淀。到{" "}
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
        cfg &&
        data && (
          <div className="grid gap-6">
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <Stat label="扫描周期" value={cfg.enabled || data.anyEnabled ? formatDuration(cfg.scanMin * 60) : "已关闭"} />
              <Stat label="静置等待" value={formatDuration(cfg.settleMin * 60)} />
              <Stat label="整理周期" value={cfg.compactHours ? `${cfg.compactHours} 小时` : "已关闭"} />
              <Stat label="升格周期" value={cfg.promoteHours ? `${cfg.promoteHours} 小时` : "已关闭"} />
              <Stat label="已入库" value={approved} />
              <Stat label="可整理 / 门槛" value={`${approved} / ${cfg.compactMinEntries}`} />
              <Stat label="已升格" value={data.counts.promoted} />
              <Stat label="已驳回" value={data.counts.rejected} />
            </div>

            <section className="grid gap-3">
              <div>
                <h2 className="text-[15px] font-semibold">每群反思进度</h2>
                <p className="text-[13px] text-muted-foreground">
                  上次整理 {data.compactAt ? formatTime(data.compactAt) : "—"} · 上次升格评审 {data.promoteAt ? formatTime(data.promoteAt) : "—"}
                </p>
              </div>
              <ChatProgressTable chats={data.chats} extra={{ label: "已沉淀", value: (c) => c.sediment }} />
            </section>

            <section className="grid gap-3">
              <div className="flex flex-wrap items-end justify-between gap-3">
                <div>
                  <h2 className="text-[15px] font-semibold">知识条目（{data.entries.length}）</h2>
                  <p className="text-[13px] text-muted-foreground">已入库的条目机器人检索可直接命中；可驳回，或升格为正式文档。</p>
                </div>
                <SegmentedTabs<Filter>
                  value={filter}
                  onChange={setFilter}
                  items={[
                    { key: "", label: "全部" },
                    { key: "approved", label: "已入库" },
                    { key: "promoted", label: "已升格" },
                    { key: "rejected", label: "已驳回" },
                  ]}
                />
              </div>
              {data.entries.length === 0 ? (
                <Card className="py-0">
                  <EmptyState icon={<BrainCircuitIcon className="size-5" />} title="还没有沉淀" description="群管理员在群里答复用户后，静置一段时间会自动提炼。" />
                </Card>
              ) : (
                <div className="grid gap-3">
                  {data.entries.map((r) => (
                    <EntryCard key={r.id} r={r} onAction={(a) => act(r, a)} />
                  ))}
                </div>
              )}
            </section>

            {data.compactions.length > 0 && (
              <section className="grid gap-3">
                <h2 className="text-[15px] font-semibold">整理记录（{data.compactions.length}）</h2>
                <Card className="py-0">
                  {data.compactions.map((c) => (
                    <CompactionRow key={c.id} c={c} />
                  ))}
                </Card>
              </section>
            )}
          </div>
        )
      )}
      <AlertDialog open={confirmCompact} onOpenChange={setConfirmCompact}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>立即整理沉淀条目</AlertDialogTitle>
            <AlertDialogDescription>对当前 {approved} 条知识做一次近义去重合并（保留细节，不因基础文档已覆盖而删除）。合并后的条目不再保留单条来源问答。</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction onClick={() => run("compact")}>确认整理</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
