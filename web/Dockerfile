# 知微管理后台（Next.js standalone 输出）。
# 产物是纯 JS，依赖安装和构建都跑在构建机架构上，多架构构建不走 QEMU。
FROM --platform=$BUILDPLATFORM node:22-alpine AS build
WORKDIR /app
RUN corepack enable
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY . .
# Go 服务地址在构建时写进 rewrites（next.config 构建期序列化，运行时改 env 无效）。
# 缺省对应 docker-compose.yml 里的 server 服务；部署方式不同时用 --build-arg 自行构建。
ARG ZHIWEI_API_ORIGIN=http://server:8080
ENV ZHIWEI_API_ORIGIN=$ZHIWEI_API_ORIGIN \
    NEXT_OUTPUT=standalone \
    NEXT_TELEMETRY_DISABLED=1
RUN pnpm build

FROM node:22-alpine
WORKDIR /app
ENV NODE_ENV=production \
    PORT=3000 \
    HOSTNAME=0.0.0.0 \
    NEXT_TELEMETRY_DISABLED=1
COPY --from=build --chown=node:node /app/.next/standalone ./
COPY --from=build --chown=node:node /app/.next/static ./.next/static
USER node
EXPOSE 3000
CMD ["node", "server.js"]
