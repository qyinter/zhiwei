"use client"

import { ChevronLeftIcon, ChevronRightIcon, MoonIcon, SunIcon } from "lucide-react"
import { useTheme } from "next-themes"
import { useEffect, useState } from "react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
import { cn } from "@/lib/utils"

/** 品牌字标：「知微」+ 品牌蓝终端光标（呼应设计系统的 rail brand） */
export function Logo({ className }: { className?: string }) {
  return (
    <span className={cn("inline-flex items-baseline gap-0.5 text-[22px] font-bold tracking-tight text-foreground", className)}>
      知<span className="text-primary dark:text-[var(--ring)]">微</span>
      <span aria-hidden className="ml-1 inline-block h-[18px] w-2 translate-y-0.5 animate-pulse bg-primary dark:bg-[var(--ring)]" />
    </span>
  )
}

export function ThemeToggle() {
  const { resolvedTheme, setTheme } = useTheme()
  const [mounted, setMounted] = useState(false)
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- SSR 水合标记：服务端渲染不出主题值
    setMounted(true)
  }, [])
  return (
    <Button
      aria-label="切换明暗主题"
      size="icon-sm"
      variant="ghost"
      onClick={() => setTheme(resolvedTheme === "dark" ? "light" : "dark")}
    >
      {mounted && resolvedTheme === "dark" ? <MoonIcon /> : <SunIcon />}
    </Button>
  )
}

export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string
  description?: string
  actions?: React.ReactNode
}) {
  return (
    <div className="mb-6 flex flex-wrap items-end justify-between gap-4">
      <div className="min-w-0">
        <h1 className="text-[27px] font-[650] tracking-[-0.02em] text-foreground">{title}</h1>
        {description && <p className="mt-1 max-w-[62ch] text-sm text-muted-foreground">{description}</p>}
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  )
}

/** 表单字段：标签在上，提示 / 错误在下（知微设计系统.html「输入与表单」） */
export function Field({
  label,
  hint,
  error,
  htmlFor,
  children,
  className,
}: {
  label: string
  hint?: React.ReactNode
  error?: string
  htmlFor?: string
  children: React.ReactNode
  className?: string
}) {
  return (
    <div className={cn("flex flex-col gap-1.75", className)}>
      <Label htmlFor={htmlFor} className="text-[13px] font-medium text-foreground/80">
        {label}
      </Label>
      {children}
      {error ? (
        <p className="text-xs text-crit">{error}</p>
      ) : (
        hint && <p className="text-xs text-muted-foreground">{hint}</p>
      )}
    </div>
  )
}

/** 小号等宽大写标签（kicker / 分组名） */
export function Kicker({ children, className }: { children: React.ReactNode; className?: string }) {
  return (
    <div className={cn("font-mono text-[11px] tracking-[0.06em] text-muted-foreground uppercase", className)}>
      {children}
    </div>
  )
}

export function StatusDot({ tone }: { tone: "ok" | "warn" | "crit" | "neutral" }) {
  const color = { ok: "bg-ok", warn: "bg-warn", crit: "bg-crit", neutral: "bg-muted-foreground/50" }[tone]
  return <span aria-hidden className={cn("inline-block size-1.5 rounded-full", color)} />
}

export function ConvStatusBadge({ status }: { status: string }) {
  return status === "handoff" ? (
    <Badge variant="warn">
      <StatusDot tone="warn" />
      人工接待
    </Badge>
  ) : (
    <Badge variant="ok">
      <StatusDot tone="ok" />
      自动回复
    </Badge>
  )
}

/** 「第 x/y 页 · 共 n 条」翻页条，超过一页才显示（全局约定：每页 20 条） */
export function Pager({
  page,
  pageSize,
  total,
  onChange,
}: {
  page: number
  pageSize: number
  total: number
  onChange: (p: number) => void
}) {
  const pages = Math.max(1, Math.ceil(total / pageSize))
  if (pages <= 1) return null
  return (
    <div className="flex items-center justify-between gap-3 px-1 pt-4 text-[13px] text-muted-foreground">
      <span className="tabular-nums">
        第 {page}/{pages} 页 · 共 {total} 条
      </span>
      <div className="flex gap-1.5">
        <Button size="icon-sm" variant="outline" disabled={page <= 1} onClick={() => onChange(page - 1)} aria-label="上一页">
          <ChevronLeftIcon />
        </Button>
        <Button size="icon-sm" variant="outline" disabled={page >= pages} onClick={() => onChange(page + 1)} aria-label="下一页">
          <ChevronRightIcon />
        </Button>
      </div>
    </div>
  )
}

export function EmptyState({ icon, title, description, action }: { icon?: React.ReactNode; title: string; description?: string; action?: React.ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 px-6 py-14 text-center">
      {icon && <div className="mb-1 grid size-11 place-items-center rounded-xl bg-muted text-muted-foreground">{icon}</div>}
      <div className="text-[15px] font-medium text-foreground">{title}</div>
      {description && <p className="max-w-[40ch] text-[13px] text-muted-foreground">{description}</p>}
      {action && <div className="mt-2">{action}</div>}
    </div>
  )
}

export function formatTime(iso: string) {
  const d = new Date(iso)
  const now = new Date()
  const pad = (n: number) => String(n).padStart(2, "0")
  const hm = `${pad(d.getHours())}:${pad(d.getMinutes())}`
  if (d.toDateString() === now.toDateString()) return hm
  return `${d.getMonth() + 1}-${pad(d.getDate())} ${hm}`
}

export function formatNumber(n: number) {
  return n.toLocaleString("zh-CN")
}
