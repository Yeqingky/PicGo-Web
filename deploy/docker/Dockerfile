# PicGo-Web 生产镜像
#
# ⚠️ 构建上下文必须是**仓库根目录**（PicGo-Web/），因为它需要 web/、picgo-agent/、
#    server/ 与 deploy/vendor/picgo-*.tgz。
#
# 前置（一次即可）：
#   make vendor        # 把打过补丁的 PicGo-Core 打成 deploy/vendor/picgo-<ver>.tgz
#
# 构建：
#   docker build -f deploy/docker/Dockerfile -t picgo-web:latest .
#
# 或直接：make docker-build

# ===========================================================================
# 阶段 1：构建前端 SPA（内置界面）
# ===========================================================================
FROM node:24-alpine AS web-builder
WORKDIR /build/web

RUN corepack enable

# 先只拷清单，利用 layer 缓存
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

COPY web/ ./
RUN pnpm build


# ===========================================================================
# 阶段 2：构建 picgo-agent 侧车
# ===========================================================================
FROM node:24-alpine AS agent-builder
WORKDIR /build/agent

RUN corepack enable

# picgo-agent 的 package.json 里 picgo 依赖是 `file:../../PicGo-Core`（本地开发用路径）。
# 镜像里没有那个目录，因此改用 `make vendor` 产出的 tarball。
COPY deploy/vendor/ /vendor/
COPY picgo-agent/package.json picgo-agent/pnpm-lock.yaml ./
RUN set -eux; \
    PKG=$(ls /vendor/picgo-*.tgz | head -n1); \
    echo "使用 vendored picgo: ${PKG}"; \
    node -e "\
      const fs=require('fs'); \
      const p=JSON.parse(fs.readFileSync('package.json','utf8')); \
      p.dependencies.picgo='file:'+process.argv[1]; \
      fs.writeFileSync('package.json', JSON.stringify(p,null,2)); \
    " "$PKG"; \
    pnpm install --frozen-lockfile=false

COPY picgo-agent/ ./
RUN pnpm build

# 只保留生产依赖，缩小体积
RUN pnpm prune --prod


# ===========================================================================
# 阶段 3：编译 Go 后端（把前端产物嵌入 webfs）
# ===========================================================================
FROM golang:1.25-alpine AS server-builder
WORKDIR /build/server

# 纯 Go：SQLite 用 glebarez/sqlite，PgSQL 用 pgx，均不需要 CGO
ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOFLAGS=-mod=mod

# 依赖缓存
COPY server/go.mod server/go.sum ./
RUN go mod download

COPY server/ ./

# 把阶段 1 的前端产物放进 go:embed 目录。
#
# ⚠️ 跨构建阶段**不能**用 `cp`（各阶段的文件系统互相隔离），必须 `COPY --from=`；
#     否则这里会静默失败或复制不到东西，最终二进制里没有前端
#     （表现为「服务起来了但所有页面 404」）。
#     `internal/webfs` 用 `//go:embed all:dist` 嵌入，因此必须在 go build **之前**就位。
RUN rm -rf internal/webfs/dist/assets && \
    find internal/webfs/dist -maxdepth 1 -type f ! -name '.gitkeep' -delete

COPY --from=web-builder /build/web/dist/ ./internal/webfs/dist/

# 校验前端确实就位（否则早点失败，而不是产出一个没前端的二进制）
RUN test -f internal/webfs/dist/index.html \
    || (echo "错误：前端产物未进入 webfs/dist，go:embed 将嵌入空目录" && exit 1)
RUN test -n "$(find internal/webfs/dist/assets -type f -print -quit 2>/dev/null)" \
    || (echo "错误：webfs/dist/assets 为空" && exit 1)

RUN go build -trimpath -ldflags "-s -w" -o /out/picgo-web ./cmd/picgo-web


# ===========================================================================
# 阶段 4：运行时
# ===========================================================================
FROM alpine:3.21

# 1) node 运行时：picgo-agent 是 Node 侧车，必须存在
# 2) ca-certificates：调图床 / GitHub OAuth / ACG API 需要
# 3) tzdata：站点与日志使用 Asia/Shanghai
# 4) wget（busybox 自带）：healthcheck 用
RUN apk add --no-cache nodejs ca-certificates tzdata

WORKDIR /app

# Go 二进制
COPY --from=server-builder /out/picgo-web /app/picgo-web

# agent（含其 node_modules；picgo-core 已作为 file: tarball 装进 node_modules）
COPY --from=agent-builder /build/agent/dist /app/picgo-agent/dist
COPY --from=agent-builder /build/agent/node_modules /app/picgo-agent/node_modules
COPY --from=agent-builder /build/agent/package.json /app/picgo-agent/package.json

# 内嵌默认主题的 seed 源（D94：主题缺失时的兜底已编进二进制，
# 这份磁盘副本便于管理员直接查看/复制）
COPY server/internal/theme/embedded /app/themes-seed/default

ENV PICGO_WEB_LISTEN=0.0.0.0:8080 \
    PICGO_WEB_DATA_DIR=/data \
    PICGO_WEB_DB_DRIVER=sqlite \
    PICGO_WEB_AGENT_AUTOSTART=false \
    PICGO_WEB_AGENT_URL=http://127.0.0.1:36678 \
    PICGO_WEB_AGENT_CONFIG_PATH=/data/picgo/config.json \
    PICGO_WEB_AGENT_PORT=36678 \
    PICGO_WEB_AGENT_HOST=127.0.0.1 \
    TZ=Asia/Shanghai \
    GIN_MODE=release

VOLUME ["/data"]
EXPOSE 8080

# 健康检查：/healthz 不使用信封、字段小写，可直接解析
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1

# 说明：agent 与 Go 同容器。默认 `PICGO_WEB_AGENT_AUTOSTART=false`，
# 由 deploy/docker/entrypoint.sh 先拉起 agent 再启动 Go
# （自动拉起的进程管理尚未实现，见 docs/PLAN.md「未完成」）。
COPY deploy/docker/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

ENTRYPOINT ["/app/entrypoint.sh"]
