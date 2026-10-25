"use client"

import { HeadsetIcon, ImageIcon, ImagePlusIcon, RotateCcwIcon, SendIcon, WrenchIcon, XIcon } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { toast } from "sonner"

import { Kicker, PageHeader } from "@/components/common"
import { ReplyMarkdown } from "@/components/reply-markdown"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Textarea } from "@/components/ui/textarea"
import { api } from "@/lib/api"
import type { KBHit } from "@/lib/types"
import { useDelayedPending } from "@/lib/use-delayed-pending"
import { cn } from "@/lib/utils"

interface Result {
  reply: string
  handoff: { reason: string; summary: string } | null
  steps: { tool: string; args: string; result: string }[]
  kbHits: KBHit[] | null
  usage: { inputTokens: number; outputTokens: number; cacheReadTokens: number }
  latencyMs: number
  model: string
  images: number
  imagesDropped: boolean
}

type Turn = { q: string; images: string[]; r?: Result; error?: string }

/** 与线上一致：每轮最多 4 张、单张 10MB 以内 */
const MAX_IMAGES = 4
const MAX_IMAGE_BYTES = 10 << 20

function readAsDataURL(f: File) {
  return new Promise<string>((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => resolve(String(r.result))
    r.onerror = () => reject(r.error)
    r.readAsDataURL(f)
  })
}

