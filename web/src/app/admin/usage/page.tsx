"use client"

import { AlertTriangleIcon, RefreshCwIcon } from "lucide-react"
import { useCallback, useEffect, useState } from "react"

import { EmptyState, formatNumber, formatTime, Kicker, PageHeader } from "@/components/common"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { SegmentedTabs } from "@/components/ui/segmented-tabs"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api } from "@/lib/api"
import { browserTZ, PURPOSE_LABEL, type UsageData, type UsageTotals } from "@/lib/types"
import { useDelayedLoading } from "@/lib/use-delayed-pending"

type Range = "1" | "7" | "30"

const RANGES: { key: Range; label: string }[] = [
  { key: "1", label: "今日" },
  { key: "7", label: "近 7 天" },
  { key: "30", label: "近 30 天" },
]

/** 1234567 → 1.2M；坐标轴与紧凑场合用 */
function compact(n: number) {
  if (n >= 1e6) return `${(n / 1e6).toFixed(n >= 1e7 ? 0 : 1)}M`
  if (n >= 1e3) return `${(n / 1e3).toFixed(n >= 1e4 ? 0 : 1)}K`
  return String(n)
}

function hitRate(t: UsageTotals) {
  return t.inputTokens ? Math.round((t.cacheReadTokens / t.inputTokens) * 100) : 0
}

function Stat({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <Card className="py-0">
      <CardContent className="p-5">
        <Kicker>{label}</Kicker>
        <div className="mt-2 font-mono text-[26px] font-semibold tracking-tight tabular-nums">{value}</div>
        {sub && <div className="mt-1 text-xs text-muted-foreground">{sub}</div>}
      </CardContent>
    </Card>
  )
}

