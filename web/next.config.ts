import type { NextConfig } from "next"

// Go 服务端地址（只在 Next 服务端使用，浏览器永远同源访问 /api）。
// rewrites 在 next build 时写进构建产物，所以要在构建前设置，运行时再改无效。
const apiOrigin = process.env.ZHIWEI_API_ORIGIN ?? "http://127.0.0.1:8080"

const nextConfig: NextConfig = {
  // Docker 镜像构建时设 NEXT_OUTPUT=standalone（见 web/Dockerfile），本地 pnpm start 不受影响
  output: process.env.NEXT_OUTPUT === "standalone" ? "standalone" : undefined,
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
