"use client"

import { UserCheckIcon } from "lucide-react"
import { useCallback, useEffect, useState } from "react"

import { EmptyState, formatTime, PageHeader, Pager } from "@/components/common"
import { Badge } from "@/components/ui/badge"
import { Card } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api, type Paged } from "@/lib/api"
import { CHANNEL_LABEL, type JoinRequestRecord } from "@/lib/types"
import { useDelayedLoading } from "@/lib/use-delayed-pending"

const DECISION: Record<JoinRequestRecord["decision"], { label: string; variant: "ok" | "crit" | "warn" | "neutral" }> = {
  approved: { label: "已同意", variant: "ok" },
  declined: { label: "已拒绝", variant: "crit" },
  pending: { label: "留给管理员", variant: "warn" },
  auto: { label: "平台已自动通过", variant: "neutral" },
  failed: { label: "审批出错", variant: "crit" },
}

export default function JoinRequestsPage() {
  const [page, setPage] = useState(1)
  const [data, setData] = useState<Paged<JoinRequestRecord> | null>(null)
  const { loading, track } = useDelayedLoading()

  const load = useCallback(
    () => track(() => api.get<Paged<JoinRequestRecord>>(`/api/join-requests?page=${page}`).then(setData)).catch(() => {}),
    [page, track]
  )

  useEffect(() => {
    load()
    const t = setInterval(load, 15000)
    return () => clearInterval(t)
  }, [load])

  return (
    <>
      <PageHeader
        title="入群审核"
        description="QQ 生效群的加群申请处理记录：取答案里的 ID 调核对接口，存在则同意、不存在则拒绝，接口不可用时留给管理员在 QQ 里处理。开关与核对接口在「系统设置 → 群运营」。"
      />
      <Card className="py-0">
        {loading && !data ? (
          <div className="grid gap-2 p-4">
            {Array.from({ length: 5 }).map((_, i) => (
              <Skeleton key={i} className="h-10" />
            ))}
          </div>
        ) : data && data.list.length === 0 ? (
          <EmptyState icon={<UserCheckIcon className="size-5" />} title="暂无入群申请" description="开启入群审核后，机器人（需是群管理员）收到的加群申请会出现在这里。" />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>申请人</TableHead>
                <TableHead>群</TableHead>
                <TableHead>答案 / 提取 ID</TableHead>
                <TableHead>结果</TableHead>
                <TableHead className="text-right">时间</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data?.list.map((r) => (
                <TableRow key={r.id}>
                  <TableCell className="max-w-40 truncate font-medium">{r.userName || <span className="font-mono text-xs">{r.userId}</span>}</TableCell>
                  <TableCell className="max-w-48 truncate text-muted-foreground">
                    <Badge variant="neutral" className="mr-1.5">
                      {CHANNEL_LABEL[r.channel] ?? r.channel}
                    </Badge>
                    {r.chatTitle || <span className="font-mono text-xs">{r.chatId}</span>}
                  </TableCell>
                  <TableCell className="max-w-64">
                    <div className="truncate text-[13px]">{r.answer || "（未填写）"}</div>
                    {r.extractedId && r.extractedId !== r.answer && <div className="font-mono text-xs text-muted-foreground">{r.extractedId}</div>}
                  </TableCell>
                  <TableCell className="max-w-64">
                    <Badge variant={DECISION[r.decision]?.variant ?? "neutral"}>{DECISION[r.decision]?.label ?? r.decision}</Badge>
                    {r.detail && <div className="mt-1 truncate text-xs text-muted-foreground" title={r.detail}>{r.detail}</div>}
                  </TableCell>
                  <TableCell className="text-right font-mono text-xs tabular-nums text-muted-foreground">{formatTime(r.createdAt)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Card>
      {data && <Pager page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
    </>
  )
}
