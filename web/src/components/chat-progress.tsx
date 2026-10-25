"use client"

import { UsersIcon } from "lucide-react"

import { EmptyState, formatTime } from "@/components/common"
import { Badge } from "@/components/ui/badge"
import { Card } from "@/components/ui/card"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { CHANNEL_LABEL, type ChatProgress, formatDuration } from "@/lib/types"

/** 每群扫描进度表（主动回复 / 反思沉淀共用）。滞后 = 应扫到的位置 − 游标 */
export function ChatProgressTable({ chats, extra }: { chats: ChatProgress[]; extra?: { label: string; value: (c: ChatProgress) => number } }) {
  if (chats.length === 0) {
    return (
      <Card className="py-0">
        <EmptyState icon={<UsersIcon className="size-5" />} title="群时间线为空" description="机器人在群里收到消息后才会出现；需要渠道能收到群消息。" />
      </Card>
    )
  }
  return (
    <Card className="py-0">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>群</TableHead>
            <TableHead>生效</TableHead>
            <TableHead className="text-right">缓冲消息</TableHead>
            <TableHead className="text-right">上次扫描</TableHead>
            <TableHead className="text-right">滞后</TableHead>
            {extra && <TableHead className="text-right">{extra.label}</TableHead>}
          </TableRow>
        </TableHeader>
        <TableBody>
          {chats.map((c) => (
            <TableRow key={`${c.channel}:${c.chatId}`}>
              <TableCell className="max-w-64">
                <div className="flex items-center gap-2">
                  <Badge variant="neutral">{CHANNEL_LABEL[c.channel] ?? c.channel}</Badge>
                  <span className="truncate">{c.title || <span className="font-mono text-xs">{c.chatId}</span>}</span>
                </div>
              </TableCell>
              <TableCell>
                {!c.allowed ? (
                  <Badge variant="neutral">不在白名单</Badge>
                ) : c.enabled ? (
                  <Badge variant="ok">生效</Badge>
                ) : (
                  <Badge variant="neutral">已关闭</Badge>
                )}
              </TableCell>
              <TableCell className="text-right font-mono tabular-nums">{c.messages}</TableCell>
              <TableCell className="text-right font-mono text-xs tabular-nums text-muted-foreground">{c.scanAt ? formatTime(c.scanAt) : "未扫描"}</TableCell>
              <TableCell className="text-right font-mono text-xs tabular-nums text-muted-foreground">{c.lagSec == null ? "—" : formatDuration(c.lagSec)}</TableCell>
              {extra && <TableCell className="text-right font-mono tabular-nums">{extra.value(c)}</TableCell>}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Card>
  )
}
