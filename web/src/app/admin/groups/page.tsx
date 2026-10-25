"use client"

import { PlusIcon, SlidersHorizontalIcon, UsersRoundIcon } from "lucide-react"
import Link from "next/link"
import { useCallback, useEffect, useState } from "react"
import { toast } from "sonner"

import { EmptyState, Field, formatTime, PageHeader } from "@/components/common"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api } from "@/lib/api"
import { CHANNEL_LABEL, formatDuration, type GroupEffective, type GroupPolicy, type GroupRow, type GroupsData } from "@/lib/types"
import { useDelayedLoading, useDelayedPending } from "@/lib/use-delayed-pending"
import { cn } from "@/lib/utils"

type Switchable = "proactive" | "welcome" | "joinReview" | "reflect" | "topics"

const FEATURES: { key: Switchable; label: string; hint: string; qqOnly?: boolean }[] = [
  { key: "proactive", label: "主动回复", hint: "有人提问长时间没人答时补位" },
  { key: "welcome", label: "入群欢迎", hint: "新成员入群发欢迎语" },
  { key: "joinReview", label: "入群审核", hint: "按核对接口自动同意 / 拒绝加群申请", qqOnly: true },
  { key: "reflect", label: "反思沉淀", hint: "从群主 / 管理员的答复里提炼知识" },
  { key: "topics", label: "问题排行", hint: "统计这个群里大家常问的问题" },
]

const emptyPolicy = (chat: string): GroupPolicy => ({
  chat,
  note: "",
  proactive: null,
  silenceSec: null,
  welcome: null,
  joinReview: null,
  reflect: null,
  topics: null,
})

/** 策略里没有任何单独设置（全部跟随全局、也没有备注）时不必保存 */
function isEmpty(p: GroupPolicy) {
  return !p.note.trim() && p.silenceSec == null && FEATURES.every((f) => p[f.key] == null)
}

function OnOff({ on, own }: { on: boolean; own: boolean }) {
  return (
    <span className="inline-flex items-center gap-1">
      <Badge variant={on ? "ok" : "neutral"}>{on ? "开" : "关"}</Badge>
      {/* 单独设置的标一个品牌色圆点，跟随全局的不标 */}
      <span aria-label={own ? "单独设置" : "跟随全局"} title={own ? "单独设置" : "跟随全局"} className={cn("size-1.5 rounded-full", own ? "bg-primary" : "bg-transparent")} />
    </span>
  )
}

type Draft = { row: GroupRow | null; policy: GroupPolicy; silence: string }

function triValue(v: boolean | null) {
  return v == null ? "inherit" : v ? "on" : "off"
}

