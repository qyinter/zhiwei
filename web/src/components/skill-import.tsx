"use client"

// 上传技能：选技能文件夹（含 SKILL.md）、.zip 技能包，或单个带 frontmatter 的 .md。
// 含 SKILL.md 的目录即一个技能，目录下的文本文件作为附属参考文件；脚本不会执行，服务端直接跳过。
import { FileArchiveIcon, FileTextIcon, FolderOpenIcon, UploadIcon } from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"
import { toast } from "sonner"

import { Field } from "@/components/common"
import { formatSize } from "@/components/kb-import"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import type { SkillImportResult } from "@/lib/types"
import { useDelayedPending } from "@/lib/use-delayed-pending"
import { cn } from "@/lib/utils"

const MAX_BYTES = 50 << 20

type Picked = { file: File; rel: string }

function hidden(rel: string) {
  return rel.split("/").some((s) => s.startsWith(".") || s === "__MACOSX")
}

const isSkillMd = (rel: string) => /(^|\/)skill\.md$/i.test(rel)

type Props = { open: boolean; onOpenChange: (v: boolean) => void; onDone: () => void }

export function SkillImportDialog(props: Props) {
  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        {/* 内容随对话框挂载 / 卸载，每次打开都是干净状态 */}
        <ImportBody {...props} />
      </DialogContent>
    </Dialog>
  )
}

function ImportBody({ onOpenChange, onDone }: Props) {
  const fileRef = useRef<HTMLInputElement>(null)
  const dirRef = useRef<HTMLInputElement>(null)
  const [picked, setPicked] = useState<Picked[]>([])
  const [mode, setMode] = useState<"overwrite" | "skip">("overwrite")
  const [result, setResult] = useState<SkillImportResult | null>(null)
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

  const totalBytes = useMemo(() => picked.reduce((n, p) => n + p.file.size, 0), [picked])
  const skillCount = picked.filter((p) => isSkillMd(p.rel)).length
  const zipCount = picked.filter((p) => /\.zip$/i.test(p.rel)).length

  const submit = () =>
    track(async () => {
      const fd = new FormData()
      fd.append("mode", mode)
      for (const p of picked) {
        fd.append("files", p.file, p.file.name)
        fd.append("paths", p.rel)
      }
      const res = await fetch("/api/skills/import", { method: "POST", body: fd, cache: "no-store" })
      const data = (await res.json().catch(() => ({}))) as Partial<SkillImportResult> & { error?: string }
      if (!res.ok) {
        toast.error(data.error ?? `上传失败（HTTP ${res.status}）`)
        if (data.ignored) setResult({ created: 0, updated: 0, skipped: 0, names: [], ignored: data.ignored })
        return
      }
      const r = data as SkillImportResult
      setResult(r)
      setPicked([])
      toast.success(`上传完成：新增 ${r.created} 个，覆盖 ${r.updated} 个${r.skipped ? `，跳过同名 ${r.skipped} 个` : ""}`)
      onDone()
    }).catch(() => toast.error("网络异常，无法连接到服务端"))

  return (
    <>
      <DialogHeader>
        <DialogTitle>上传技能</DialogTitle>
        <DialogDescription>沿用 Claude Skills 的 SKILL.md 格式。可选技能文件夹、.zip 技能包，或单个带 frontmatter 的 .md 文件。</DialogDescription>
      </DialogHeader>

      <div className="grid gap-4">
        <pre className="overflow-x-auto rounded-lg bg-muted px-3.5 py-3 font-mono text-[12px] leading-relaxed text-muted-foreground">
          {`refund-policy/
├── SKILL.md          必需：开头写 name 和 description
└── references/       可选：参考文档，模型按需读取
    └── rules.md      支持 .md .txt .json .yaml .csv`}
        </pre>
        <p className="-mt-1 text-xs text-muted-foreground">
          description 写清楚「什么时候用」，模型就靠它判断是否读取这个技能。脚本文件（.py / .ts / .sh 等）不会执行，上传时会跳过。
        </p>

        <div className="flex flex-wrap gap-2">
          <Button type="button" variant="outline" onClick={() => dirRef.current?.click()}>
            <FolderOpenIcon />
            选择技能文件夹
          </Button>
          <Button type="button" variant="outline" onClick={() => fileRef.current?.click()}>
            <FileArchiveIcon />
            选择 .zip / .md
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
            accept=".zip,.md,.markdown"
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

        <Field label="已有同名技能">
          <Select value={mode} onValueChange={(v) => setMode(v as "overwrite" | "skip")}>
            <SelectTrigger className="w-full sm:w-52">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="overwrite">覆盖（保留启用状态）</SelectItem>
              <SelectItem value="skip">跳过</SelectItem>
            </SelectContent>
          </Select>
        </Field>

        {picked.length > 0 && (
          <div className="grid gap-1.5">
            <div className="flex justify-between text-xs text-muted-foreground">
              <span>
                {picked.length} 个文件
                {skillCount > 0 && `，含 ${skillCount} 个 SKILL.md`}
                {zipCount > 0 && `，${zipCount} 个压缩包`}
              </span>
              <span className={cn("font-mono tabular-nums", totalBytes > MAX_BYTES && "text-crit")}>{formatSize(totalBytes)} / 50 MB</span>
            </div>
            <ul className="max-h-56 overflow-y-auto rounded-lg border text-[12.5px]">
              {picked.map((p) => (
                <li key={p.rel} className="flex items-center gap-2 border-b px-3 py-1.5 last:border-b-0">
                  {/\.zip$/i.test(p.rel) ? <FileArchiveIcon className="size-3.5 shrink-0" /> : <FileTextIcon className="size-3.5 shrink-0" />}
                  <span className="truncate">{p.rel}</span>
                  {isSkillMd(p.rel) && (
                    <Badge variant="accent" className="ml-auto">
                      技能入口
                    </Badge>
                  )}
                </li>
              ))}
            </ul>
          </div>
        )}

        {result && result.names.length > 0 && (
          <div className="text-[13px] text-muted-foreground">
            已上传：
            {result.names.map((n) => (
              <code key={n} className="mx-0.5 rounded bg-muted px-1.5 py-0.5 font-mono text-[12px] text-foreground">
                {n}
              </code>
            ))}
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
        <Button onClick={submit} disabled={pending || picked.length === 0 || totalBytes > MAX_BYTES}>
          <UploadIcon />
          {pending ? "上传中…" : "上传"}
        </Button>
      </DialogFooter>
    </>
  )
}
