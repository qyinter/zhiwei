"use client"

import { SaveIcon } from "lucide-react"
import { useRouter } from "next/navigation"
import { useEffect, useState } from "react"
import { toast } from "sonner"

import { Field, Kicker, PageHeader } from "@/components/common"
import {
  AgentForm,
  BrandForm,
  EmbeddingForm,
  ModelCatalogForm,
  JoinReviewForm,
  LLMForm,
  ProactiveForm,
  QQBotForm,
  ReflectForm,
  TelegramForm,
  TopicsForm,
} from "@/components/settings-forms"
import { WelcomeForm } from "@/components/welcome-editor"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { SegmentedTabs } from "@/components/ui/segmented-tabs"
import { Skeleton } from "@/components/ui/skeleton"
import { api } from "@/lib/api"
import type { AllSettings } from "@/lib/types"
import { useDelayedPending } from "@/lib/use-delayed-pending"

type Tab = "llm" | "agent" | "embedding" | "catalog" | "groupops" | "reflect" | "brand" | "qqbot" | "telegram" | "wecom" | "account"

const TABS: { key: Tab; label: string }[] = [
  { key: "llm", label: "大模型" },
  { key: "agent", label: "客服策略" },
  { key: "embedding", label: "向量检索" },
  { key: "catalog", label: "模型目录" },
  { key: "groupops", label: "群运营" },
  { key: "reflect", label: "反思沉淀" },
  { key: "brand", label: "品牌" },
  { key: "qqbot", label: "QQ 机器人" },
  { key: "telegram", label: "Telegram" },
  { key: "wecom", label: "企业微信" },
  { key: "account", label: "账号" },
]

function PasswordForm() {
  const router = useRouter()
  const [form, setForm] = useState({ oldPassword: "", newPassword: "", confirm: "" })
  const { pending, track } = useDelayedPending()
  const submit = () => {
    if (form.newPassword !== form.confirm) {
      toast.error("两次输入的新密码不一致")
      return
    }
    track(async () => {
      await api.post("/api/auth/password", { oldPassword: form.oldPassword, newPassword: form.newPassword })
      toast.success("密码已修改，请重新登录")
      router.replace("/login")
    }).catch(() => {})
  }
  return (
    <div className="grid max-w-md gap-5">
      <Field label="原密码">
        <Input type="password" autoComplete="current-password" value={form.oldPassword} onChange={(e) => setForm({ ...form, oldPassword: e.target.value })} />
      </Field>
      <Field label="新密码" hint="至少 8 位；修改后所有登录会话失效">
        <Input type="password" autoComplete="new-password" value={form.newPassword} onChange={(e) => setForm({ ...form, newPassword: e.target.value })} />
      </Field>
      <Field label="确认新密码">
        <Input type="password" autoComplete="new-password" value={form.confirm} onChange={(e) => setForm({ ...form, confirm: e.target.value })} />
      </Field>
      <div>
        <Button onClick={submit} disabled={pending || !form.oldPassword || !form.newPassword}>
          修改密码
        </Button>
      </div>
    </div>
  )
}

export default function SettingsPage() {
  const [tab, setTab] = useState<Tab>("llm")
  const [cfg, setCfg] = useState<AllSettings | null>(null)
  const { pending, track } = useDelayedPending()

  useEffect(() => {
    api
      .get<AllSettings>("/api/settings")
      .then((c) => {
        setCfg(c)
        // 其他页面可用 ?tab=xxx 直达某个分区
        const t = new URLSearchParams(window.location.search).get("tab")
        if (TABS.some((x) => x.key === t)) setTab(t as Tab)
      })
      .catch(() => {})
  }, [])

  const save = () =>
    track(async () => {
      if (!cfg) return
      // 按群设置在「群管理」页编辑，这里不回写，免得用打开页面时的旧值覆盖
      const rest: Partial<AllSettings> = { ...cfg }
      delete rest.groups
      setCfg(await api.put<AllSettings>("/api/settings", rest))
      toast.success("已保存，渠道与模型已按新配置重新装配")
    }).catch(() => {})

  return (
    <>
      <PageHeader
        title="系统设置"
        description="全部配置保存在 PostgreSQL，保存后即时生效，无需重启服务。"
        actions={
          tab !== "account" && (
            <Button onClick={save} disabled={pending || !cfg}>
              <SaveIcon />
              {pending ? "保存中…" : "保存设置"}
            </Button>
          )
        }
      />
      <SegmentedTabs<Tab> className="mb-5 max-w-full" value={tab} onChange={setTab} items={TABS} />
      <Card className="py-0">
        <CardContent className="p-6 sm:p-8">
          {!cfg ? (
            <div className="grid gap-4">
              <Skeleton className="h-10" />
              <Skeleton className="h-10" />
              <Skeleton className="h-10" />
            </div>
          ) : (
            <div className="max-w-3xl">
              {tab === "llm" && <LLMForm value={cfg.llm} onChange={(llm) => setCfg({ ...cfg, llm })} testUrl="/api/settings/llm-test" />}
              {tab === "agent" && <AgentForm value={cfg.agent} onChange={(agent) => setCfg({ ...cfg, agent })} />}
              {tab === "groupops" && (
                <div className="grid gap-10">
                  <section className="grid gap-4">
                    <Kicker>主动回复</Kicker>
                    <ProactiveForm value={cfg.proactive} onChange={(proactive) => setCfg({ ...cfg, proactive })} />
                  </section>
                  <section className="grid gap-4">
                    <Kicker>问题排行</Kicker>
                    <TopicsForm value={cfg.topics} onChange={(topics) => setCfg({ ...cfg, topics })} />
                  </section>
                  <section className="grid gap-4">
                    <Kicker>入群欢迎</Kicker>
                    <WelcomeForm value={cfg.welcome} onChange={(welcome) => setCfg({ ...cfg, welcome })} brand={cfg.brand.name} />
                  </section>
                  <section className="grid gap-4">
                    <Kicker>入群审核（QQ）</Kicker>
                    <JoinReviewForm value={cfg.joinReview} onChange={(joinReview) => setCfg({ ...cfg, joinReview })} />
                  </section>
                </div>
              )}
              {tab === "embedding" && <EmbeddingForm value={cfg.embedding} onChange={(embedding) => setCfg({ ...cfg, embedding })} />}
              {tab === "catalog" && <ModelCatalogForm value={cfg.modelCatalog} onChange={(modelCatalog) => setCfg({ ...cfg, modelCatalog })} />}
              {tab === "reflect" && <ReflectForm value={cfg.reflect} onChange={(reflect) => setCfg({ ...cfg, reflect })} />}
              {tab === "brand" && <BrandForm value={cfg.brand} onChange={(brand) => setCfg({ ...cfg, brand })} />}
              {tab === "qqbot" && <QQBotForm value={cfg.qqbot} onChange={(qqbot) => setCfg({ ...cfg, qqbot })} />}
              {tab === "telegram" && <TelegramForm value={cfg.telegram} onChange={(telegram) => setCfg({ ...cfg, telegram })} />}
              {tab === "wecom" && (
                <div className="grid gap-3">
                  <Kicker>预留渠道</Kicker>
                  <p className="text-sm text-muted-foreground">
                    企业微信接入已在服务端预留适配器接口（<code className="font-mono">internal/channel/wecom</code>），配置项包括 CorpID、AgentID、Secret、Token 与 EncodingAESKey，将在后续版本开放。
                  </p>
                </div>
              )}
              {tab === "account" && <PasswordForm />}
            </div>
          )}
        </CardContent>
      </Card>
    </>
  )
}