export default function PlaygroundPage() {
  const [turns, setTurns] = useState<Turn[]>([])
  const [text, setText] = useState("")
  const [images, setImages] = useState<string[]>([])
  const fileRef = useRef<HTMLInputElement>(null)
  const [reset, setReset] = useState(true)
  const [selected, setSelected] = useState<number | null>(null)
  const { pending, track } = useDelayedPending()
  const bottom = useRef<HTMLDivElement>(null)

  useEffect(() => {
    bottom.current?.scrollIntoView({ block: "end" })
  }, [turns.length])

  const addFiles = async (files: Iterable<File>) => {
    const picked = Array.from(files).filter((f) => f.type.startsWith("image/"))
    if (picked.some((f) => f.size > MAX_IMAGE_BYTES)) toast.error("单张图片不能超过 10MB")
    const ok = picked.filter((f) => f.size <= MAX_IMAGE_BYTES)
    const urls = await Promise.all(ok.map(readAsDataURL))
    setImages((cur) => {
      const next = [...cur, ...urls]
      if (next.length > MAX_IMAGES) toast.error(`一次最多 ${MAX_IMAGES} 张图片`)
      return next.slice(0, MAX_IMAGES)
    })
  }

  const send = () => {
    const q = text.trim()
    if ((!q && images.length === 0) || pending) return
    const imgs = images
    setText("")
    setImages([])
    const idx = turns.length
    setTurns((t) => [...t, { q, images: imgs }])
    track(async () => {
      try {
        const r = await api.post<Result>("/api/playground", { text: q, images: imgs, reset }, { silent: true })
        setTurns((t) => t.map((x, i) => (i === idx ? { ...x, r } : x)))
        setSelected(idx)
      } catch (e) {
        setTurns((t) => t.map((x, i) => (i === idx ? { ...x, error: (e as Error).message } : x)))
      }
      setReset(false)
    })
  }

  const current = selected != null ? turns[selected]?.r : undefined

  return (
    <>
      <PageHeader
        title="体验台"
        description="与线上同一套提示词、知识库与工具，不经过任何渠道。右侧可查看每轮的检索命中与工具调用。"
        actions={
          <Button variant="outline" size="sm" onClick={() => { setTurns([]); setSelected(null); setReset(true) }}>
            <RotateCcwIcon />
            新对话
          </Button>
        }
      />
      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_340px]">
        <Card className="flex h-[calc(100vh-220px)] min-h-[480px] flex-col gap-0 py-0">
          <div className="flex-1 overflow-y-auto p-5">
            {turns.length === 0 ? (
              <p className="py-20 text-center text-sm text-muted-foreground">以用户的口吻问一个问题试试，例如「怎么充值？」</p>
            ) : (
              <div className="grid gap-4">
                {turns.map((t, i) => (
                  <div key={i} className="grid gap-2">
                    {t.images.length > 0 && (
                      <div className="ml-auto flex max-w-[80%] flex-wrap justify-end gap-1.5">
                        {t.images.map((src, j) => (
                          // eslint-disable-next-line @next/next/no-img-element -- 本地预览的 data URL
                          <img key={j} src={src} alt="上传的图片" className="max-h-32 max-w-40 rounded-lg object-contain ring-1 ring-foreground/10" />
                        ))}
                      </div>
                    )}
                    {t.q && <div className="ml-auto max-w-[80%] rounded-2xl bg-primary px-3.5 py-2 text-[13.5px] whitespace-pre-wrap text-primary-foreground">{t.q}</div>}
                    {/* 回复里可能有链接、代码块等块级内容，不能放进 <button>，用可聚焦的 div 承担点击 */}
                    <div
                      role="button"
                      tabIndex={0}
                      onClick={() => setSelected(i)}
                      onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && (e.preventDefault(), setSelected(i))}
                      className={cn(
                        "max-w-[85%] cursor-pointer rounded-2xl px-3.5 py-2 text-left text-[13.5px] leading-relaxed ring-1 ring-foreground/10 transition",
                        selected === i ? "bg-accent-soft ring-primary/40" : "bg-card hover:bg-muted"
                      )}
                    >
                      {t.error ? (
                        <span className="text-crit">{t.error}</span>
                      ) : t.r ? (
                        <>
                          {t.r.reply ? <ReplyMarkdown source={t.r.reply} /> : <span className="text-muted-foreground">（模型选择不回复）</span>}
                          <span className="mt-1.5 flex flex-wrap gap-1.5">
                            {t.r.steps.length > 0 && (
                              <Badge variant="neutral">
                                <WrenchIcon />
                                {t.r.steps.length} 次工具
                              </Badge>
                            )}
                            {t.r.images > 0 && (
                              <Badge variant="neutral">
                                <ImageIcon />
                                看了 {t.r.images} 张图
                              </Badge>
                            )}
                            {t.r.imagesDropped && <Badge variant="warn">模型拒收图片，已去图重试</Badge>}
                            {t.r.handoff && (
                              <Badge variant="warn">
                                <HeadsetIcon />
                                触发转人工
                              </Badge>
                            )}
                          </span>
                        </>
                      ) : (
                        <span className="text-muted-foreground">思考中…</span>
                      )}
                    </div>
                  </div>
                ))}
                <div ref={bottom} />
              </div>
            )}
          </div>
          {images.length > 0 && (
            <div className="flex flex-wrap gap-2 border-t px-4 pt-3">
              {images.map((src, i) => (
                <div key={i} className="relative">
                  {/* eslint-disable-next-line @next/next/no-img-element -- 本地预览的 data URL */}
                  <img src={src} alt={`待发送图片 ${i + 1}`} className="size-16 rounded-lg object-cover ring-1 ring-foreground/10" />
                  <button
                    type="button"
                    aria-label="移除这张图片"
                    onClick={() => setImages((cur) => cur.filter((_, j) => j !== i))}
                    className="absolute -top-1.5 -right-1.5 grid size-5 place-items-center rounded-full bg-foreground text-background"
                  >
                    <XIcon className="size-3" />
                  </button>
                </div>
              ))}
            </div>
          )}
          <div className="flex gap-2 border-t p-4">
            <input
              ref={fileRef}
              type="file"
              accept="image/*"
              multiple
              className="hidden"
              onChange={(e) => {
                if (e.target.files) addFiles(e.target.files).catch(() => toast.error("读取图片失败"))
                e.target.value = ""
              }}
            />
            <Button variant="outline" className="h-auto" onClick={() => fileRef.current?.click()} disabled={images.length >= MAX_IMAGES} aria-label="添加图片">
              <ImagePlusIcon />
            </Button>
            <Textarea
              rows={2}
              className="min-h-0 resize-none"
              placeholder="输入消息，Enter 发送，Shift + Enter 换行；可直接粘贴截图"
              value={text}
              onChange={(e) => setText(e.target.value)}
              onPaste={(e) => {
                const files = Array.from(e.clipboardData.files).filter((f) => f.type.startsWith("image/"))
                if (files.length > 0) {
                  e.preventDefault()
                  addFiles(files).catch(() => toast.error("读取图片失败"))
                }
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                  e.preventDefault()
                  send()
                }
              }}
            />
            <Button className="h-auto" onClick={send} disabled={pending || (!text.trim() && images.length === 0)} aria-label="发送">
              <SendIcon />
            </Button>
          </div>
        </Card>

        <Card className="py-0">
          <CardContent className="grid content-start gap-5 p-5">
            <Kicker>本轮详情</Kicker>
            {!current ? (
              <p className="text-[13px] text-muted-foreground">发送消息后，点击任意一条回复查看详情。</p>
            ) : (
              <>
                <dl className="grid grid-cols-2 gap-3 text-[13px]">
                  <div>
                    <dt className="text-xs text-muted-foreground">模型</dt>
                    <dd className="truncate font-mono">{current.model}</dd>
                  </div>
                  <div>
                    <dt className="text-xs text-muted-foreground">耗时</dt>
                    <dd className="font-mono tabular-nums">{current.latencyMs}ms</dd>
                  </div>
                  <div>
                    <dt className="text-xs text-muted-foreground">输入 / 输出</dt>
                    <dd className="font-mono tabular-nums">{current.usage.inputTokens} / {current.usage.outputTokens}</dd>
                  </div>
                  <div>
                    <dt className="text-xs text-muted-foreground">缓存命中</dt>
                    <dd className="font-mono tabular-nums">{current.usage.cacheReadTokens}</dd>
                  </div>
                </dl>
                {current.handoff && (
                  <div className="rounded-lg bg-warn-soft p-3 text-xs text-warn">
                    转人工：{current.handoff.reason} — {current.handoff.summary}
                    <div className="mt-1 opacity-80">体验台只展示，不会真正挂起会话。</div>
                  </div>
                )}
                <section className="grid gap-2">
                  <div className="text-[13px] font-medium">预检索命中（{current.kbHits?.length ?? 0}）</div>
                  {current.kbHits?.map((h) => (
                    <div key={h.chunkId} className="rounded-lg border p-2.5 text-xs">
                      <div className="mb-1 flex justify-between font-mono text-muted-foreground">
                        <span className="truncate">{h.docTitle}{h.heading && ` / ${h.heading}`}</span>
                        <span>{h.score.toFixed(2)}</span>
                      </div>
                      <p className="line-clamp-3">{h.content}</p>
                    </div>
                  ))}
                </section>
                <section className="grid gap-2">
                  <div className="text-[13px] font-medium">工具调用（{current.steps.length}）</div>
                  {current.steps.map((s, i) => (
                    <details key={i} className="rounded-lg border text-xs">
                      <summary className="cursor-pointer px-2.5 py-2 font-mono">
                        {s.tool}({s.args})
                      </summary>
                      <pre className="max-h-48 overflow-auto border-t bg-muted/50 p-2.5 font-mono whitespace-pre-wrap">{s.result}</pre>
                    </details>
                  ))}
                </section>
              </>
            )}
          </CardContent>
        </Card>
      </div>
    </>
  )
}
