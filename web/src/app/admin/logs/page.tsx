"use client"

import { PauseIcon, PlayIcon, SearchIcon, Trash2Icon } from "lucide-react"
import { useCallback, useEffect, useRef, useState } from "react"

import { EmptyState, PageHeader } from "@/components/common"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { SegmentedTabs } from "@/components/ui/segmented-tabs"
import { api } from "@/lib/api"
import type { LogEntry } from "@/lib/types"
import { cn } from "@/lib/utils"

type Level = "" | "WARN" | "ERROR"

const LEVELS: { key: Level; label: string }[] = [
  { key: "", label: "全部" },
  { key: "WARN", label: "警告及以上" },
  { key: "ERROR", label: "仅错误" },
]

/** 页面最多保留的条数（与服务端缓冲同量级） */
const KEEP = 2000

function levelBadge(l: LogEntry["level"]) {
  switch (l) {
    case "ERROR":
      return <Badge variant="crit">错误</Badge>
    case "WARN":
      return <Badge variant="warn">警告</Badge>
    default:
      return <Badge variant="neutral">信息</Badge>
  }
}

function clock(iso: string) {
  const d = new Date(iso)
  const pad = (n: number, w = 2) => String(n).padStart(w, "0")
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

export default function LogsPage() {
  const [level, setLevel] = useState<Level>("")
  const [keyword, setKeyword] = useState("")
  const [query, setQuery] = useState("")
  const [paused, setPaused] = useState(false)
  const [list, setList] = useState<LogEntry[] | null>(null)
  // 增量拉取的游标；筛选条件变化时清零重拉
  const after = useRef(0)
  // 筛选条件的代次：换条件后，旧条件下还没返回的请求结果直接丢弃
  const gen = useRef(0)
  // 正在拉取的代次：同一代次不并发拉（网络慢时定时器会叠请求，结果重复追加）
  const inflight = useRef<number | null>(null)

  const poll = useCallback(async () => {
    const g = gen.current
    if (inflight.current === g) return
    inflight.current = g
    try {
      const qs = new URLSearchParams({ after: String(after.current) })
      if (level) qs.set("level", level)
      if (query) qs.set("q", query)
      const r = await api.get<{ list: LogEntry[]; latest: number }>(`/api/logs?${qs}`, { silent: true })
      if (g !== gen.current) return
      after.current = r.latest
      setList((cur) => {
        const last = cur?.at(-1)?.seq ?? 0
        const next = [...(cur ?? []), ...r.list.filter((e) => e.seq > last)]
        return next.length > KEEP ? next.slice(next.length - KEEP) : next
      })
    } finally {
      if (inflight.current === g) inflight.current = null
    }
  }, [level, query])

  useEffect(() => {
    gen.current += 1
    after.current = 0
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 换筛选条件先清空，再从头拉
    setList(null)
    poll().catch(() => {})
  }, [poll])

  useEffect(() => {
    if (paused) return
    const t = setInterval(() => poll().catch(() => {}), 3000)
    return () => clearInterval(t)
  }, [poll, paused])

  const shown = list ? [...list].reverse() : null

  return (
    <>
      <PageHeader
        title="运行日志"
        description="服务端最近 2000 条日志，只存在内存里，重启后清空；完整日志以服务进程的标准输出为准。每 3 秒自动刷新。"
        actions={
          <>
            <Button variant="outline" size="sm" onClick={() => setPaused((p) => !p)}>
              {paused ? <PlayIcon /> : <PauseIcon />}
              {paused ? "继续刷新" : "暂停刷新"}
            </Button>
            <Button variant="ghost" size="sm" onClick={() => setList([])}>
              <Trash2Icon />
              清屏
            </Button>
          </>
        }
      />
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <SegmentedTabs<Level> value={level} onChange={setLevel} items={LEVELS} />
        <form
          className="relative w-full sm:w-72"
          onSubmit={(e) => {
            e.preventDefault()
            setQuery(keyword.trim())
          }}
        >
          <SearchIcon className="absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input className="pl-9" placeholder="搜索消息或字段，回车生效" value={keyword} onChange={(e) => setKeyword(e.target.value)} onBlur={() => setQuery(keyword.trim())} />
        </form>
      </div>
      <Card className="py-0">
        {shown == null ? (
          <p className="px-5 py-10 text-center text-sm text-muted-foreground">加载中…</p>
        ) : shown.length === 0 ? (
          <EmptyState title="没有符合条件的日志" description={query || level ? "换个筛选条件试试。" : undefined} />
        ) : (
          <div className="max-h-[calc(100vh-260px)] min-h-80 divide-y overflow-y-auto font-mono text-[12.5px]">
            {shown.map((e) => (
              <div key={e.seq} className={cn("grid gap-1 px-4 py-2 sm:grid-cols-[120px_52px_minmax(0,1fr)] sm:gap-3", e.level === "ERROR" && "bg-crit-soft/40")}>
                <span className="text-muted-foreground tabular-nums">{clock(e.time)}</span>
                <span>{levelBadge(e.level)}</span>
                <span className="min-w-0 break-words">
                  <span className="font-sans text-[13px] text-foreground">{e.msg}</span>
                  {e.attrs.map((a) => (
                    <span key={a.key} className="ml-2 text-muted-foreground">
                      {a.key}=<span className="text-foreground/80">{a.value}</span>
                    </span>
                  ))}
                </span>
              </div>
            ))}
          </div>
        )}
      </Card>
    </>
  )
}
