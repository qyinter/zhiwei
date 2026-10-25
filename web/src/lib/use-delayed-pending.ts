'use client'

// 延迟加载指示（全局约定）：请求超过阈值才显示加载态——低于阈值人眼感知为
// 「即时」，切换骨架屏/按钮态反而造成闪烁。列表刷新用 useDelayedLoading，
// 保存等动作用 useDelayedPending。
import { useCallback, useRef, useState } from 'react'

/** 全局阈值（ms）：超过才显示加载态 */
export const PENDING_DELAY_MS = 200

function useDelayedFlag(initial: boolean, immediateFirst: boolean, delay: number) {
  const [flag, setFlag] = useState(initial)
  const firstDoneRef = useRef(false)
  // 并发计数：重叠请求以最后一个结束为准，避免快请求提前清掉慢请求的加载态
  const depthRef = useRef(0)

  const track = useCallback(
    async <T,>(task: () => Promise<T>): Promise<T> => {
      depthRef.current += 1
      const wait = immediateFirst && !firstDoneRef.current ? 0 : delay
      const timer = setTimeout(() => setFlag(true), wait)
      try {
        const result = await task()
        firstDoneRef.current = true
        return result
      } finally {
        clearTimeout(timer)
        depthRef.current -= 1
        if (depthRef.current === 0) setFlag(false)
      }
    },
    [delay, immediateFirst]
  )

  return { flag, track }
}

/**
 * 列表加载：首屏立即显示骨架（loading 初始为 true），此后刷新（删除/翻页/筛选）
 * 仅当请求超过阈值才重新显示，快速请求原地更新不闪烁。
 */
export function useDelayedLoading(delay = PENDING_DELAY_MS) {
  const { flag, track } = useDelayedFlag(true, true, delay)
  return { loading: flag, track }
}

/**
 * 动作 pending（保存/提交等）：始终延迟显示，快速完成的动作不闪按钮态。
 */
export function useDelayedPending(delay = PENDING_DELAY_MS) {
  const { flag, track } = useDelayedFlag(false, false, delay)
  return { pending: flag, track }
}
