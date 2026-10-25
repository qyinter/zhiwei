"use client"

import { useRouter } from "next/navigation"
import { useState } from "react"

import { Field, Kicker, Logo, ThemeToggle } from "@/components/common"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { api } from "@/lib/api"
import { useDelayedPending } from "@/lib/use-delayed-pending"

export default function LoginPage() {
  const router = useRouter()
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const { pending, track } = useDelayedPending()

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    track(async () => {
      await api.post("/api/auth/login", { username, password })
      // 只接受站内相对路径，防开放重定向
      const redirect = new URLSearchParams(window.location.search).get("redirect")
      router.replace(redirect && redirect.startsWith("/") && !redirect.startsWith("//") ? redirect : "/admin")
    }).catch(() => {})
  }

  return (
    <div className="grid min-h-screen place-items-center px-4">
      <div className="absolute top-5 right-5">
        <ThemeToggle />
      </div>
      <div className="w-full max-w-sm">
        <div className="mb-6 text-center">
          <Logo />
          <Kicker className="mt-2">多渠道 AI 客服 · 管理后台</Kicker>
        </div>
        <Card className="py-0">
          <CardContent className="p-6">
            <form className="grid gap-5" onSubmit={submit}>
              <Field label="用户名" htmlFor="login-user">
                <Input id="login-user" value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" autoFocus />
              </Field>
              <Field label="密码" htmlFor="login-pass">
                <Input id="login-pass" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" />
              </Field>
              <Button type="submit" size="lg" disabled={pending || !username || !password}>
                {pending ? "登录中…" : "登录"}
              </Button>
            </form>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
