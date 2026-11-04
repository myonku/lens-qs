# ---- 构建阶段 ----
FROM golang:1.26-alpine AS builder

WORKDIR /src
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}

# 先复制依赖清单，利用层缓存
COPY go.mod go.sum ./
RUN go mod download

# 复制源码并编译
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/lens-qs-api ./cmd/api \
 && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/lens-qs-migrate ./cmd/migrate \
 && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/lens-qs-seed ./cmd/seed \
 && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/lens-qs-createuser ./cmd/createuser \
 && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/lens-qs-reset-testdata ./cmd/reset-testdata

# ---- 运行阶段 ----
FROM alpine:3.20

# 时区与证书（容器内按 Asia/Shanghai 记录时间更贴近现场）
RUN apk add --no-cache ca-certificates tzdata curl \
 && ln -sf /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
 && echo "Asia/Shanghai" > /etc/timezone

WORKDIR /app
COPY --from=builder /out/ /app/
# 参考部件号清单（seed 命令使用）
COPY data/*.csv /app/data/

ENV APP_ENV=prod \
    APP_ADDR=:8080 \
    DB_DRIVER=postgres \
    AUTO_MIGRATE=true

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD curl -fsS http://127.0.0.1:8080/healthz || exit 1

COPY docker-entrypoint.sh /app/docker-entrypoint.sh
RUN sed -i 's/\r$//' /app/docker-entrypoint.sh \
 && chmod +x /app/docker-entrypoint.sh

ENTRYPOINT ["/app/docker-entrypoint.sh"]
CMD ["/app/lens-qs-api"]
