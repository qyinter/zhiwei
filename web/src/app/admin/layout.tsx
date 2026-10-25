"use client"

import {
  BookOpenIcon,
  BrainCircuitIcon,
  ChartColumnIcon,
  FlaskConicalIcon,
  HeadsetIcon,
  LayoutDashboardIcon,
  ListOrderedIcon,
  LogOutIcon,
  MenuIcon,
  MessageCircleReplyIcon,
  MessagesSquareIcon,
  PuzzleIcon,
  ScrollTextIcon,
  SettingsIcon,
  UserCheckIcon,
  UsersRoundIcon,
} from "lucide-react"
import Link from "next/link"
import { usePathname, useRouter } from "next/navigation"
import { useEffect, useState } from "react"

import { Kicker, Logo, ThemeToggle } from "@/components/common"
import { Button } from "@/components/ui/button"
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"
import { api } from "@/lib/api"
import { cn } from "@/lib/utils"

const NAV = [
  { group: "运营", items: [
    { href: "/admin", label: "总览", icon: LayoutDashboardIcon },
    { href: "/admin/conversations", label: "会话", icon: MessagesSquareIcon },
    { href: "/admin/handoffs", label: "人工队列", icon: HeadsetIcon },
    { href: "/admin/topics", label: "问题排行", icon: ListOrderedIcon },
    { href: "/admin/usage", label: "用量统计", icon: ChartColumnIcon },
  ] },
  { group: "群运营", items: [
    { href: "/admin/groups", label: "群管理", icon: UsersRoundIcon },
    { href: "/admin/proactive", label: "主动回复", icon: MessageCircleReplyIcon },
    { href: "/admin/reflection", label: "反思沉淀", icon: BrainCircuitIcon },
    { href: "/admin/join-requests", label: "入群审核", icon: UserCheckIcon },
  ] },
  { group: "配置", items: [
    { href: "/admin/kb", label: "知识库", icon: BookOpenIcon },
    { href: "/admin/skills", label: "技能", icon: PuzzleIcon },
    { href: "/admin/playground", label: "体验台", icon: FlaskConicalIcon },
    { href: "/admin/settings", label: "系统设置", icon: SettingsIcon },
    { href: "/admin/logs", label: "运行日志", icon: ScrollTextIcon },
  ] },
]

function Nav({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = usePathname()
  return (
    <nav className="flex flex-col gap-0.5">
      {NAV.map((g) => (
        <div key={g.group} className="mb-2">
          <Kicker className="px-2.5 pt-3 pb-1.5 text-[10.5px] tracking-[0.1em]">{g.group}</Kicker>
          {g.items.map(({ href, label, icon: Icon }) => {
            const active = href === "/admin" ? pathname === href : pathname.startsWith(href)
            return (
              <Link
                key={href}
                href={href}
                onClick={onNavigate}
                className={cn(
                  "flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13.5px] transition-colors",
                  active ? "bg-accent-soft font-medium text-primary dark:text-[var(--ring)]" : "text-muted-foreground hover:bg-muted hover:text-foreground"
                )}
              >
                <Icon className="size-4" />
                {label}
              </Link>
            )
          })}
        </div>
      ))}
    </nav>
  )
}

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  const router = useRouter()
  const [user, setUser] = useState<{ username: string } | null>(null)
  const [open, setOpen] = useState(false)

  useEffect(() => {
    // 未初始化先去向导；未登录 api 层会自动跳登录页
    api
      .get<{ completed: boolean }>("/api/setup/status", { silent: true })
      .then((s) => {
        if (!s.completed) {
          router.replace("/setup")
          return
        }
        return api.get<{ username: string }>("/api/auth/me", { silent: true }).then(setUser)
      })
      .catch(() => {})
  }, [router])

  const logout = () => api.post("/api/auth/logout").finally(() => router.replace("/login"))

  return (
    <div className="min-h-screen md:grid md:grid-cols-[236px_minmax(0,1fr)]">
      <aside className="sticky top-0 hidden h-screen flex-col border-r bg-card px-4 py-6 md:flex">
        <div className="px-2.5">
          <Logo />
          <Kicker className="mt-1 mb-5">AI 客服 · 管理后台</Kicker>
        </div>
        {/* 菜单超出屏幕高度时在侧栏内部滚动：否则要滚整页才点得到下面的项，点完路由跳转回到页顶，看起来像侧栏「弹上去」 */}
        <div className="-mx-1 min-h-0 flex-1 overflow-y-auto px-1 pb-2">
          <Nav />
        </div>
        <div className="flex items-center justify-between gap-2 border-t px-1 pt-4">
          <span className="truncate text-[13px] text-muted-foreground">{user?.username ?? "…"}</span>
          <div className="flex">
            <ThemeToggle />
            <Button size="icon-sm" variant="ghost" onClick={logout} aria-label="退出登录">
              <LogOutIcon />
            </Button>
          </div>
        </div>
      </aside>

      <header className="sticky top-0 z-20 flex items-center justify-between border-b bg-background/85 px-4 py-3 backdrop-blur md:hidden">
        <Sheet open={open} onOpenChange={setOpen}>
          <SheetTrigger asChild>
            <Button size="icon-sm" variant="ghost" aria-label="打开导航">
              <MenuIcon />
            </Button>
          </SheetTrigger>
          <SheetContent side="left" className="w-64 overflow-y-auto px-4 py-6">
            <SheetTitle className="px-2.5">
              <Logo />
            </SheetTitle>
            <Nav onNavigate={() => setOpen(false)} />
          </SheetContent>
        </Sheet>
        <Logo className="text-lg" />
        <ThemeToggle />
      </header>

      <main className="min-w-0 px-4 py-6 sm:px-8 sm:py-10">
        <div className="mx-auto max-w-6xl">
          {user ? (
            children
          ) : (
            <div className="grid gap-4">
              <Skeleton className="h-9 w-48" />
              <Skeleton className="h-32 w-full rounded-2xl" />
            </div>
          )}
        </div>
      </main>
    </div>
  )
}
