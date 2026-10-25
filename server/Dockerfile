# 知微 Go 服务。纯 Go（不依赖 cgo），编译阶段跑在构建机架构上交叉编译，多架构构建不走 QEMU。
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/zhiwei ./cmd/zhiwei

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H -u 10001 zhiwei
COPY --from=build /out/zhiwei /usr/local/bin/zhiwei
USER zhiwei
# 容器里要监听全部网卡，web 容器才连得上；对外仍只暴露 web
ENV ZHIWEI_HTTP_ADDR=0.0.0.0:8080
EXPOSE 8080
ENTRYPOINT ["zhiwei"]
