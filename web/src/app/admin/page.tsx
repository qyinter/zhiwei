"use client"

import { AlertTriangleIcon, RefreshCwIcon } from "lucide-react"
import Link from "next/link"
import { useCallback, useEffect, useState } from "react"

import { formatNumber, Kicker, PageHeader, StatusDot } from "@/components/common"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { api } from "@/lib/api"
import { browserTZ, CHANNEL_LABEL, type ChannelStatus, type Overview } from "@/lib/types"
import { useDelayedLoading } from "@/lib/use-delayed-pending"

function Stat({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <Card className="py-0">
      <CardContent className="p-5">
        <Kicker>{label}</Kicker>
        <div className="mt-2 font-mono text-[28px] font-semibold tracking-tight text-foreground tabular-nums">{value}</div>
        {sub && <div className="mt-1 text-xs text-muted-foreground">{sub}</div>}
      </CardContent>
    </Card>
  )
}

function channelTone(s: ChannelStatus): "ok" | "warn" | "crit" | "neutral" {
  if (!s.enabled) return "neutral"
  if (s.connected) return "ok"
  return s.lastError ? "crit" : "warn"
}

export default function OverviewPage() {
  const [data, setData] = useState<Overview | null>(null)
  const { loading, track } = useDelayedLoading()

  const load = useCallback(() => track(() => api.get<Overview>(`/api/overview?tz=${encodeURIComponent(browserTZ())}`).then(setData)).catch(() => {}), [track])
  useEffect(() => {
    load()
    const t = setInterval(load, 15000)
    return () => clearInterval(t)
  }, [load])

  return (
    <>
      <PageHeader
        title="总览"
        description={data ? `${data.brand.name} · 今日运行情况与渠道状态，每 15 秒刷新` : "今日运行情况与渠道状态"}
        actions={
          <Button variant="outline" size="sm" onClick={load}>
            <RefreshCwIcon />
            刷新
          </Button>
        }
      />
      {loading && !data ? (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-28 rounded-2xl" />
          ))}
        </div>
      ) : (
        data && (
          <div className="grid gap-6">
            {data.llm.error && (
              <div className="flex items-start gap-3 rounded-xl border border-warn/30 bg-warn-soft p-4 text-[13px] text-warn">
                <AlertTriangleIcon className="mt-0.5 size-4 shrink-0" />
                <span>
                  大模型未就绪：{data.llm.error}。自动回复已暂停，
                  <Link className="underline underline-offset-2" href="/admin/settings">
                    前往设置
                  </Link>
                </span>
              </div>
            )}
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
              <Stat label="今日咨询" value={formatNumber(data.stats.todayInbound)} sub={`自动回复 ${formatNumber(data.stats.todayReplies)} 条`} />
              <Stat label="待人工" value={formatNumber(data.stats.openHandoffs)} sub="人工队列中未处理" />
              <Stat
                label="今日模型调用"
                value={formatNumber(data.stats.todayLlmCalls)}
                sub={data.stats.todayLlmErrors > 0 ? `失败 ${data.stats.todayLlmErrors} 次` : "无失败"}
              />
              <Link href="/admin/usage" className="rounded-2xl outline-none focus-visible:ring-2 focus-visible:ring-ring">
                <Stat
                  label="今日 Token"
                  value={formatNumber(data.stats.todayInputTokens + data.stats.todayOutputTokens)}
                  sub={`输入 ${formatNumber(data.stats.todayInputTokens)}（缓存命中 ${data.stats.todayInputTokens ? Math.round((data.stats.todayCacheReadTokens / data.stats.todayInputTokens) * 100) : 0}%）· 输出 ${formatNumber(data.stats.todayOutputTokens)}`}
                />
              </Link>
            </div>

            <div className="grid gap-4 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
              <Card className="py-0">
                <CardContent className="p-5">
                  <div className="mb-4 flex items-center justify-between">
                    <h2 className="text-base font-semibold">渠道</h2>
                    <Link href="/admin/settings" className="text-[13px] text-primary dark:text-[var(--ring)]">
                      配置渠道
                    </Link>
                  </div>
                  <div className="divide-y">
                    {data.channels.map((c) => (
                      <div key={c.channel} className="flex items-center justify-between gap-4 py-3">
                        <div className="min-w-0">
                          <div className="text-[13.5px] font-medium">{CHANNEL_LABEL[c.channel] ?? c.channel}</div>
                          <div className="truncate text-xs text-muted-foreground">{c.lastError || c.detail}</div>
                        </div>
                        <Badge variant={channelTone(c) === "neutral" ? "neutral" : channelTone(c)}>
                          <StatusDot tone={channelTone(c)} />
                          {!c.enabled ? "未启用" : c.connected ? "已连接" : c.lastError ? "异常" : "连接中"}
                        </Badge>
                      </div>
                    ))}
                  </div>
                </CardContent>
              </Card>
              <Card className="py-0">
                <CardContent className="grid gap-4 p-5">
                  <h2 className="text-base font-semibold">大模型与知识</h2>
                  <dl className="grid gap-3 text-[13px]">
                    <div className="flex justify-between gap-3">
                      <dt className="text-muted-foreground">协议</dt>
                      <dd className="font-mono">{data.llm.protocol}</dd>
                    </div>
                    <div className="flex justify-between gap-3">
                      <dt className="text-muted-foreground">模型</dt>
                      <dd className="truncate font-mono">{data.llm.model || "未配置"}</dd>
                    </div>
                    <div className="flex justify-between gap-3">
                      <dt className="text-muted-foreground">知识文档</dt>
                      <dd className="font-mono tabular-nums">{data.stats.kbDocuments}</dd>
                    </div>
                    <div className="flex justify-between gap-3">
                      <dt className="text-muted-foreground">检索块</dt>
                      <dd className="font-mono tabular-nums">{data.kbChunks}</dd>
                    </div>
                    <div className="flex justify-between gap-3">
                      <dt className="text-muted-foreground">累计会话</dt>
                      <dd className="font-mono tabular-nums">{formatNumber(data.stats.conversations)}</dd>
                    </div>
                  </dl>
                </CardContent>
              </Card>
            </div>
          </div>
        )
      )}
    </>
  )
}
