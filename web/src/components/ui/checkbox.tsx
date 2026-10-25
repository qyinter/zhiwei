"use client"

import * as React from "react"
import { Checkbox as CheckboxPrimitive } from "radix-ui"
import { CheckIcon, MinusIcon } from "lucide-react"

import { cn } from "@/lib/utils"

/**
 * 复选框（知微设计系统.html §表单控件）：18×18、圆角 5px、1.5px 边框；
 * 选中态品牌蓝底 + 白色对勾（stroke 3.5），带颜色过渡；禁用态降透明度；
 * 半选态（checked="indeterminate"，分节全选框部分勾选）同底色 + 白色横线。
 */
function Checkbox({ className, ...props }: React.ComponentProps<typeof CheckboxPrimitive.Root>) {
  return (
    <CheckboxPrimitive.Root
      data-slot="checkbox"
      className={cn(
        "peer size-4.5 shrink-0 cursor-pointer rounded-[5px] border-[1.5px] border-input bg-transparent outline-none transition-colors",
        "focus-visible:ring-2 focus-visible:ring-ring/40",
        "data-[state=checked]:border-auxiliary-blue data-[state=checked]:bg-auxiliary-blue dark:data-[state=checked]:border-auxiliary-blue-light dark:data-[state=checked]:bg-auxiliary-blue-light",
        "data-[state=indeterminate]:border-auxiliary-blue data-[state=indeterminate]:bg-auxiliary-blue dark:data-[state=indeterminate]:border-auxiliary-blue-light dark:data-[state=indeterminate]:bg-auxiliary-blue-light",
        "disabled:cursor-not-allowed disabled:opacity-50",
        className
      )}
      {...props}
    >
      <CheckboxPrimitive.Indicator className="grid place-items-center text-white" data-slot="checkbox-indicator">
        {props.checked === "indeterminate" ? (
          <MinusIcon className="size-3" strokeWidth={3.5} />
        ) : (
          <CheckIcon className="size-3" strokeWidth={3.5} />
        )}
      </CheckboxPrimitive.Indicator>
    </CheckboxPrimitive.Root>
  )
}

export { Checkbox }
