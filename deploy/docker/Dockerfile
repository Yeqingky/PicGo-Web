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

# picgo-core 从 npm 取打过补丁的版本：`@yeqingky/picgo-core`（含按次指定图床等补丁，
# 见 PicGo-Core 仓库的 FORK-NOTES.md）。因此镜像里**不需要**本地 PicGo-Core 源码，
# 也不需要预先 `make vendor` 打 tarball。
COPY picgo-agent/package.json picgo-agent/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

COPY picgo-agent/ ./
RUN pnpm build

# 只保留生产依赖，缩小体积
RUN pnpm prune --prod

# 构建期断言：确认 picgo-core 真的装进来了，且**带我们的补丁**
# （否则容器起得来但上传会静默用错图床 —— 这类问题在运行时极难排查）
RUN node -e "\
      const p=require('@yeqingky/picgo-core/package.json'); \
      const fs=require('fs'); \
      const src=fs.readFileSync(require.resolve('@yeqingky/picgo-core'),'utf8'); \
      if(!src.includes('contextData')) throw new Error('picgo-core 缺少补丁（contextData）'); \
      console.log('✓ picgo-core', p.version, '（补丁已就位）'); \
    "


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

# agent（含其 node_modules；picgo-core 从 npm 装进 node_modules）
COPY --from=agent-builder /build/agent/dist /app/picgo-agent/dist
COPY --from=agent-builder /build/agent/node_modules /app/picgo-agent/node_modules
COPY --from=agent-builder /build/agent/package.json /app/picgo-agent/package.json

# ENV 说明：
#   PICGO_WEB_AGENT_URL   Go 的 supervisor 依据它推导 agent 的监听地址并注入子进程
#   PICGO_WEB_AGENT_AUTOSTART  保持默认 true —— **由 Go 拉起并管理 agent 子进程**
#     （令牌生成、健康探测、退避重启、优雅关停都在 Go 里，见 internal/agent/lifecycle.go）
ENV PICGO_WEB_LISTEN=0.0.0.0:8080 \
    PICGO_WEB_DATA_DIR=/data \
    PICGO_WEB_DB_DRIVER=sqlite \
    PICGO_WEB_AGENT_AUTOSTART=true \
    PICGO_WEB_AGENT_URL=http://127.0.0.1:36678 \
    TZ=Asia/Shanghai \
    GIN_MODE=release

VOLUME ["/data"]
EXPOSE 8080

# 健康检查：/healthz 不使用信封、字段小写，可直接解析
HEALTHCHECK --interval=30s --timeout=5s --start-period=25s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1

# 无入口脚本。
#
# 为什么不需要：所有初始化工作都在 Go 里完成，且都有测试覆盖 ——
#   · 数据目录创建        cfg.EnsureDirs()
#   · 主题 seed           启动时自动种入（internal/theme/store.go 的 Seed）
#   · agent 令牌          解析/生成/落盘 0600（internal/agent/lifecycle.go）
#   · 拉起 agent 子进程   由 supervisor 完成（含 /app/picgo-agent/dist/index.js 探测）
#   · 健康探测 + 退避重启  同上
#   · 优雅关停            收到 TERM 后先停队列再关 agent
#
# 曾经用 entrypoint.sh 做这些，但它必须手工把 Go 的 `PICGO_WEB_AGENT_*`
# 映射成 agent 的 `PICGO_AGENT_*`（**两个前缀不同名**）—— 漏掉就会让 agent
# 把配置写进镜像内部而不是数据卷，重启即丢且极难排查。交给 Go 后这类问题不存在。
CMD ["/app/picgo-web"]
