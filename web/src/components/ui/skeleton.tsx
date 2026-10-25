import { cn } from "@/lib/utils"

/**
 * 骨架屏：微光自左向右扫过（globals.css 的 .skeleton-shimmer）。
 * 相比 opacity 脉动对比度更高、观感更连续；prefers-reduced-motion 下自动停用动画。
 */
function Skeleton({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="skeleton"
      className={cn("skeleton-shimmer rounded-md", className)}
      {...props}
    />
  )
}

export { Skeleton }
