"use client"

import {
  BookOpenIcon,
  ChevronRightIcon,
  FileTextIcon,
  FolderIcon,
  PencilIcon,
  PlusIcon,
  SearchIcon,
  SparklesIcon,
  Trash2Icon,
  UploadIcon,
} from "lucide-react"
import Link from "next/link"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { toast } from "sonner"

import { EmptyState, Field, formatTime, PageHeader } from "@/components/common"
import { cleanPath, KBImportDialog } from "@/components/kb-import"
import { ToggleRow } from "@/components/settings-forms"
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
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Textarea } from "@/components/ui/textarea"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { api } from "@/lib/api"
import type { EmbedStatus, KBDocument, KBHit } from "@/lib/types"
import { useDelayedLoading, useDelayedPending } from "@/lib/use-delayed-pending"
import { cn } from "@/lib/utils"

type Draft = { id?: number; path: string; title: string; content: string; enabled: boolean }
const EMPTY: Draft = { path: "", title: "", content: "", enabled: true }

type Folder = { name: string; path: string; folders: Folder[]; docs: KBDocument[]; count: number }

/** 按 path 组装目录树（文档已按 path, title 排序） */
function buildTree(docs: KBDocument[]): Folder {
  const root: Folder = { name: "", path: "", folders: [], docs: [], count: 0 }
  for (const d of docs) {
    let node = root
    node.count++
    for (const seg of d.path ? d.path.split("/") : []) {
      const path = node.path ? `${node.path}/${seg}` : seg
      let child = node.folders.find((f) => f.name === seg)
      if (!child) {
        child = { name: seg, path, folders: [], docs: [], count: 0 }
        node.folders.push(child)
      }
      node = child
      node.count++
    }
    node.docs.push(d)
  }
  return root
}

function allFolders(docs: KBDocument[]) {
  const set = new Set<string>()
  for (const d of docs) {
    const segs = d.path ? d.path.split("/") : []
    segs.forEach((_, i) => set.add(segs.slice(0, i + 1).join("/")))
  }
  return [...set].sort()
}

