"use client"

import { ArrowLeftIcon, ArrowRightIcon, CheckIcon, KeyRoundIcon } from "lucide-react"
import { useRouter } from "next/navigation"
import { useEffect, useState } from "react"
import { toast } from "sonner"

import { Field, Kicker, Logo, ThemeToggle } from "@/components/common"
import { BrandForm, LLMForm, QQBotForm, TelegramForm } from "@/components/settings-forms"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { api } from "@/lib/api"
import type { Brand, LLMSettings, QQBotSettings, TelegramSettings } from "@/lib/types"
import { useDelayedPending } from "@/lib/use-delayed-pending"
import { cn } from "@/lib/utils"

const STEPS = [
  { key: "code", title: "初始化口令", desc: "证明你是这台服务器的管理者" },
  { key: "admin", title: "管理员账号", desc: "用于登录管理后台" },
  { key: "brand", title: "品牌信息", desc: "机器人以什么身份服务用户" },
  { key: "llm", title: "大模型", desc: "直连 OpenAI 兼容或 Anthropic 接口" },
  { key: "channels", title: "接入渠道", desc: "可稍后在设置页配置" },
] as const

export default function SetupPage() {
  const router = useRouter()
  const [step, setStep] = useState(0)
  const [code, setCode] = useState("")
  const [admin, setAdmin] = useState({ username: "admin", password: "", confirm: "" })
  const [brand, setBrand] = useState<Brand>({ name: "知微", description: "", supportUrl: "" })
  const [llm, setLLM] = useState<LLMSettings>({
    protocol: "openai",
    baseUrl: "https://api.openai.com/v1",
    apiKey: "",
    model: "",
    temperature: 0.3,
    maxTokens: 1024,
    timeoutSec: 60,
  })
  const [qqbot, setQQBot] = useState<QQBotSettings>({
    enabled: false, appId: "", appSecret: "", mode: "websocket", sandbox: false,
    group: true, c2c: true, guild: false, guildDm: false, groupAll: false, atUser: false,
  })
  const [telegram, setTelegram] = useState<TelegramSettings>({ enabled: false, botToken: "", apiBase: "https://api.telegram.org", private: true })
  const { pending, track } = useDelayedPending()

  useEffect(() => {
    api.get<{ completed: boolean }>("/api/setup/status", { silent: true }).then((s) => {
      if (s.completed) router.replace("/login")
    }).catch(() => {})
  }, [router])

  // 每步的本地校验；返回错误文案或 null
  const stepError = (): string | null => {
    switch (STEPS[step].key) {
      case "code":
        return /^\d{8}$/.test(code.trim()) ? null : "请输入 8 位数字口令"
      case "admin":
        if (admin.username.trim().length < 3) return "用户名至少 3 个字符"
        if (admin.password.length < 8) return "密码至少 8 位"
        return admin.password === admin.confirm ? null : "两次输入的密码不一致"
      case "brand":
        return brand.name.trim() ? null : "请填写品牌名称"
      case "llm":
        return llm.apiKey && llm.model && llm.baseUrl ? null : "请填写 Base URL、API Key 与模型"
      case "channels":
        if (qqbot.enabled && (!qqbot.appId || !qqbot.appSecret)) return "启用 QQ 机器人需要 AppID 与 AppSecret"
        if (telegram.enabled && !telegram.botToken) return "启用 Telegram 需要 Bot Token"
        return null
    }
  }
  const error = stepError()
  const last = step === STEPS.length - 1

  const next = () => {
    if (error) {
      toast.error(error)
      return
    }
    if (!last) {
      setStep(step + 1)
      return
    }
    track(async () => {
      await api.post(
        "/api/setup/complete",
        { admin: { username: admin.username.trim(), password: admin.password }, brand, llm, qqbot, telegram },
        { headers: { "X-Setup-Code": code.trim() } }
      )
      toast.success("初始化完成，请使用管理员账号登录")
      router.replace("/login")
    }).catch(() => {})
  }

  return (
    <div className="min-h-screen">
      <header className="mx-auto flex max-w-5xl items-center justify-between px-4 py-5 sm:px-6">
        <Logo />
        <ThemeToggle />
      </header>
      <main className="mx-auto grid max-w-5xl gap-8 px-4 pb-20 sm:px-6 md:grid-cols-[240px_minmax(0,1fr)]">
        <aside>
          <Kicker className="text-primary dark:text-[var(--ring)]">Setup · 首次启动</Kicker>
          <h1 className="mt-2 text-[27px] font-[650] tracking-[-0.02em]">初始化知微</h1>
          <p className="mt-2 text-sm text-muted-foreground">服务器只需要一个数据库连接串，其余配置都在这里完成并保存到 PostgreSQL，之后可随时在后台修改。</p>
          {/* 步骤条（知微设计系统.html「导航 · 步骤条」） */}
          <ol className="mt-8 hidden flex-col gap-1 md:flex">
            {STEPS.map((s, i) => (
              <li key={s.key} className={cn("flex items-start gap-3 rounded-lg px-2 py-2", i === step && "bg-muted")}>
                <span
                  className={cn(
                    "mt-0.5 grid size-6 shrink-0 place-items-center rounded-full border text-xs font-medium tabular-nums",
                    i < step && "border-primary bg-primary text-primary-foreground",
                    i === step && "border-primary text-primary dark:border-[var(--ring)] dark:text-[var(--ring)]",
                    i > step && "text-muted-foreground"
                  )}
                >
                  {i < step ? <CheckIcon className="size-3.5" /> : i + 1}
                </span>
                <span>
                  <span className={cn("block text-[13.5px] font-medium", i > step ? "text-muted-foreground" : "text-foreground")}>{s.title}</span>
                  <span className="block text-xs text-muted-foreground">{s.desc}</span>
                </span>
              </li>
            ))}
          </ol>
        </aside>

        <Card className="py-0">
          <CardContent className="p-6 sm:p-8">
            <Kicker>
              步骤 {step + 1} / {STEPS.length}
            </Kicker>
            <h2 className="mt-1 mb-6 text-xl font-semibold">{STEPS[step].title}</h2>

            {STEPS[step].key === "code" && (
              <div className="grid gap-5">
                <div className="flex gap-3 rounded-xl border bg-muted/40 p-4 text-[13px] text-muted-foreground">
                  <KeyRoundIcon className="mt-0.5 size-4 shrink-0 text-primary dark:text-[var(--ring)]" />
                  <p>
                    服务端启动时会在日志中打印一行 <code className="font-mono text-foreground">setupCode=xxxxxxxx</code>。口令只在本次进程有效，重启后会更换。这一步防止实例刚暴露到网络上就被他人抢先初始化。
                  </p>
                </div>
                <Field label="初始化口令" htmlFor="setup-code">
                  <Input id="setup-code" className="font-mono tracking-[0.3em]" inputMode="numeric" maxLength={8} value={code} onChange={(e) => setCode(e.target.value)} placeholder="00000000" autoFocus />
                </Field>
              </div>
            )}

            {STEPS[step].key === "admin" && (
              <div className="grid gap-5">
                <Field label="用户名" htmlFor="admin-user">
                  <Input id="admin-user" value={admin.username} onChange={(e) => setAdmin({ ...admin, username: e.target.value })} autoComplete="username" />
                </Field>
                <Field label="密码" hint="至少 8 位" htmlFor="admin-pass">
                  <Input id="admin-pass" type="password" value={admin.password} onChange={(e) => setAdmin({ ...admin, password: e.target.value })} autoComplete="new-password" />
                </Field>
                <Field label="确认密码" htmlFor="admin-confirm">
                  <Input id="admin-confirm" type="password" value={admin.confirm} onChange={(e) => setAdmin({ ...admin, confirm: e.target.value })} autoComplete="new-password" />
                </Field>
              </div>
            )}

            {STEPS[step].key === "brand" && <BrandForm value={brand} onChange={setBrand} />}
            {STEPS[step].key === "llm" && (
              <LLMForm value={llm} onChange={setLLM} testUrl="/api/setup/llm-test" testHeaders={{ "X-Setup-Code": code.trim() }} />
            )}
            {STEPS[step].key === "channels" && (
              <div className="grid gap-8">
                <section>
                  <Kicker className="mb-3">QQ 开放平台</Kicker>
                  <QQBotForm value={qqbot} onChange={setQQBot} />
                </section>
                <section className="border-t pt-6">
                  <Kicker className="mb-3">Telegram</Kicker>
                  <TelegramForm value={telegram} onChange={setTelegram} />
                </section>
                <p className="rounded-xl border border-dashed p-4 text-[13px] text-muted-foreground">企业微信等渠道已预留接口，后续版本开放。</p>
              </div>
            )}

            <div className="mt-8 flex items-center justify-between border-t pt-6">
              <Button variant="ghost" disabled={step === 0} onClick={() => setStep(step - 1)}>
                <ArrowLeftIcon />
                上一步
              </Button>
              <Button onClick={next} disabled={pending}>
                {last ? (pending ? "正在初始化…" : "完成初始化") : "下一步"}
                {!last && <ArrowRightIcon />}
              </Button>
            </div>
          </CardContent>
        </Card>
      </main>
    </div>
  )
}
