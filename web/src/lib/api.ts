import { toast } from "sonner"

// 后端（Go）经 next.config.ts 的 rewrites 同源暴露在 /api/*，会话走 httpOnly cookie。
// 约定：成功直接返回数据；失败返回 {error} + HTTP 状态码。

export class ApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = "ApiError"
    this.status = status
  }
}

export interface RequestOptions {
  /** 不弹全局错误 toast（静默探测类请求） */
  silent?: boolean
  headers?: Record<string, string>
}

async function request<T>(method: string, url: string, body?: unknown, opts: RequestOptions = {}): Promise<T> {
  let res: Response
  try {
    res = await fetch(url, {
      method,
      headers: { "Content-Type": "application/json", ...opts.headers },
      body: body === undefined ? undefined : JSON.stringify(body),
      cache: "no-store",
    })
  } catch {
    const message = "网络异常，无法连接到服务端"
    if (!opts.silent) toast.error(message)
    throw new ApiError(-1, message)
  }

  if (res.status === 204) return undefined as T
  let data: unknown = undefined
  try {
    data = await res.json()
  } catch {
    // 无响应体
  }
  if (res.ok) return data as T

  const message = (data as { error?: string } | undefined)?.error ?? `请求失败（HTTP ${res.status}）`
  // 登录态失效：硬跳转重置内存状态（登录页自身的 401 是账密错误，照常提示）
  if (res.status === 401 && typeof window !== "undefined" && !url.startsWith("/api/auth/login")) {
    const here = window.location.pathname
    if (!here.startsWith("/login")) {
      // eslint-disable-next-line @next/next/no-location-assign-relative-destination -- 刻意硬跳转：清空内存中的登录态
      window.location.href = `/login?redirect=${encodeURIComponent(here)}`
      throw new ApiError(401, message)
    }
  }
  if (!opts.silent) toast.error(message)
  throw new ApiError(res.status, message)
}

export const api = {
  get: <T>(url: string, opts?: RequestOptions) => request<T>("GET", url, undefined, opts),
  post: <T>(url: string, body?: unknown, opts?: RequestOptions) => request<T>("POST", url, body ?? {}, opts),
  put: <T>(url: string, body?: unknown, opts?: RequestOptions) => request<T>("PUT", url, body, opts),
  patch: <T>(url: string, body?: unknown, opts?: RequestOptions) => request<T>("PATCH", url, body, opts),
  del: <T>(url: string, opts?: RequestOptions) => request<T>("DELETE", url, undefined, opts),
}

export interface Paged<T> {
  list: T[]
  total: number
  page: number
  pageSize: number
}
