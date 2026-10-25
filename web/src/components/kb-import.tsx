"use client"

// 知识库批量导入：多选 .md / .txt / .zip，或直接选文件夹（保留子目录结构）。
// 文件所在子目录 → 文档目录，文件名去扩展名 → 标题；zip 在服务端展开后按同样规则处理。
import { FileArchiveIcon, FileTextIcon, FolderOpenIcon, UploadIcon } from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"
import { toast } from "sonner"

import { Field } from "@/components/common"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import type { KBImportResult } from "@/lib/types"
import { useDelayedPending } from "@/lib/use-delayed-pending"
import { cn } from "@/lib/utils"

const TEXT_EXT = /\.(md|markdown|txt)$/i
const ZIP_EXT = /\.zip$/i
const MAX_BYTES = 50 << 20

type Picked = { file: File; rel: string }

/** 与服务端 CleanKBPath 同规则 */
export function cleanPath(p: string) {
  return p
    .replaceAll("\\", "/")
    .split("/")
    .map((s) => s.trim())
    .filter(Boolean)
    .join("/")
}

function target(prefix: string, rel: string) {
  const parts = rel.split("/")
  const name = parts.pop() ?? ""
  return { path: cleanPath([prefix, ...parts].join("/")), title: name.replace(/\.[^.]+$/, "").trim() }
}

export function formatSize(n: number) {
  return n < 1 << 20 ? `${Math.max(1, Math.round(n / 1024))} KB` : `${(n / 1024 / 1024).toFixed(1)} MB`
}

function hidden(rel: string) {
  return rel.split("/").some((s) => s.startsWith(".") || s === "__MACOSX")
}

type Props = {
  open: boolean
  onOpenChange: (v: boolean) => void
  folders: string[]
  defaultPrefix: string
  onDone: () => void
}

export function KBImportDialog(props: Props) {
  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        {/* 内容随对话框挂载 / 卸载，每次打开都是干净状态 */}
        <ImportBody {...props} />
      </DialogContent>
    </Dialog>
  )
}

