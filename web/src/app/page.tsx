"use client"

import { useRouter } from "next/navigation"
import { useEffect } from "react"

import { Logo } from "@/components/common"
import { api } from "@/lib/api"

// 入口分流：未初始化 → 向导；已初始化 → 后台（未登录由后台布局再跳登录页）
export default function Home() {
  const router = useRouter()
  useEffect(() => {
    api
      .get<{ completed: boolean }>("/api/setup/status", { silent: true })
      .then((s) => router.replace(s.completed ? "/admin" : "/setup"))
      .catch(() => router.replace("/admin"))
  }, [router])
  return (
    <main className="grid min-h-screen place-items-center">
      <Logo />
    </main>
  )
}
