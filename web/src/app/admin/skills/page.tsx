"use client"

import { ChevronRightIcon, FileTextIcon, PlusIcon, PuzzleIcon, SettingsIcon, Trash2Icon, UploadIcon, WrenchIcon } from "lucide-react"
import Link from "next/link"
import { useCallback, useEffect, useState } from "react"
import { toast } from "sonner"

import { EmptyState, formatTime, PageHeader } from "@/components/common"
import { formatSize } from "@/components/kb-import"
import { SkillImportDialog } from "@/components/skill-import"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { api } from "@/lib/api"
import type { AllSettings, BuiltinTool, ModelCatalogSettings, Skill, SkillDetail } from "@/lib/types"
import { useDelayedLoading, useDelayedPending } from "@/lib/use-delayed-pending"
import { cn } from "@/lib/utils"

type ListResp = { builtin: BuiltinTool[]; list: Skill[]; template: string }

/** editing：undefined = 关闭；0 = 新建；其余 = 技能 ID */
export default function SkillsPage() {
  const [data, setData] = useState<ListResp | null>(null)
  const [editing, setEditing] = useState<number | undefined>()
  const [importOpen, setImportOpen] = useState(false)
  const { loading, track } = useDelayedLoading()

  const [catalog, setCatalog] = useState<ModelCatalogSettings | null>(null)
  const load = useCallback(
    () =>
      track(() =>
        Promise.all([
          api.get<ListResp>("/api/skills").then(setData),
          api.get<AllSettings>("/api/settings", { silent: true }).then((s) => setCatalog(s.modelCatalog)),
        ])
      ).catch(() => {}),
    [track]
  )

  // 内置工具里由设置控制的开关（目前只有模型目录）：只提交这一个分区，其余分区服务端保持不变
  const toggleCatalog = (enabled: boolean) => {
    if (!catalog) return
    setCatalog({ ...catalog, enabled })
    api
      .put<AllSettings>("/api/settings", { modelCatalog: { ...catalog, enabled } })
      .then((s) => {
        setCatalog(s.modelCatalog)
        toast.success(enabled ? "已开启实时模型目录" : "已关闭实时模型目录")
      })
      .catch(() => setCatalog(catalog))
  }
  useEffect(() => {
    load()
  }, [load])

  const toggle = (k: Skill, enabled: boolean) => {
    setData((d) => d && { ...d, list: d.list.map((x) => (x.id === k.id ? { ...x, enabled } : x)) })
    api
      .patch(`/api/skills/${k.id}`, { enabled })
      .then(() => toast.success(enabled ? `已启用「${k.name}」` : `已停用「${k.name}」`))
      .catch(load)
  }

  // 编辑框按 onClose 依赖拉详情，回调必须稳定，否则父组件重渲染会重新拉取并冲掉未保存的修改
  const closeEditor = useCallback(() => setEditing(undefined), [])
  const savedEditor = useCallback(() => {
    setEditing(undefined)
    load()
  }, [load])

  const skills = data?.list ?? []
  const enabledCount = skills.filter((k) => k.enabled).length

  return (
    <>
      <PageHeader
        title="技能"
        description="上传 SKILL.md 技能包，教机器人处理特定类型的问题。模型先看技能的适用场景，用得上时才读取完整说明。"
        actions={
          <>
            <Button variant="outline" onClick={() => setImportOpen(true)}>
              <UploadIcon />
              上传技能
            </Button>
            <Button onClick={() => setEditing(0)}>
              <PlusIcon />
              新建技能
            </Button>
          </>
        }
      />

      <section className="grid gap-3">
        <div className="flex items-baseline justify-between gap-3">
          <h2 className="text-[15px] font-semibold">已安装技能</h2>
          {skills.length > 0 && (
            <span className="text-xs text-muted-foreground">
              {enabledCount} / {skills.length} 已启用
            </span>
          )}
        </div>
        {loading && !data ? (
          <div className="grid gap-3 md:grid-cols-2">
            {Array.from({ length: 2 }).map((_, i) => (
              <Skeleton key={i} className="h-32 rounded-2xl" />
            ))}
          </div>
        ) : skills.length === 0 ? (
          <Card>
            <EmptyState
              icon={<PuzzleIcon className="size-5" />}
              title="还没有技能"
              description="把一类问题的处理方法写成 SKILL.md，比如退款流程、报价口径、故障排查步骤，上传后机器人遇到这类问题会先读说明再回答。"
              action={
                <Button variant="outline" onClick={() => setImportOpen(true)}>
                  <UploadIcon />
                  上传技能
                </Button>
              }
            />
          </Card>
        ) : (
          <div className="grid gap-3 md:grid-cols-2">
            {skills.map((k) => (
              <Card key={k.id} className={cn("gap-0 py-0 transition-colors hover:border-ring/40", !k.enabled && "bg-muted/40")}>
                <CardContent className="flex h-full flex-col gap-2.5 p-4">
                  <div className="flex items-start justify-between gap-3">
                    <button type="button" className="min-w-0 text-left" onClick={() => setEditing(k.id)}>
                      <span className={cn("block truncate font-mono text-[14px] font-semibold hover:underline", !k.enabled && "text-muted-foreground")}>{k.name}</span>
                    </button>
                    <Switch checked={k.enabled} onCheckedChange={(v) => toggle(k, v)} aria-label={`启用 ${k.name}`} />
                  </div>
                  <p className="line-clamp-3 text-[13px] leading-relaxed text-muted-foreground">{k.description}</p>
                  <div className="mt-auto flex flex-wrap items-center gap-x-3 gap-y-1 pt-1 text-xs text-muted-foreground">
                    <span>{k.fileCount > 0 ? `${k.fileCount} 个附属文件 · ` : ""}{formatSize(k.size)}</span>
                    <span>{k.useCount > 0 ? `已读取 ${k.useCount} 次${k.lastUsedAt ? `，最近 ${formatTime(k.lastUsedAt)}` : ""}` : "尚未被读取"}</span>
                    <Button variant="ghost" size="sm" className="ml-auto -mr-2 h-7" onClick={() => setEditing(k.id)}>
                      查看 / 编辑
                      <ChevronRightIcon />
                    </Button>
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>
        )}
      </section>

      <section className="mt-8 grid gap-3">
        <div>
          <h2 className="text-[15px] font-semibold">内置工具</h2>
          <p className="mt-0.5 text-xs text-muted-foreground">写在程序里的能力；带开关的可在这里启停，其余始终可用。</p>
        </div>
        <Card className="py-0">
          <CardContent className="divide-y p-0">
            {(data?.builtin ?? []).map((t) => (
              <div key={t.name} className="flex gap-3 px-4 py-3">
                <WrenchIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <code className="font-mono text-[13px] font-semibold">{t.name}</code>
                    {t.proactive ? <Badge variant="neutral">主动回复可用</Badge> : <Badge variant="neutral">仅被 @ / 私聊</Badge>}
                    {t.setting && catalog && <Badge variant={catalog.enabled ? "ok" : "neutral"}>{catalog.enabled ? "已开启" : "未开启"}</Badge>}
                    {t.onlyWithSkills && <Badge variant={enabledCount > 0 ? "ok" : "neutral"}>{enabledCount > 0 ? "已提供" : "有启用技能时提供"}</Badge>}
                  </div>
                  <p className="mt-1 text-[13px] text-muted-foreground">{t.description}</p>
                </div>
                {t.setting === "modelCatalog" && catalog && (
                  <div className="flex shrink-0 items-start gap-1">
                    <Button variant="ghost" size="icon-sm" asChild>
                      <Link href="/admin/settings?tab=catalog" aria-label="模型目录设置">
                        <SettingsIcon />
                      </Link>
                    </Button>
                    <Switch checked={catalog.enabled} onCheckedChange={toggleCatalog} aria-label={`启用 ${t.name}`} />
                  </div>
                )}
              </div>
            ))}
          </CardContent>
        </Card>
      </section>

      <SkillEditor
        id={editing}
        template={data?.template ?? ""}
        onClose={closeEditor}
        onSaved={savedEditor}
      />
      <SkillImportDialog open={importOpen} onOpenChange={setImportOpen} onDone={load} />
    </>
  )
}

type EditorProps = { id: number | undefined; template: string; onClose: () => void; onSaved: () => void }

function SkillEditor(props: EditorProps) {
  return (
    <Dialog open={props.id !== undefined} onOpenChange={(v) => !v && props.onClose()}>
      <DialogContent className="sm:max-w-3xl">{props.id !== undefined && <EditorBody {...props} id={props.id} />}</DialogContent>
    </Dialog>
  )
}

function EditorBody({ id, template, onClose, onSaved }: EditorProps & { id: number }) {
  const [detail, setDetail] = useState<SkillDetail | null>(null)
  const [content, setContent] = useState(id === 0 ? template : "")
  const [enabled, setEnabled] = useState(true)
  const [openFile, setOpenFile] = useState<string | null>(null)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const { pending, track } = useDelayedPending()

  useEffect(() => {
    if (id === 0) return
    api
      .get<SkillDetail>(`/api/skills/${id}`)
      .then((d) => {
        setDetail(d)
        setContent(d.content)
        setEnabled(d.enabled)
      })
      .catch(onClose)
  }, [id, onClose])

  const save = () =>
    track(async () => {
      const body = { content, enabled }
      await (id === 0 ? api.post("/api/skills", body) : api.put(`/api/skills/${id}`, body))
      toast.success("已保存，下一条消息起生效")
      onSaved()
    }).catch(() => {})

  const remove = () =>
    track(async () => {
      await api.del(`/api/skills/${id}`)
      toast.success(`已删除「${detail?.name}」`)
      onSaved()
    }).catch(() => {})

  const loadingDetail = id !== 0 && !detail

  return (
    <>
      <DialogHeader>
        <DialogTitle className="font-mono">{id === 0 ? "新建技能" : (detail?.name ?? "…")}</DialogTitle>
        <DialogDescription>name 与 description 从开头的 frontmatter 读取；正文是给模型看的处理说明，用 Markdown 写。</DialogDescription>
      </DialogHeader>

      <div className="grid gap-4">
        {loadingDetail ? (
          <Skeleton className="h-[50dvh]" />
        ) : (
          <Textarea
            value={content}
            onChange={(e) => setContent(e.target.value)}
            spellCheck={false}
            aria-label="SKILL.md"
            className="h-[50dvh] resize-none font-mono text-[12.5px] leading-relaxed [field-sizing:fixed]"
          />
        )}

        <label className="flex cursor-pointer items-center justify-between gap-4">
          <span>
            <span className="block text-[13.5px] font-medium">启用</span>
            <span className="block text-xs text-muted-foreground">停用后模型看不到这个技能</span>
          </span>
          <Switch checked={enabled} onCheckedChange={setEnabled} />
        </label>

        {detail && detail.files.length > 0 && (
          <div className="grid gap-1.5">
            <div className="text-xs text-muted-foreground">附属文件（只读，重新上传技能包可替换）</div>
            <ul className="rounded-lg border text-[12.5px]">
              {detail.files.map((f) => {
                const open = openFile === f.path
                return (
                  <li key={f.path} className="border-b last:border-b-0">
                    <button
                      type="button"
                      className="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-muted"
                      onClick={() => setOpenFile(open ? null : f.path)}
                      aria-expanded={open}
                    >
                      <ChevronRightIcon className={cn("size-3.5 shrink-0 transition-transform", open && "rotate-90")} />
                      <FileTextIcon className="size-3.5 shrink-0 text-muted-foreground" />
                      <span className="truncate font-mono">{f.path}</span>
                      <span className="ml-auto shrink-0 text-muted-foreground">{formatSize(new Blob([f.content]).size)}</span>
                    </button>
                    {open && (
                      <pre className="max-h-64 overflow-auto border-t bg-muted/50 px-3 py-2.5 font-mono text-[12px] leading-relaxed whitespace-pre-wrap">{f.content}</pre>
                    )}
                  </li>
                )
              })}
            </ul>
          </div>
        )}
      </div>

      <DialogFooter className="sm:justify-between">
        {id !== 0 ? (
          <Button variant="ghost" className="text-crit hover:text-crit" onClick={() => setConfirmDelete(true)} disabled={pending || loadingDetail}>
            <Trash2Icon />
            删除
          </Button>
        ) : (
          <span />
        )}
        <div className="flex gap-2">
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button onClick={save} disabled={pending || loadingDetail || !content.trim()}>
            {pending ? "保存中…" : "保存"}
          </Button>
        </div>
      </DialogFooter>

      <AlertDialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>删除技能</AlertDialogTitle>
            <AlertDialogDescription>确认删除「{detail?.name}」及其 {detail?.files.length ?? 0} 个附属文件？删除后不可恢复。</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={remove}>
              删除
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