/** 「天 → 当天总 token」的柱状图：同一序列一个颜色，悬停看当天明细。 */
function DailyChart({ days, from, span }: { days: UsageData["usage"]["days"]; from: string; span: number }) {
  const [hover, setHover] = useState<number | null>(null)
  // 补齐没有调用的日子，横轴才是连续的
  const byDay = new Map(days.map((d) => [d.day, d]))
  const start = new Date(from)
  const slots = Array.from({ length: span }, (_, i) => {
    const d = new Date(start)
    d.setDate(start.getDate() + i)
    const key = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`
    return { key, label: `${d.getMonth() + 1}-${String(d.getDate()).padStart(2, "0")}`, data: byDay.get(key) }
  })
  const total = (d?: UsageTotals) => (d ? d.inputTokens + d.outputTokens : 0)
  const peak = Math.max(...slots.map((s) => total(s.data)), 1)
  // 纵轴取整到 1 / 2 / 5 × 10^n
  const mag = 10 ** Math.floor(Math.log10(peak))
  const top = [1, 2, 5, 10].map((m) => m * mag).find((v) => v >= peak) ?? peak
  const ticks = [0, top / 2, top]
  const labelEvery = span > 14 ? 5 : 1

  return (
    <div className="mt-3 grid grid-cols-[40px_minmax(0,1fr)] gap-x-2">
      <div className="relative h-48">
        {ticks.map((t) => (
          <span key={t} className="absolute right-0 -translate-y-1/2 font-mono text-[11px] text-muted-foreground tabular-nums" style={{ bottom: `${(t / top) * 100}%` }}>
            {compact(t)}
          </span>
        ))}
      </div>
      <div className="relative h-48">
        {ticks.map((t) => (
          <div key={t} className="absolute inset-x-0 h-px bg-border" style={{ bottom: `${(t / top) * 100}%` }} />
        ))}
        <div className="absolute inset-0 flex items-end gap-0.5">
          {slots.map((s, i) => (
            <div
              key={s.key}
              className="relative flex h-full flex-1 items-end justify-center"
              onMouseEnter={() => setHover(i)}
              onMouseLeave={() => setHover((h) => (h === i ? null : h))}
            >
              <div
                className="w-full max-w-6 rounded-t-[4px] bg-chart-1 transition-opacity"
                style={{ height: `${(total(s.data) / top) * 100}%`, opacity: hover == null || hover === i ? 1 : 0.45 }}
              />
              {hover === i && (
                // 浮层贴着柱顶；高柱子时压到图内，不盖住卡片标题
                <div
                  className={`pointer-events-none absolute z-10 w-48 rounded-lg border bg-popover p-2.5 text-xs shadow-md ${i > span / 2 ? "right-1/2" : "left-1/2"}`}
                  style={{ bottom: `calc(${Math.min((total(s.data) / top) * 100, 40)}% + 8px)` }}
                >
                  <div className="mb-1 font-medium">{s.key}</div>
                  {s.data ? (
                    <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-muted-foreground">
                      <dt>调用</dt>
                      <dd className="text-right font-mono text-foreground tabular-nums">{formatNumber(s.data.calls)}</dd>
                      <dt>输入</dt>
                      <dd className="text-right font-mono text-foreground tabular-nums">{formatNumber(s.data.inputTokens)}</dd>
                      <dt>缓存命中</dt>
                      <dd className="text-right font-mono text-foreground tabular-nums">{hitRate(s.data)}%</dd>
                      <dt>输出</dt>
                      <dd className="text-right font-mono text-foreground tabular-nums">{formatNumber(s.data.outputTokens)}</dd>
                      {s.data.errors > 0 && (
                        <>
                          <dt>失败</dt>
                          <dd className="text-right font-mono text-foreground tabular-nums">{s.data.errors}</dd>
                        </>
                      )}
                    </dl>
                  ) : (
                    <div className="text-muted-foreground">无调用</div>
                  )}
                </div>
              )}
            </div>
          ))}
        </div>
      </div>
      <div />
      <div className="mt-1.5 flex h-4 gap-0.5">
        {slots.map((s, i) => (
          <span key={s.key} className="relative flex-1">
            {(i % labelEvery === 0 || i === span - 1) && (
              <span className="absolute left-1/2 -translate-x-1/2 font-mono text-[11px] whitespace-nowrap text-muted-foreground tabular-nums">{s.label}</span>
            )}
          </span>
        ))}
      </div>
    </div>
  )
}

function GroupTable({ title, rows, label }: { title: string; rows: (UsageTotals & { key: string })[]; label: (k: string) => string }) {
  return (
    <Card className="py-0">
      <div className="px-5 pt-4 pb-2 text-[15px] font-semibold">{title}</div>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="pl-5">名称</TableHead>
            <TableHead className="text-right">调用</TableHead>
            <TableHead className="text-right">输入</TableHead>
            <TableHead className="text-right">缓存命中</TableHead>
            <TableHead className="text-right">输出</TableHead>
            <TableHead className="pr-5 text-right">平均耗时</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((r) => (
            <TableRow key={r.key}>
              <TableCell className="max-w-48 truncate pl-5">{label(r.key)}</TableCell>
              <TableCell className="text-right font-mono tabular-nums">
                {formatNumber(r.calls)}
                {r.errors > 0 && <span className="ml-1 text-crit">（败 {r.errors}）</span>}
              </TableCell>
              <TableCell className="text-right font-mono tabular-nums">{compact(r.inputTokens)}</TableCell>
              <TableCell className="text-right font-mono tabular-nums">{hitRate(r)}%</TableCell>
              <TableCell className="text-right font-mono tabular-nums">{compact(r.outputTokens)}</TableCell>
              <TableCell className="pr-5 text-right font-mono tabular-nums">{(r.avgLatencyMs / 1000).toFixed(1)}s</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Card>
  )
}

export default function UsagePage() {
  const [range, setRange] = useState<Range>("7")
  const [data, setData] = useState<UsageData | null>(null)
  const { loading, track } = useDelayedLoading()

  const load = useCallback(
    () => track(() => api.get<UsageData>(`/api/usage?days=${range}&tz=${encodeURIComponent(browserTZ())}`).then(setData)).catch(() => {}),
    [track, range]
  )
  useEffect(() => {
    load()
  }, [load])

  const u = data?.usage
  const t = u?.totals
  // 工具使用率的分母：客服回复与主动回复的推理轮数（只有这两类会调工具）
  const runs = u?.byPurpose.filter((p) => p.key === "reply" || p.key === "proactive").reduce((n, p) => n + p.calls, 0) ?? 0

  return (
    <>
      <PageHeader
        title="用量统计"
        description="每次调用大模型的 token、缓存命中与耗时。缓存命中率越高越省钱：系统提示词保持不变、少改业务口径与技能，命中率才稳定。"
        actions={
          <>
            <SegmentedTabs<Range> value={range} onChange={setRange} items={RANGES} />
            <Button variant="outline" size="icon-sm" onClick={load} aria-label="刷新">
              <RefreshCwIcon />
            </Button>
          </>
        }
      />
      {loading && !data ? (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-28 rounded-2xl" />
          ))}
        </div>
      ) : (
        u &&
        t &&
        data && (
          <div className="grid gap-6">
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
              <Stat label="模型调用" value={formatNumber(t.calls)} sub={t.errors ? `失败 ${t.errors} 次（${((t.errors / t.calls) * 100).toFixed(1)}%）` : "无失败"} />
              <Stat label="输入 Token" value={compact(t.inputTokens)} sub={`缓存写入 ${compact(t.cacheWriteTokens)}${t.images ? ` · 图片 ${t.images} 张` : ""}`} />
              <Stat label="缓存命中率" value={`${hitRate(t)}%`} sub={`命中 ${compact(t.cacheReadTokens)} token`} />
              <Stat label="输出 Token" value={compact(t.outputTokens)} sub={`平均耗时 ${(t.avgLatencyMs / 1000).toFixed(1)} 秒`} />
            </div>

            {t.calls === 0 ? (
              <Card className="py-0">
                <EmptyState title="这段时间没有调用记录" />
              </Card>
            ) : (
              <>
                {data.days > 1 && (
                  <Card className="py-0">
                    <CardContent className="grid gap-4 p-5">
                      <div>
                        <h2 className="text-[15px] font-semibold">每日 Token（输入 + 输出）</h2>
                        <p className="text-[13px] text-muted-foreground">悬停查看当天调用次数与缓存命中率。</p>
                      </div>
                      <DailyChart days={u.days} from={data.from} span={data.days} />
                    </CardContent>
                  </Card>
                )}

                <div className="grid gap-4 xl:grid-cols-2">
                  <GroupTable title="按用途" rows={u.byPurpose} label={(k) => PURPOSE_LABEL[k] ?? k} />
                  <GroupTable title="按模型" rows={u.byModel} label={(k) => k} />
                </div>

                <Card className="py-0">
                  <div className="px-5 pt-4 pb-2">
                    <div className="text-[15px] font-semibold">工具调用</div>
                    <p className="text-[13px] text-muted-foreground">使用率 = 用到该工具的推理轮数 ÷ 客服回复与主动回复的总轮数（{formatNumber(runs)}）。</p>
                  </div>
                  {u.tools.length === 0 ? (
                    <p className="px-5 pb-5 text-[13px] text-muted-foreground">这段时间模型没有调用工具（预检索已经提供了足够的资料）。</p>
                  ) : (
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead className="pl-5">工具</TableHead>
                          <TableHead className="text-right">调用次数</TableHead>
                          <TableHead className="text-right">涉及轮数</TableHead>
                          <TableHead className="pr-5 text-right">使用率</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {u.tools.map((x) => (
                          <TableRow key={x.tool}>
                            <TableCell className="pl-5 font-mono text-[13px]">{x.tool}</TableCell>
                            <TableCell className="text-right font-mono tabular-nums">{formatNumber(x.calls)}</TableCell>
                            <TableCell className="text-right font-mono tabular-nums">{formatNumber(x.runs)}</TableCell>
                            <TableCell className="pr-5 text-right font-mono tabular-nums">{runs ? Math.round((x.runs / runs) * 100) : 0}%</TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  )}
                </Card>

                {u.errors.length > 0 && (
                  <Card className="py-0">
                    <div className="flex items-center gap-2 px-5 pt-4 pb-2 text-[15px] font-semibold">
                      <AlertTriangleIcon className="size-4 text-crit" />
                      最近失败（{u.errors.length}）
                    </div>
                    <div className="divide-y">
                      {u.errors.map((e, i) => (
                        <div key={i} className="grid gap-1 px-5 py-3 text-[13px]">
                          <div className="flex flex-wrap gap-x-3 text-xs text-muted-foreground">
                            <span className="font-mono tabular-nums">{formatTime(e.at)}</span>
                            <span>{PURPOSE_LABEL[e.purpose] ?? e.purpose}</span>
                            <span className="font-mono">{e.model}</span>
                            <span className="font-mono tabular-nums">{(e.latencyMs / 1000).toFixed(1)}s</span>
                          </div>
                          <p className="font-mono text-xs break-all">{e.error}</p>
                        </div>
                      ))}
                    </div>
                  </Card>
                )}
              </>
            )}
          </div>
        )
      )}
    </>
  )
}
