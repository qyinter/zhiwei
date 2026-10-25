"use client"

import { HeadsetIcon, PlayIcon, RotateCcwIcon, SendIcon } from "lucide-react"
import { useCallback, useEffect, useRef, useState } from "react"
import { toast } from "sonner"

import { ConvStatusBadge, formatTime, Kicker } from "@/components/common"
import { ReplyMarkdown } from "@/components/reply-markdown"
import { Button } from "@/components/ui/button"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"
import { Textarea } from "@/components/ui/textarea"
import { api } from "@/lib/api"
import { CHANNEL_LABEL, CHAT_TYPE_LABEL, type Conversation, type Message } from "@/lib/types"
import { useDelayedPending } from "@/lib/use-delayed-pending"
import { cn } from "@/lib/utils"

const IMAGE_MARK = /\s*\[图片(×\d+)?\]/g

/** 客户发来的图片缩略图，点开看原图（已缩放到长边 1568 像素）。 */
export function MessageImages({ images }: { images: NonNullable<Message["images"]> }) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {images.map((im) => (
        <a key={im.id} href={`/api/message-images/${im.id}`} target="_blank" rel="noreferrer" className="block overflow-hidden rounded-lg ring-1 ring-foreground/10">
          {/* eslint-disable-next-line @next/next/no-img-element -- 后台鉴权接口的原图，不走 next/image 优化 */}
          <img src={`/api/message-images/${im.id}`} alt="客户发来的图片" width={im.width} height={im.height} loading="lazy" className="h-auto max-h-40 w-auto max-w-56 object-contain" />
        </a>
      ))}
    </div>
  )
}

/** 会话详情抽屉：消息流 + 人工回复 + 恢复自动 / 重置上下文。会话列表与人工队列共用。 */
export function ConversationSheet({ convId, onClose, onChanged }: { convId: number | null; onClose: () => void; onChanged?: () => void }) {
  const [conv, setConv] = useState<Conversation | null>(null)
  const [messages, setMessages] = useState<Message[] | null>(null)
  const [text, setText] = useState("")
  const { pending, track } = useDelayedPending()
  const bottom = useRef<HTMLDivElement>(null)

  const load = useCallback(async () => {
    if (convId == null) return
    const [c, m] = await Promise.all([
      api.get<Conversation>(`/api/conversations/${convId}`),
      api.get<{ list: Message[] }>(`/api/conversations/${convId}/messages`),
    ])
    setConv(c)
    setMessages(m.list)
  }, [convId])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 切换会话时先清空旧内容，避免闪现上一个会话
    setConv(null)
    setMessages(null)
    load().catch(() => {})
    if (convId == null) return
    const t = setInterval(() => load().catch(() => {}), 5000)
    return () => clearInterval(t)
  }, [convId, load])

  useEffect(() => {
    bottom.current?.scrollIntoView({ block: "end" })
  }, [messages?.length])

  const act = (fn: () => Promise<unknown>, ok: string) =>
    track(async () => {
      await fn()
      toast.success(ok)
      await load()
      onChanged?.()
    }).catch(() => {})

  const send = () => {
    if (!text.trim()) return
    act(async () => {
      await api.post(`/api/conversations/${convId}/reply`, { text })
      setText("")
    }, "已发送")
  }

  return (
    <Sheet open={convId != null} onOpenChange={(o) => !o && onClose()}>
      <SheetContent className="flex w-full flex-col gap-0 p-0 sm:max-w-xl">
        <SheetHeader className="border-b px-5 py-4">
          <SheetTitle className="flex items-center gap-2">
            {conv ? conv.userName || conv.userId.slice(0, 12) : "会话"}
            {conv && <ConvStatusBadge status={conv.status} />}
          </SheetTitle>
          <SheetDescription className="truncate font-mono text-xs">
            {conv ? `${CHANNEL_LABEL[conv.channel] ?? conv.channel} · ${CHAT_TYPE_LABEL[conv.chatType] ?? conv.chatType} · ${conv.title || conv.chatId}` : "加载中…"}
          </SheetDescription>
        </SheetHeader>

        <div className="flex-1 overflow-y-auto bg-background px-5 py-4">
          {messages == null ? (
            <div className="grid gap-3">
              <Skeleton className="h-12 w-2/3" />
              <Skeleton className="ml-auto h-16 w-3/4" />
            </div>
          ) : messages.length === 0 ? (
            <p className="py-10 text-center text-sm text-muted-foreground">暂无消息</p>
          ) : (
            <div className="grid gap-3">
              {messages.map((m) => {
                const mine = m.direction !== "in"
                const pastContext = conv && new Date(m.createdAt) < new Date(conv.contextSince)
                return (
                  <div key={m.id} className={cn("flex flex-col gap-1", mine ? "items-end" : "items-start", pastContext && "opacity-55")}>
                    <Kicker className="normal-case">
                      {m.direction === "in" ? m.senderName || "用户" : m.direction === "human" ? `人工 · ${m.senderName}` : "机器人"} · {formatTime(m.createdAt)}
                    </Kicker>
                    <div
                      className={cn(
                        "max-w-[85%] rounded-2xl px-3.5 py-2 text-[13.5px] leading-relaxed",
                        m.direction !== "out" && "whitespace-pre-wrap",
                        m.direction === "in" && "bg-card ring-1 ring-foreground/10",
                        m.direction === "out" && "bg-accent-soft text-foreground",
                        m.direction === "human" && "bg-warn-soft text-foreground"
                      )}
                    >
                      {m.direction === "out" ? (
                        <ReplyMarkdown source={m.content} />
                      ) : m.images?.length ? (
                        // 图片还在就直接展示，正文里的「[图片]」占位不再重复（30 天后图片清理，占位留着说明发过图）
                        <div className="grid gap-2">
                          {m.content.replace(IMAGE_MARK, "").trim() && <span>{m.content.replace(IMAGE_MARK, "").trim()}</span>}
                          <MessageImages images={m.images} />
                        </div>
                      ) : (
                        m.content
                      )}
                    </div>
                  </div>
                )
              })}
              <div ref={bottom} />
            </div>
          )}
        </div>

        <div className="grid gap-3 border-t bg-card px-5 py-4">
          {conv?.channel === "qqbot" && (
            <p className="text-xs text-muted-foreground">QQ：用户最后一条消息 5 分钟内走被动回复；超过后改发主动消息，受平台频控（单群 20 条/分钟、每天 1000 条），用户关闭「允许主动发送」时会失败。</p>
          )}
          <Textarea
            rows={3}
            placeholder={conv?.channel === "playground" ? "体验台会话不支持人工回复" : "以机器人身份人工回复…（⌘/Ctrl + Enter 发送）"}
            value={text}
            disabled={conv?.channel === "playground"}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) send()
            }}
          />
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex gap-2">
              {conv?.status === "handoff" ? (
                <Button size="sm" variant="outline" disabled={pending} onClick={() => act(() => api.post(`/api/conversations/${convId}/resume`), "已恢复自动回复")}>
                  <PlayIcon />
                  处理完毕，恢复自动
                </Button>
              ) : (
                <Button size="sm" variant="ghost" disabled={pending || !conv} onClick={() => act(() => api.post(`/api/conversations/${convId}/reset`), "上下文已重置")}>
                  <RotateCcwIcon />
                  重置上下文
                </Button>
              )}
            </div>
            <Button size="sm" disabled={pending || !text.trim() || conv?.channel === "playground"} onClick={send}>
              {conv?.status === "handoff" ? <HeadsetIcon /> : <SendIcon />}
              发送
            </Button>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  )
}
