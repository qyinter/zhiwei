"use client"

import { MessagesSquareIcon, SearchIcon } from "lucide-react"
import { useCallback, useEffect, useState } from "react"

import { ConvStatusBadge, EmptyState, formatTime, PageHeader, Pager } from "@/components/common"
import { ConversationSheet } from "@/components/conversation-panel"
import { Badge } from "@/components/ui/badge"
import { Card } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api, type Paged } from "@/lib/api"
import { CHANNEL_LABEL, CHAT_TYPE_LABEL, type Conversation } from "@/lib/types"
import { useDelayedLoading } from "@/lib/use-delayed-pending"

const ALL = "all"

export default function ConversationsPage() {
  const [data, setData] = useState<Paged<Conversation> | null>(null)
  const [page, setPage] = useState(1)
  const [channel, setChannel] = useState(ALL)
  const [status, setStatus] = useState(ALL)
  const [keyword, setKeyword] = useState("")
  const [query, setQuery] = useState("")
  const [openId, setOpenId] = useState<number | null>(null)
  const { loading, track } = useDelayedLoading()

  const load = useCallback(() => {
    const q = new URLSearchParams({ page: String(page) })
    if (channel !== ALL) q.set("channel", channel)
    if (status !== ALL) q.set("status", status)
    if (query) q.set("keyword", query)
    return track(() => api.get<Paged<Conversation>>(`/api/conversations?${q}`).then(setData)).catch(() => {})
  }, [page, channel, status, query, track])

  useEffect(() => {
    load()
  }, [load])

  return (
    <>
      <PageHeader title="会话" description="每个渠道 × 群 × 用户一个会话。点击查看消息、人工回复或重置上下文。" />
      <div className="mb-4 flex flex-wrap gap-2">
        <form
          className="relative min-w-56 flex-1"
          onSubmit={(e) => {
            e.preventDefault()
            setPage(1)
            setQuery(keyword.trim())
          }}
        >
          <SearchIcon className="absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input className="pl-9" placeholder="搜索用户名 / 用户 ID / 群 ID，回车确认" value={keyword} onChange={(e) => setKeyword(e.target.value)} />
        </form>
        <Select value={channel} onValueChange={(v) => { setPage(1); setChannel(v) }}>
          <SelectTrigger className="w-36">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>全部渠道</SelectItem>
            <SelectItem value="qqbot">QQ 机器人</SelectItem>
            <SelectItem value="telegram">Telegram</SelectItem>
            <SelectItem value="playground">体验台</SelectItem>
          </SelectContent>
        </Select>
        <Select value={status} onValueChange={(v) => { setPage(1); setStatus(v) }}>
          <SelectTrigger className="w-32">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>全部状态</SelectItem>
            <SelectItem value="active">自动回复</SelectItem>
            <SelectItem value="handoff">人工接待</SelectItem>
          </SelectContent>
        </Select>
      </div>

      <Card className="py-0">
        {loading && !data ? (
          <div className="grid gap-2 p-4">
            {Array.from({ length: 6 }).map((_, i) => (
              <Skeleton key={i} className="h-10" />
            ))}
          </div>
        ) : data && data.list.length === 0 ? (
          <EmptyState icon={<MessagesSquareIcon className="size-5" />} title="暂无会话" description="渠道收到用户消息后会出现在这里；也可以先去体验台试一试。" />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>用户</TableHead>
                <TableHead>渠道</TableHead>
                <TableHead>群 / 聊天</TableHead>
                <TableHead>状态</TableHead>
                <TableHead className="text-right">消息</TableHead>
                <TableHead className="text-right">最近活跃</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data?.list.map((c) => (
                <TableRow key={c.id} className="cursor-pointer" onClick={() => setOpenId(c.id)}>
                  <TableCell className="max-w-48 truncate font-medium">{c.userName || <span className="font-mono text-xs">{c.userId}</span>}</TableCell>
                  <TableCell>
                    <Badge variant="neutral">{CHANNEL_LABEL[c.channel] ?? c.channel}</Badge>
                  </TableCell>
                  <TableCell className="max-w-56 truncate text-muted-foreground">
                    {CHAT_TYPE_LABEL[c.chatType]} · <span className="font-mono text-xs">{c.title || c.chatId}</span>
                  </TableCell>
                  <TableCell>
                    <ConvStatusBadge status={c.status} />
                  </TableCell>
                  <TableCell className="text-right font-mono tabular-nums">{c.messageCount}</TableCell>
                  <TableCell className="text-right font-mono text-xs tabular-nums text-muted-foreground">{formatTime(c.lastMessageAt)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Card>
      {data && <Pager page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
      <ConversationSheet convId={openId} onClose={() => setOpenId(null)} onChanged={load} />
    </>
  )
}