export default function GroupsPage() {
  const [data, setData] = useState<GroupsData | null>(null)
  const [draft, setDraft] = useState<Draft | null>(null)
  const { loading, track } = useDelayedLoading()
  const save = useDelayedPending()

  const load = useCallback(() => track(() => api.get<GroupsData>("/api/group-policies").then(setData)).catch(() => {}), [track])
  useEffect(() => {
    load()
  }, [load])

  const open = (row: GroupRow | null) => {
    const p = row?.policy ?? emptyPolicy(row ? `${row.channel}:${row.chatId}` : "")
    setDraft({ row, policy: { ...p }, silence: p.silenceSec == null ? "" : String(p.silenceSec) })
  }

  const submit = () => {
    if (!draft || !data) return
    const silence = draft.silence.trim()
    if (silence !== "" && !/^\d+$/.test(silence)) {
      toast.error("静默阈值请填整数秒，留空表示跟随全局")
      return
    }
    const p: GroupPolicy = { ...draft.policy, chat: draft.policy.chat.trim(), note: draft.policy.note.trim(), silenceSec: silence === "" ? null : Number(silence) }
    const others = data.list.filter((r) => r.policy && r.policy.chat !== p.chat).map((r) => r.policy as GroupPolicy)
    const policies = isEmpty(p) ? others : [...others, p]
    save
      .track(async () => {
        await api.put("/api/settings", { groups: { policies } })
        toast.success(isEmpty(p) ? "已恢复为全部跟随全局" : "已保存，下一轮扫描起生效")
        setDraft(null)
        await load()
      })
      .catch(() => {})
  }

  const g = data?.global
  const isQQ = (draft?.row?.channel ?? draft?.policy.chat.split(":")[0]) === "qqbot"

  return (
    <>
      <PageHeader
        title="群管理"
        description="机器人见过的群，以及每个群各项功能是否开启。默认跟随系统设置里的全局开关；某个群想单独开或关，点右侧「设置」。"
        actions={
          <Button variant="outline" size="sm" onClick={() => open(null)}>
            <PlusIcon />
            按群 ID 添加
          </Button>
        }
      />
      {data && !data.allowAllChats && (
        <div className="mb-5 rounded-xl border px-4 py-3 text-[13px] text-muted-foreground">
          已开启群白名单：不在白名单里的群，机器人不响应，以下功能也都不生效。白名单在{" "}
          <Link className="text-primary underline-offset-4 hover:underline" href="/admin/settings?tab=agent">
            系统设置 → 客服策略
          </Link>{" "}
          里改。
        </div>
      )}
      {g && (
        <div className="mb-4 flex flex-wrap gap-x-5 gap-y-1.5 text-[13px] text-muted-foreground">
          <span className="font-medium text-foreground">全局：</span>
          {FEATURES.map((f) => (
            <span key={f.key}>
              {f.label} {g[f.key] ? "开" : "关"}
              {f.key === "proactive" && `（静默 ${formatDuration(g.silenceSec)}）`}
            </span>
          ))}
          <span className="inline-flex items-center gap-1.5">
            <span className="size-1.5 rounded-full bg-primary" />= 单独设置
          </span>
        </div>
      )}
      {loading && !data ? (
        <Skeleton className="h-64 rounded-2xl" />
      ) : (
        data &&
        (data.list.length === 0 ? (
          <Card className="py-0">
            <EmptyState icon={<UsersRoundIcon className="size-5" />} title="还没有群" description="机器人在群里收到消息后，群会出现在这里。也可以按群 ID 手动添加。" />
          </Card>
        ) : (
          <Card className="py-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="pl-5">群</TableHead>
                  {FEATURES.map((f) => (
                    <TableHead key={f.key}>{f.label}</TableHead>
                  ))}
                  <TableHead className="text-right">最近消息</TableHead>
                  <TableHead className="pr-5" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.list.map((r) => {
                  const e: GroupEffective = r.effective
                  return (
                    <TableRow key={`${r.channel}:${r.chatId}`} className={cn(!r.allowed && "opacity-55")}>
                      <TableCell className="max-w-64 pl-5">
                        <div className="flex items-center gap-2">
                          <Badge variant="neutral">{CHANNEL_LABEL[r.channel] ?? r.channel}</Badge>
                          <span className="truncate">{r.policy?.note || r.title || <span className="font-mono text-xs">{r.chatId}</span>}</span>
                        </div>
                        {(r.policy?.note || r.title) && <div className="mt-0.5 truncate font-mono text-xs text-muted-foreground">{r.chatId}</div>}
                        {!r.allowed && <div className="mt-0.5 text-xs text-muted-foreground">不在白名单</div>}
                      </TableCell>
                      {FEATURES.map((f) => (
                        <TableCell key={f.key}>
                          {f.qqOnly && r.channel !== "qqbot" ? (
                            <span className="text-xs text-muted-foreground">—</span>
                          ) : (
                            <span className="inline-flex items-center gap-1.5">
                              <OnOff on={e[f.key]} own={r.policy?.[f.key] != null || (f.key === "proactive" && r.policy?.silenceSec != null)} />
                              {f.key === "proactive" && e.proactive && <span className="font-mono text-xs text-muted-foreground">{formatDuration(e.silenceSec)}</span>}
                            </span>
                          )}
                        </TableCell>
                      ))}
                      <TableCell className="text-right font-mono text-xs text-muted-foreground tabular-nums">{r.lastAt ? formatTime(r.lastAt) : "—"}</TableCell>
                      <TableCell className="pr-5 text-right">
                        <Button size="sm" variant="ghost" onClick={() => open(r)}>
                          <SlidersHorizontalIcon />
                          设置
                        </Button>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </Card>
        ))
      )}

      <Dialog open={draft != null} onOpenChange={(o) => !o && setDraft(null)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{draft?.row ? draft.row.title || draft.row.chatId : "按群 ID 添加"}</DialogTitle>
            <DialogDescription>选「跟随全局」的项随系统设置变化；单独开启的功能，即使全局关闭也会在这个群生效。</DialogDescription>
          </DialogHeader>
          {draft && g && (
            <div className="grid gap-4">
              {!draft.row && (
                <Field label="群标识" hint="格式 渠道:群ID，例如 qqbot:8F3A…（群 openid）或 telegram:-100123456" htmlFor="gp-chat">
                  <Input id="gp-chat" className="font-mono" value={draft.policy.chat} onChange={(e) => setDraft({ ...draft, policy: { ...draft.policy, chat: e.target.value } })} />
                </Field>
              )}
              <Field label="备注" hint="后台显示用，优先于群名显示；QQ 群名自动获取，取不到时可在这里填" htmlFor="gp-note">
                <Input id="gp-note" value={draft.policy.note} maxLength={64} onChange={(e) => setDraft({ ...draft, policy: { ...draft.policy, note: e.target.value } })} />
              </Field>
              <div className="grid gap-3 sm:grid-cols-2">
                {FEATURES.filter((f) => !f.qqOnly || isQQ).map((f) => (
                  <Field key={f.key} label={f.label} hint={f.hint}>
                    <Select
                      value={triValue(draft.policy[f.key])}
                      onValueChange={(v) => setDraft({ ...draft, policy: { ...draft.policy, [f.key]: v === "inherit" ? null : v === "on" } })}
                    >
                      <SelectTrigger className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="inherit">跟随全局（{g[f.key] ? "开" : "关"}）</SelectItem>
                        <SelectItem value="on">开启</SelectItem>
                        <SelectItem value="off">关闭</SelectItem>
                      </SelectContent>
                    </Select>
                  </Field>
                ))}
                <Field label="主动回复静默阈值（秒）" hint={`留空跟随全局（${g.silenceSec} 秒）`} htmlFor="gp-silence">
                  <Input id="gp-silence" inputMode="numeric" className="font-mono tabular-nums" value={draft.silence} onChange={(e) => setDraft({ ...draft, silence: e.target.value })} />
                </Field>
              </div>
            </div>
          )}
          <DialogFooter>
            <Button variant="ghost" onClick={() => setDraft(null)}>
              取消
            </Button>
            <Button onClick={submit} disabled={save.pending || !draft?.policy.chat.trim()}>
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
