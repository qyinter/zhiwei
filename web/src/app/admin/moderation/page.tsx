"use client"

import { ShieldAlertIcon, Undo2Icon } from "lucide-react"
import { useCallback, useEffect, useState } from "react"
import { toast } from "sonner"

import { EmptyState, formatTime, PageHeader, Pager } from "@/components/common"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api } from "@/lib/api"
import { CHANNEL_LABEL, type ModerationAction, type ModerationData } from "@/lib/types"
import { useDelayedLoading, useDelayedPending } from "@/lib/use-delayed-pending"

const ACTION: Record<ModerationAction["action"], { label: string; variant: "ok" | "crit" | "warn" | "neutral" }> = {
  none: { label: "仅记录", variant: "neutral" },
  delete: { label: "已删除", variant: "warn" },
  mute: { label: "删除并禁言", variant: "crit" },
  ban: { label: "删除并封禁", variant: "crit" },
}

const SOURCE: Record<ModerationAction["source"], string> = { rule: "规则", llm: "模型" }

export default function ModerationPage() {
  const [page, setPage] = useState(1)
  const [data, setData] = useState<ModerationData | null>(null)
  const { loading, track } = useDelayedLoading()
  const revert = useDelayedPending()

  const load = useCallback(() => track(() => api.get<ModerationData>(`/api/moderation?page=${page}`).then(setData)).catch(() => {}), [page, track])

  useEffect(() => {
    load()
    const t = setInterval(load, 15000)
    return () => clearInterval(t)
  }, [load])

  const undo = (r: ModerationAction) =>
    revert
      .track(async () => {
        await api.post(`/api/moderation/${r.id}/revert`)
        toast.success(r.action === "mute" || r.action === "ban" ? "已解除限制并标记为误判" : "已标记为误判")
        await load()
      })
      .catch(() => {})

  return (
    <>
      <PageHeader
        title="消息治理"
        description="群里被判定为广告的消息及处置结果：规则层即时处置新成员的链接 / @ / 转发与关键词黑名单，模型层对可疑消息再判一次。误判可在这里恢复（解除禁言 / 封禁；被删的消息无法找回）。规则与开关在「系统设置 → 群运营」。"
      />
      {data && !data.anyEnabled && (
        <p className="mb-4 text-sm text-muted-foreground">群消息治理尚未开启（全局与各群都关着），这里只会显示历史记录。</p>
      )}
      <Card className="py-0">
        {loading && !data ? (
          <div className="grid gap-2 p-4">
            {Array.from({ length: 5 }).map((_, i) => (
              <Skeleton key={i} className="h-10" />
            ))}
          </div>
        ) : data && data.list.length === 0 ? (
          <EmptyState icon={<ShieldAlertIcon className="size-5" />} title="暂无处置记录" description="开启群消息治理后，被判定为广告的消息会出现在这里。Telegram 需把机器人设为群管理员并勾选「删除消息」「封禁用户」。" />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>发言人</TableHead>
                <TableHead>群</TableHead>
                <TableHead>消息</TableHead>
                <TableHead>判定</TableHead>
                <TableHead>处置</TableHead>
                <TableHead className="text-right">时间</TableHead>
                <TableHead className="pr-5" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {data?.list.map((r) => (
                <TableRow key={r.id} className={r.revertedAt ? "opacity-55" : undefined}>
                  <TableCell className="max-w-40 truncate font-medium">{r.userName || <span className="font-mono text-xs">{r.userId}</span>}</TableCell>
                  <TableCell className="max-w-48 truncate text-muted-foreground">
                    <Badge variant="neutral" className="mr-1.5">
                      {CHANNEL_LABEL[r.channel] ?? r.channel}
                    </Badge>
                    {r.chatTitle || <span className="font-mono text-xs">{r.chatId}</span>}
                  </TableCell>
                  <TableCell className="max-w-72">
                    <div className="truncate text-[13px]" title={r.content}>
                      {r.content || "（无文字）"}
                    </div>
                  </TableCell>
                  <TableCell className="max-w-64">
                    <div className="truncate text-[13px]" title={r.reason}>
                      <span className="mr-1.5 font-mono text-xs text-muted-foreground">{SOURCE[r.source] ?? r.source}</span>
                      {r.reason}
                    </div>
                    {r.source === "llm" && <div className="font-mono text-xs text-muted-foreground">置信度 {r.confidence.toFixed(2)}</div>}
                  </TableCell>
                  <TableCell className="max-w-56">
                    <Badge variant={ACTION[r.action]?.variant ?? "neutral"}>{ACTION[r.action]?.label ?? r.action}</Badge>
                    {r.revertedAt && (
                      <Badge variant="ok" className="ml-1.5">
                        已恢复
                      </Badge>
                    )}
                    {r.detail && (
                      <div className="mt-1 truncate text-xs text-crit" title={r.detail}>
                        {r.detail}
                      </div>
                    )}
                  </TableCell>
                  <TableCell className="text-right font-mono text-xs tabular-nums text-muted-foreground">{formatTime(r.createdAt)}</TableCell>
                  <TableCell className="pr-5 text-right">
                    {!r.revertedAt && (
                      <Button size="sm" variant="ghost" disabled={revert.pending} onClick={() => undo(r)}>
                        <Undo2Icon />
                        误判恢复
                      </Button>
                    )}
                  </TableCell>
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
