"use client"

import * as React from "react"
import { Dialog as DialogPrimitive } from "radix-ui"

import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { XIcon } from "lucide-react"

function Dialog({
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Root>) {
  return <DialogPrimitive.Root data-slot="dialog" {...props} />
}

function DialogTrigger({
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Trigger>) {
  return <DialogPrimitive.Trigger data-slot="dialog-trigger" {...props} />
}

function DialogPortal({
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Portal>) {
  return <DialogPrimitive.Portal data-slot="dialog-portal" {...props} />
}

function DialogClose({
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Close>) {
  return <DialogPrimitive.Close data-slot="dialog-close" {...props} />
}

function DialogOverlay({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Overlay>) {
  return (
    <DialogPrimitive.Overlay
      data-slot="dialog-overlay"
      className={cn(
        "fixed inset-0 isolate z-50 bg-black/10 duration-100 supports-backdrop-filter:backdrop-blur-xs data-open:animate-in data-open:fade-in-0 data-closed:animate-out data-closed:fade-out-0",
        className
      )}
      {...props}
    />
  )
}

/**
 * 对话框内容。**默认限高并让内容自己滚动**。
 *
 * 原先这里既没有 max-height 也没有 overflow：内容一长，弹窗就顺着
 * `top-1/2 -translate-y-1/2` 往视口外两头长，而背景页此时是锁住的——
 * 于是既看不到底部的按钮，也没有任何地方能滚。表单类弹窗（密钥编辑那种
 * 十几个字段的）必然踩中，而每个调用方各自补一遍 `max-h-[85vh] overflow-y-auto`
 * 只会补漏一半：全站 34 个弹窗里当时只有 4 个补了。
 *
 * 实现上滚动条挂在**内层**包裹层而不是 Content 本身，这样右上角的关闭按钮
 * （相对 Content 绝对定位）不会跟着内容滚走——长表单里滚到一半找不到关闭按钮
 * 是另一种别扭。
 *
 * `scrollable={false}` 留给自己管理高度与滚动区域的弹窗（例如「头部固定 +
 * 列表区自己滚」的那几个）：此时行为与改动前完全一致，padding 与栅格间距回到
 * Content 上，由调用方的 className 接管。
 */
function DialogContent({
  className,
  children,
  showCloseButton = true,
  scrollable = true,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Content> & {
  showCloseButton?: boolean
  scrollable?: boolean
}) {
  return (
    <DialogPortal>
      <DialogOverlay />
      <DialogPrimitive.Content
        data-slot="dialog-content"
        className={cn(
          "fixed top-1/2 left-1/2 z-50 w-full max-w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 rounded-2xl bg-popover text-sm text-popover-foreground ring-1 ring-foreground/10 duration-100 outline-none sm:max-w-sm data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95",
          // 栅格列写成 minmax(0,1fr)：grid 轨道的默认最小宽是 auto，一段没有断点的
          // 长字符串（邮箱、token、URL）会把轨道连同整个弹窗撑出视口，横向出现滚动条。
          // 自管滚动时保持原样：栅格 + 间距 + 内边距都在 Content 上
          scrollable ? "grid grid-cols-[minmax(0,1fr)]" : "grid grid-cols-[minmax(0,1fr)] gap-4 p-4",
          className
        )}
        {...props}
      >
        {scrollable ? (
          // dvh 而不是 vh：移动端浏览器地址栏收起前后 vh 不变，会算出超过实际
          // 可视高度的框，底部按钮照样够不着。
          // auto-rows-max：限高 grid 的 auto 行在内容超高时会把子项压到各自的
          // 最小高度，而滚动容器（SegmentedTabs 之类带 overflow-x-auto 的）最小
          // 高度是 0——整条标签栏被压成一道缝。行高按内容定死，超出交给滚动。
          <div
            data-slot="dialog-body"
            className="grid auto-rows-max grid-cols-[minmax(0,1fr)] max-h-[calc(100dvh-4rem)] gap-4 overflow-y-auto overscroll-contain p-4"
          >
            {children}
          </div>
        ) : (
          children
        )}
        {showCloseButton && (
          <DialogPrimitive.Close data-slot="dialog-close" asChild>
            <Button
              variant="ghost"
              className="absolute top-2 right-2 z-10"
              size="icon-sm"
            >
              <XIcon
              />
              <span className="sr-only">Close</span>
            </Button>
          </DialogPrimitive.Close>
        )}
      </DialogPrimitive.Content>
    </DialogPortal>
  )
}

function DialogHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="dialog-header"
      className={cn("flex flex-col gap-2", className)}
      {...props}
    />
  )
}

function DialogFooter({
  className,
  showCloseButton = false,
  children,
  ...props
}: React.ComponentProps<"div"> & {
  showCloseButton?: boolean
}) {
  return (
    <div
      data-slot="dialog-footer"
      className={cn(
        "-mx-4 -mb-4 flex flex-col-reverse gap-2 rounded-b-2xl border-t bg-muted/50 p-4 sm:flex-row sm:justify-end",
        className
      )}
      {...props}
    >
      {children}
      {showCloseButton && (
        <DialogPrimitive.Close asChild>
          <Button variant="outline">Close</Button>
        </DialogPrimitive.Close>
      )}
    </div>
  )
}

function DialogTitle({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Title>) {
  return (
    <DialogPrimitive.Title
      data-slot="dialog-title"
      className={cn(
        "font-heading text-base leading-none font-medium [overflow-wrap:anywhere]",
        className
      )}
      {...props}
    />
  )
}

function DialogDescription({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Description>) {
  return (
    <DialogPrimitive.Description
      data-slot="dialog-description"
      className={cn(
        "text-sm text-muted-foreground [overflow-wrap:anywhere] *:[a]:underline *:[a]:underline-offset-3 *:[a]:hover:text-foreground",
        className
      )}
      {...props}
    />
  )
}

export {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogOverlay,
  DialogPortal,
  DialogTitle,
  DialogTrigger,
}
