'use client'

// 分段标签（滑动指示块）：白色高亮块在标签间平滑滑动，而非点击直接切换。
import type { LucideIcon } from 'lucide-react'
import { useLayoutEffect, useRef, useState } from 'react'

import { cn } from '@/lib/utils'

export interface SegmentedTabItem<T extends string> {
  key: T
  label: string
  icon?: LucideIcon
  count?: number
  /** 内容标记：该项已有内容时在标签后显示品牌色小圆点（如多语言已填） */
  dot?: boolean
}

export function SegmentedTabs<T extends string>({
  items,
  value,
  onChange,
  className,
  fill
}: {
  items: SegmentedTabItem<T>[]
  value: T
  onChange: (key: T) => void
  className?: string
  /** true = 各标签等分撑满容器宽度（下拉/抽屉等定宽场景） */
  fill?: boolean
}) {
  const btnRefs = useRef<Record<string, HTMLButtonElement | null>>({})
  const [indicator, setIndicator] = useState<{ left: number; width: number }>({ left: 0, width: 0 })
  const [ready, setReady] = useState(false)

  useLayoutEffect(() => {
    const btn = btnRefs.current[value]
    if (btn) {
      setIndicator({ left: btn.offsetLeft, width: btn.offsetWidth })
      setReady(true)
    }
  }, [value, items])

  return (
    <div
      className={cn(
        'relative flex gap-1 overflow-x-auto rounded-xl border border-gray-100 bg-gray-50 p-1 dark:border-gray-700/60 dark:bg-gray-900/40',
        fill ? 'w-full' : 'w-fit',
        className
      )}
    >
      {/* 滑动指示块 */}
      <span
        aria-hidden
        className={cn(
          'absolute top-1 bottom-1 rounded-lg bg-white shadow-sm dark:bg-gray-700',
          ready ? 'transition-[left,width] duration-300 ease-out' : 'opacity-0'
        )}
        style={{ left: indicator.left, width: indicator.width }}
      />
      {items.map(({ key, label, icon: Icon, count, dot }) => {
        const active = key === value
        return (
          <button
            className={cn(
              'relative z-10 flex items-center gap-2 rounded-lg px-3.5 py-1.5 text-sm font-medium whitespace-nowrap transition-colors',
              fill && 'flex-1 justify-center',
              active ? 'text-heading dark:text-white' : 'text-text-secondary hover:text-heading dark:hover:text-white'
            )}
            key={key}
            onClick={() => onChange(key)}
            ref={(el) => {
              btnRefs.current[key] = el
            }}
            type="button"
          >
            {Icon && <Icon className="size-4" />}
            {label}
            {dot && (
              <span
                aria-hidden
                className={cn(
                  'size-1.5 rounded-full transition-colors',
                  active
                    ? 'bg-auxiliary-blue dark:bg-auxiliary-blue-light'
                    : 'bg-auxiliary-blue/40 dark:bg-auxiliary-blue-light/40'
                )}
              />
            )}
            {count !== undefined && (
              <span className="rounded-full bg-gray-100 px-1.5 text-xs tabular-nums text-text-secondary dark:bg-gray-800">
                {count}
              </span>
            )}
          </button>
        )
      })}
    </div>
  )
}
