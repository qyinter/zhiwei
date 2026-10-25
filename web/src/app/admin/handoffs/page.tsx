"use client"

import { HeadsetIcon } from "lucide-react"
import { useCallback, useEffect, useState } from "react"

import { EmptyState, formatTime, PageHeader, Pager } from "@/components/common"
import { ConversationSheet } from "@/components/conversation-panel"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent } from "@/components/ui/card"
import { SegmentedTabs } from "@/components/ui/segmented-tabs"
import { Skeleton } from "@/components/ui/skeleton"
import { api, type Paged } from "@/lib/api"
import { CHANNEL_LABEL, type Handoff } from "@/lib/types"
import { useDelayedLoading } from "@/lib/use-delayed-pending"

type Tab = "open" | "resolved" | "all"

export default function HandoffsPage() {
  const [tab, setTab] = useState<Tab>("open")
  const [page, setPage] = useState(1)
  const [data, setData] = useState<Paged<Handoff> | null>(null)
  const [openId, setOpenId] = useState<number | null>(null)
  const { loading, track } = useDelayedLoading()

  const load = useCallback(() => {
    const q = new URLSearchParams({ page: String(page) })
    if (tab !== "all") q.set("status", tab)
    return track(() => api.get<Paged<Handoff>>(`/api/handoffs?${q}`).then(setData)).catch(() => {})
  }, [page, tab, track])

  useEffect(() => {
    load()
    const t = setInterval(load, 15000)
    return () => clearInterval(t)
  }, [load])

  return (
    <>
      <PageHeader title="人工队列" description="模型判定需要人工、或用户发送「人工」时进入队列；该会话暂停自动回复，处理完点「恢复自动」。" />
      <SegmentedTabs<Tab>
        className="mb-4"
        value={tab}
        onChange={(t) => {
          setPage(1)
          setTab(t)
        }}
        items={[
          { key: "open", label: "待处理" },
          { key: "resolved", label: "已处理" },
          { key: "all", label: "全部" },
        ]}
      />
      {loading && !data ? (
        <div className="grid gap-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-24 rounded-2xl" />
          ))}
        </div>
      ) : data && data.list.length === 0 ? (
        <Card className="py-0">
          <EmptyState icon={<HeadsetIcon className="size-5" />} title={tab === "open" ? "没有待处理的人工会话" : "暂无记录"} />
        </Card>
      ) : (
        <div className="grid gap-3">
          {data?.list.map((h) => (
            <Card key={h.id} className="cursor-pointer py-0 transition-shadow hover:shadow-md" onClick={() => setOpenId(h.conversationId)}>
              <CardContent className="grid gap-2 p-5">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium">{h.userName || h.userId.slice(0, 12)}</span>
                  <Badge variant="neutral">{CHANNEL_LABEL[h.channel] ?? h.channel}</Badge>
                  {h.status === "open" ? <Badge variant="warn">待处理</Badge> : <Badge variant="ok">已处理</Badge>}
                  <span className="ml-auto font-mono text-xs text-muted-foreground tabular-nums">{formatTime(h.createdAt)}</span>
                </div>
                <p className="text-[13.5px] text-foreground">{h.summary || "（无摘要）"}</p>
                <p className="text-xs text-muted-foreground">原因：{h.reason}</p>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
      {data && <Pager page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
      <ConversationSheet convId={openId} onClose={() => setOpenId(null)} onChanged={load} />
    </>
  )
}