export default function KBPage() {
  const [docs, setDocs] = useState<KBDocument[] | null>(null)
  const [embed, setEmbed] = useState<EmbedStatus | null>(null)
  const [draft, setDraft] = useState<Draft | null>(null)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [filter, setFilter] = useState("")
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const [importOpen, setImportOpen] = useState(false)
  const [importPrefix, setImportPrefix] = useState("")
  const [rename, setRename] = useState<{ from: string; to: string } | null>(null)
  const [query, setQuery] = useState("")
  const [hits, setHits] = useState<KBHit[] | null>(null)
  const [searchOpen, setSearchOpen] = useState(false)
  const { loading, track } = useDelayedLoading()
  const { pending, track: trackSave } = useDelayedPending()

  // 工作区高度 = 视口 − 工作区顶部位置，两栏刚好铺满一屏、不用滚页面就能看到保存按钮。
  // 页头高度随描述折行、状态标签变化，所以实测而不是写死；直接写 CSS 变量，不触发重渲染。
  const workRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = workRef.current
    if (!el) return
    const fit = () => {
      const top = el.getBoundingClientRect().top + window.scrollY
      el.style.setProperty("--kb-h", `${Math.max(480, window.innerHeight - top - 24)}px`)
    }
    fit()
    window.addEventListener("resize", fit)
    return () => window.removeEventListener("resize", fit)
  }, [docs, embed])

  const fetchList = useCallback(
    () =>
      api.get<{ list: KBDocument[]; embedding: EmbedStatus }>("/api/kb").then((r) => {
        setDocs(r.list)
        setEmbed(r.embedding)
      }),
    []
  )
  const load = useCallback(() => track(fetchList).catch(() => {}), [track, fetchList])
  useEffect(() => {
    load()
  }, [load])

  // 向量化进行中：每 3 秒刷新进度（静默，不弹错）
  const embedding = !!embed?.enabled && embed.embedded < embed.total && !embed.lastError
  useEffect(() => {
    if (!embedding) return
    const t = setInterval(() => {
      api.get<{ list: KBDocument[]; embedding: EmbedStatus }>("/api/kb", { silent: true }).then((r) => setEmbed(r.embedding)).catch(() => {})
    }, 3000)
    return () => clearInterval(t)
  }, [embedding])

  const folders = useMemo(() => allFolders(docs ?? []), [docs])
  const tree = useMemo(() => {
    const kw = filter.trim().toLowerCase()
    const list = (docs ?? []).filter((d) => !kw || `${d.path}/${d.title}`.toLowerCase().includes(kw))
    return buildTree(list)
  }, [docs, filter])

  const open = (id: number) =>
    api
      .get<KBDocument>(`/api/kb/${id}`)
      .then((d) => setDraft({ id: d.id, path: d.path, title: d.title, content: d.content ?? "", enabled: d.enabled }))
      .catch(() => {})

  const save = () => {
    if (!draft) return
    trackSave(async () => {
      const saved = draft.id
        ? await api.put<KBDocument>(`/api/kb/${draft.id}`, draft)
        : await api.post<KBDocument>("/api/kb", draft)
      toast.success(`已保存，切分为 ${saved.chunkCount} 个检索块`)
      setDraft({ ...draft, id: saved.id, path: saved.path })
      await fetchList()
    }).catch(() => {})
  }

  const remove = () => {
    if (!draft?.id) return
    trackSave(async () => {
      await api.del(`/api/kb/${draft.id}`)
      toast.success("已删除")
      setDraft(null)
      setConfirmDelete(false)
      await fetchList()
    }).catch(() => {})
  }

  const doRename = () => {
    if (!rename) return
    trackSave(async () => {
      const to = cleanPath(rename.to)
      const r = await api.post<{ documents: number }>("/api/kb/folders/rename", { from: rename.from, to })
      toast.success(`已移动 ${r.documents} 篇文档`)
      if (draft && (draft.path === rename.from || draft.path.startsWith(`${rename.from}/`))) {
        setDraft({ ...draft, path: cleanPath(to + draft.path.slice(rename.from.length)) })
      }
      setRename(null)
      await fetchList()
    }).catch(() => {})
  }

  const search = (e: React.FormEvent) => {
    e.preventDefault()
    if (!query.trim()) return
    api.post<{ list: KBHit[] }>("/api/kb/search", { query, topK: 5 }).then((r) => setHits(r.list)).catch(() => {})
  }

  const toggle = (path: string) =>
    setCollapsed((cur) => {
      const next = new Set(cur)
      if (next.has(path)) next.delete(path)
      else next.add(path)
      return next
    })

  const renderDoc = (d: KBDocument, depth: number) => (
    <button
      key={d.id}
      onClick={() => open(d.id)}
      style={{ paddingLeft: 12 + depth * 14 }}
      className={cn(
        "flex w-full items-center gap-2 rounded-lg py-2 pr-3 text-left transition-colors hover:bg-muted",
        draft?.id === d.id && "bg-accent-soft"
      )}
    >
      <FileTextIcon className="size-3.5 shrink-0 text-muted-foreground" />
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-2 text-[13.5px] font-medium">
          <span className="truncate">{d.title}</span>
          {!d.enabled && <Badge variant="neutral">停用</Badge>}
        </span>
        <span className="block font-mono text-[11px] text-muted-foreground tabular-nums">
          {d.chunkCount} 块 · {formatTime(d.updatedAt)}
        </span>
      </span>
    </button>
  )

  const renderFolder = (f: Folder, depth: number): React.ReactNode => {
    const shut = collapsed.has(f.path) && !filter.trim()
    return (
      <div key={f.path}>
        <div className="group flex items-center rounded-lg hover:bg-muted" style={{ paddingLeft: 4 + depth * 14 }}>
          <button onClick={() => toggle(f.path)} className="flex min-w-0 flex-1 items-center gap-1.5 py-1.5 text-left text-[13px] font-medium">
            <ChevronRightIcon className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform", !shut && "rotate-90")} />
            <FolderIcon className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="truncate">{f.name}</span>
            <span className="font-mono text-[11px] text-muted-foreground tabular-nums">{f.count}</span>
          </button>
          <span className="flex shrink-0 pr-1 opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100 pointer-coarse:opacity-100">
            <Button size="icon-xs" variant="ghost" title="在此目录新建文档" onClick={() => setDraft({ ...EMPTY, path: f.path })}>
              <PlusIcon />
            </Button>
            <Button size="icon-xs" variant="ghost" title="导入到此目录" onClick={() => openImport(f.path)}>
              <UploadIcon />
            </Button>
            <Button size="icon-xs" variant="ghost" title="改名 / 移动目录" onClick={() => setRename({ from: f.path, to: f.path })}>
              <PencilIcon />
            </Button>
          </span>
        </div>
        {!shut && (
          <>
            {f.folders.map((c) => renderFolder(c, depth + 1))}
            {f.docs.map((d) => renderDoc(d, depth + 1))}
          </>
        )}
      </div>
    )
  }

  const openImport = (prefix: string) => {
    setImportPrefix(prefix)
    setImportOpen(true)
  }

  return (
    <>
      <PageHeader
        title="知识库"
        description="按目录管理 Markdown 文档，自动切块，保存即生效。"
        actions={
          <>
            {embed && docs && docs.length > 0 && <EmbedChip status={embed} />}
            <Button variant="outline" onClick={() => setSearchOpen(true)}>
              <SearchIcon />
              检索测试
            </Button>
            <Button variant="outline" onClick={() => openImport("")}>
              <UploadIcon />
              批量导入
            </Button>
            <Button onClick={() => setDraft({ ...EMPTY })}>
              <PlusIcon />
              新建文档
            </Button>
          </>
        }
      />
      {/* 宽屏下左右两栏都钉在视口内、各自滚动：长文档不再把目录树顶出屏幕 */}
      <div ref={workRef} className="grid gap-6 lg:grid-cols-[300px_minmax(0,1fr)] lg:items-start">
        <div className="flex flex-col gap-4 lg:sticky lg:top-6 lg:h-(--kb-h)">
          <Card className="max-h-[60dvh] min-h-0 py-0 lg:max-h-none lg:flex-1">
            <CardContent className="flex min-h-0 flex-1 flex-col p-2">
              {loading && !docs ? (
                <div className="grid gap-2 p-2">
                  {Array.from({ length: 4 }).map((_, i) => (
                    <Skeleton key={i} className="h-12" />
                  ))}
                </div>
              ) : docs?.length === 0 ? (
                <EmptyState
                  icon={<BookOpenIcon className="size-5" />}
                  title="还没有文档"
                  description="把常见问题、价格规则、使用教程写进来，机器人才有依据可答。已有 Markdown 可直接批量导入。"
                />
              ) : (
                <>
                  <div className="relative mb-1 shrink-0">
                    <SearchIcon className="absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
                    <Input className="h-8 pl-8 text-[13px]" placeholder="筛选目录或标题" value={filter} onChange={(e) => setFilter(e.target.value)} />
                  </div>
                  <div className="grid min-h-0 flex-1 content-start gap-0.5 overflow-y-auto overscroll-contain">
                    {tree.folders.map((f) => renderFolder(f, 0))}
                    {tree.docs.map((d) => renderDoc(d, 0))}
                    {tree.count === 0 && <p className="px-3 py-4 text-center text-xs text-muted-foreground">没有匹配的文档</p>}
                  </div>
                </>
              )}
            </CardContent>
          </Card>
        </div>

        <Card className="py-0 lg:sticky lg:top-6 lg:h-(--kb-h)">
          {draft ? (
            <CardContent className="flex min-h-0 flex-1 flex-col gap-4 p-5">
              <div className="grid shrink-0 gap-3 sm:grid-cols-[220px_minmax(0,1fr)]">
                <div className="relative">
                  <FolderIcon className="absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
                  <Input
                    className="h-11 pl-9"
                    list="kb-folders"
                    placeholder="根目录"
                    aria-label="所在目录"
                    value={draft.path}
                    onChange={(e) => setDraft({ ...draft, path: e.target.value })}
                  />
                </div>
                <Input className="h-11 text-base font-medium" placeholder="文档标题" aria-label="文档标题" value={draft.title} onChange={(e) => setDraft({ ...draft, title: e.target.value })} />
              </div>
              <Textarea
                className="h-[60dvh] resize-none font-mono text-[13px] leading-relaxed [field-sizing:fixed] lg:h-auto lg:min-h-0 lg:flex-1"
                placeholder={"# 充值\n\n支持支付宝、微信充值，最低 10 元，即时到账。\n\n# 退款\n\n未消费余额 7 天内可申请退款。"}
                value={draft.content}
                onChange={(e) => setDraft({ ...draft, content: e.target.value })}
              />
              <ToggleRow label="启用" hint="停用后不参与检索，内容保留" checked={draft.enabled} onChange={(enabled) => setDraft({ ...draft, enabled })} />
              <div className="flex shrink-0 items-center justify-between border-t pt-4">
                {draft.id ? (
                  <Button variant="destructive" onClick={() => setConfirmDelete(true)}>
                    <Trash2Icon />
                    删除
                  </Button>
                ) : (
                  <span />
                )}
                <div className="flex gap-2">
                  <Button variant="ghost" onClick={() => setDraft(null)}>
                    取消
                  </Button>
                  <Button onClick={save} disabled={pending || !draft.title.trim()}>
                    {pending ? "保存中…" : "保存"}
                  </Button>
                </div>
              </div>
            </CardContent>
          ) : (
            <EmptyState icon={<BookOpenIcon className="size-5" />} title="选择左侧文档进行编辑" description="或点击右上角「新建文档」。" />
          )}
        </Card>
      </div>

      <datalist id="kb-folders">
        {folders.map((f) => (
          <option key={f} value={f} />
        ))}
      </datalist>

      <Dialog open={searchOpen} onOpenChange={setSearchOpen}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>检索测试</DialogTitle>
            <DialogDescription>输入用户可能的问法，看机器人会检索到哪些片段（与线上预检索、kb_search 同一套）。点结果可打开对应文档。</DialogDescription>
          </DialogHeader>
          <form onSubmit={search} className="flex gap-2">
            <div className="relative min-w-0 flex-1">
              <SearchIcon className="absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input autoFocus className="pl-9" placeholder="例如：怎么充钱" value={query} onChange={(e) => setQuery(e.target.value)} />
            </div>
            <Button type="submit" disabled={!query.trim()}>
              检索
            </Button>
          </form>
          {hits &&
            (hits.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                没有命中。{embed?.enabled ? "试试补充同义说法到文档里。" : "试试补充同义说法到文档里，或开启向量检索按语义召回。"}
              </p>
            ) : (
              <ol className="-mx-1 grid max-h-[60dvh] gap-2 overflow-y-auto overscroll-contain px-1">
                {hits.map((h, i) => (
                  <li key={`${h.chunkId}-${h.reflectionId ?? 0}`}>
                    <button
                      type="button"
                      disabled={!h.documentId}
                      onClick={() => {
                        setSearchOpen(false)
                        open(h.documentId)
                      }}
                      className="w-full rounded-lg border p-3 text-left text-[13px] transition-colors enabled:hover:bg-muted"
                    >
                      <div className="mb-1 flex items-center justify-between gap-3 font-mono text-xs text-muted-foreground">
                        <span className="truncate">
                          [{i + 1}] {h.docPath && `${h.docPath}/`}
                          {h.docTitle}
                          {h.heading && ` / ${h.heading}`}
                        </span>
                        <span className="flex shrink-0 gap-1.5">
                          {h.keyword > 0 && <Badge variant="neutral">关键词 {h.keyword.toFixed(2)}</Badge>}
                          {h.vector > 0 && <Badge variant="accent">语义 {h.vector.toFixed(2)}</Badge>}
                        </span>
                      </div>
                      <p className="line-clamp-4 whitespace-pre-wrap text-foreground">{h.content}</p>
                    </button>
                  </li>
                ))}
              </ol>
            ))}
        </DialogContent>
      </Dialog>

      <KBImportDialog open={importOpen} onOpenChange={setImportOpen} folders={folders} defaultPrefix={importPrefix} onDone={fetchList} />

      <Dialog open={!!rename} onOpenChange={(v) => !v && setRename(null)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>改名 / 移动目录</DialogTitle>
            <DialogDescription>「{rename?.from}」及其子目录下的文档会一起移动。填已有目录即合并，留空移到根目录。</DialogDescription>
          </DialogHeader>
          <Field label="新路径" hint="用「/」分隔多级目录" htmlFor="rename-to">
            <Input id="rename-to" list="kb-folders" value={rename?.to ?? ""} onChange={(e) => rename && setRename({ ...rename, to: e.target.value })} />
          </Field>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setRename(null)}>
              取消
            </Button>
            <Button onClick={doRename} disabled={pending || !rename || cleanPath(rename.to) === rename.from}>
              {pending ? "移动中…" : "确定"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>删除文档</AlertDialogTitle>
            <AlertDialogDescription>确认删除「{draft?.title}」？删除后不可恢复，机器人将不再引用其内容。</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={remove}>
              确认删除
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

/** 向量检索状态标签（页头）：模型名、错误原因放悬停提示，不占版面 */
function EmbedChip({ status }: { status: EmbedStatus }) {
  const chip = "inline-flex h-8 items-center gap-1.5 rounded-full px-3 font-mono text-xs tabular-nums transition-colors"
  if (!status.enabled) {
    return (
      <Link href="/admin/settings?tab=embedding" className={cn(chip, "bg-muted text-muted-foreground hover:text-foreground")} title="当前只按关键词检索，点此开启向量检索">
        <SparklesIcon className="size-3.5" />
        仅关键词
      </Link>
    )
  }
  const done = status.embedded >= status.total
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className={cn(chip, status.lastError ? "bg-crit-soft text-crit" : done ? "bg-accent-soft text-primary" : "bg-warn-soft text-warn")}>
          <SparklesIcon className="size-3.5" />
          {status.lastError ? "向量服务异常" : done ? "语义检索" : "向量化中"} {status.embedded}/{status.total}
        </span>
      </TooltipTrigger>
      <TooltipContent className="max-w-80">
        {status.model}
        {status.lastError && <span className="mt-1 block">嵌入服务不可用，检索已退回纯关键词：{status.lastError}</span>}
      </TooltipContent>
    </Tooltip>
  )
}