function ImportBody({ onOpenChange, folders, defaultPrefix, onDone }: Props) {
  const fileRef = useRef<HTMLInputElement>(null)
  const dirRef = useRef<HTMLInputElement>(null)
  const [picked, setPicked] = useState<Picked[]>([])
  const [prefix, setPrefix] = useState(defaultPrefix)
  const [mode, setMode] = useState<"overwrite" | "skip">("overwrite")
  const [result, setResult] = useState<KBImportResult | null>(null)
  const { pending, track } = useDelayedPending()

  // React 不认 webkitdirectory 属性，挂载后手动加
  useEffect(() => {
    dirRef.current?.setAttribute("webkitdirectory", "")
  })

  const add = (files: FileList | null) => {
    if (!files) return
    const next = Array.from(files)
      .map((file) => ({ file, rel: file.webkitRelativePath || file.name }))
      .filter((p) => !hidden(p.rel))
    setPicked((cur) => {
      const seen = new Set(cur.map((p) => p.rel))
      return [...cur, ...next.filter((p) => !seen.has(p.rel))]
    })
    setResult(null)
  }

  const rows = useMemo(
    () =>
      picked.map((p) => ({
        ...p,
        kind: ZIP_EXT.test(p.rel) ? ("zip" as const) : TEXT_EXT.test(p.rel) ? ("text" as const) : ("skip" as const),
        ...target(prefix, p.rel),
      })),
    [picked, prefix]
  )
  const usable = rows.filter((r) => r.kind !== "skip")
  const totalBytes = usable.reduce((n, r) => n + r.file.size, 0)

  const submit = () =>
    track(async () => {
      const fd = new FormData()
      fd.append("prefix", prefix)
      fd.append("mode", mode)
      for (const r of usable) {
        fd.append("files", r.file, r.file.name)
        fd.append("paths", r.rel)
      }
      const res = await fetch("/api/kb/import", { method: "POST", body: fd, cache: "no-store" })
      const data = (await res.json().catch(() => ({}))) as Partial<KBImportResult> & { error?: string }
      if (!res.ok) {
        toast.error(data.error ?? `导入失败（HTTP ${res.status}）`)
        if (data.ignored) setResult({ created: 0, updated: 0, skipped: 0, ignored: data.ignored })
        return
      }
      const r = data as KBImportResult
      setResult(r)
      setPicked([])
      toast.success(`导入完成：新增 ${r.created} 篇，覆盖 ${r.updated} 篇${r.skipped ? `，跳过同名 ${r.skipped} 篇` : ""}`)
      onDone()
    }).catch(() => toast.error("网络异常，无法连接到服务端"))

  return (
    <>
      <DialogHeader>
        <DialogTitle>批量导入</DialogTitle>
        <DialogDescription>
          支持 .md / .markdown / .txt 与 .zip。选文件夹时保留子目录结构：子目录成为文档目录，文件名成为标题。
        </DialogDescription>
      </DialogHeader>

      <div className="grid gap-4">
        <div className="flex flex-wrap gap-2">
          <Button type="button" variant="outline" onClick={() => fileRef.current?.click()}>
            <FileTextIcon />
            选择文件
          </Button>
          <Button type="button" variant="outline" onClick={() => dirRef.current?.click()}>
            <FolderOpenIcon />
            选择文件夹
          </Button>
          {picked.length > 0 && (
            <Button type="button" variant="ghost" onClick={() => setPicked([])}>
              清空
            </Button>
          )}
          <input
            ref={fileRef}
            type="file"
            multiple
            accept=".md,.markdown,.txt,.zip"
            className="hidden"
            onChange={(e) => {
              add(e.target.files)
              e.target.value = ""
            }}
          />
          <input
            ref={dirRef}
            type="file"
            multiple
            className="hidden"
            onChange={(e) => {
              add(e.target.files)
              e.target.value = ""
            }}
          />
        </div>

        <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_160px]">
          <Field label="导入到目录" hint="留空 = 根目录；可输入新目录，例如「产品/接入」" htmlFor="import-prefix">
            <Input id="import-prefix" list="kb-folders" value={prefix} onChange={(e) => setPrefix(e.target.value)} placeholder="根目录" />
          </Field>
          <Field label="同目录同名文档">
            <Select value={mode} onValueChange={(v) => setMode(v as "overwrite" | "skip")}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="overwrite">覆盖正文</SelectItem>
                <SelectItem value="skip">跳过</SelectItem>
              </SelectContent>
            </Select>
          </Field>
        </div>
        <datalist id="kb-folders">
          {folders.map((f) => (
            <option key={f} value={f} />
          ))}
        </datalist>

        {rows.length > 0 && (
          <div className="grid gap-1.5">
            <div className="flex justify-between text-xs text-muted-foreground">
              <span>
                待导入 {usable.length} 个文件{rows.length > usable.length && `，${rows.length - usable.length} 个格式不支持将跳过`}
              </span>
              <span className={cn("font-mono tabular-nums", totalBytes > MAX_BYTES && "text-crit")}>
                {formatSize(totalBytes)} / 50 MB
              </span>
            </div>
            <ul className="max-h-64 overflow-y-auto rounded-lg border text-[12.5px]">
              {rows.map((r) => (
                <li key={r.rel} className={cn("flex items-center gap-2 border-b px-3 py-1.5 last:border-b-0", r.kind === "skip" && "text-muted-foreground line-through")}>
                  {r.kind === "zip" ? <FileArchiveIcon className="size-3.5 shrink-0" /> : <FileTextIcon className="size-3.5 shrink-0" />}
                  {r.kind === "zip" ? (
                    <span className="truncate">
                      {r.rel} <span className="text-muted-foreground">· 压缩包内的文件按其目录导入到「{prefix ? cleanPath(prefix) : "根目录"}」下</span>
                    </span>
                  ) : (
                    <span className="truncate">
                      <span className="text-muted-foreground">{r.path ? `${r.path} / ` : ""}</span>
                      {r.title}
                    </span>
                  )}
                </li>
              ))}
            </ul>
          </div>
        )}

        {result && result.ignored.length > 0 && (
          <div className="grid gap-1.5">
            <div className="text-xs text-warn">以下 {result.ignored.length} 个文件未导入：</div>
            <ul className="max-h-40 overflow-y-auto rounded-lg border text-[12.5px]">
              {result.ignored.map((f) => (
                <li key={f.name} className="flex justify-between gap-3 border-b px-3 py-1.5 last:border-b-0">
                  <span className="truncate">{f.name}</span>
                  <span className="shrink-0 text-muted-foreground">{f.reason}</span>
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>

      <DialogFooter>
        <Button variant="ghost" onClick={() => onOpenChange(false)}>
          {result ? "完成" : "取消"}
        </Button>
        <Button onClick={submit} disabled={pending || usable.length === 0 || totalBytes > MAX_BYTES}>
          <UploadIcon />
          {pending ? "导入中…" : `导入 ${usable.length || ""} 个文件`}
        </Button>
      </DialogFooter>
    </>
  )
}
