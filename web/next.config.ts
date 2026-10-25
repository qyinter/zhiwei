import type { NextConfig } from "next"

// Go 服务端地址（只在 Next 服务端使用，浏览器永远同源访问 /api）
const apiOrigin = process.env.ZHIWEI_API_ORIGIN ?? "http://127.0.0.1:8080"

const nextConfig: NextConfig = {
  cacheComponents: true,
  partialPrefetching: true,
  turbopack: {
    rules: {
      "*.css": {
        loaders: ["@tailwindcss/turbopack"],
        as: "*.css",
      },
    },
  },
  async rewrites() {
    return [
      { source: "/api/:path*", destination: `${apiOrigin}/api/:path*` },
      // QQ 机器人 webhook 回调：开放平台 → Next → Go（公网只需暴露 Next 一个端口）
      { source: "/webhook/:path*", destination: `${apiOrigin}/webhook/:path*` },
    ]
  },
}

export default nextConfig
