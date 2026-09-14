# PicGo-Web 根 Makefile
#
# 目标命名与 README.md、「docs/PLAN.md」保持一致。
# 三端：server（Go）/ picgo-agent（Node 侧车）/ web（React 前端）

SHELL := /bin/bash
.DEFAULT_GOAL := help

# ---- 路径与变量 ----
ROOT        := $(CURDIR)
SERVER_DIR  := $(ROOT)/server
AGENT_DIR   := $(ROOT)/picgo-agent
WEB_DIR     := $(ROOT)/web
THEME_DIR   := $(ROOT)/themes
CORE_DIR    := $(ROOT)/../PicGo-Core

# 统一 Go 环境（纯 Go、免 CGO）
export CGO_ENABLED := 0
export GOFLAGS     := -mod=mod

BIN := $(SERVER_DIR)/picgo-web

.PHONY: help
help: ## 显示本帮助
	@echo "PicGo-Web 可用目标："
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

# ===========================================================================
# 依赖安装
# ===========================================================================

.PHONY: deps
deps: deps-core deps-agent deps-web ## 安装全部依赖（含 PicGo-Core）

.PHONY: deps-core
deps-core: ## 准备本地 PicGo-Core（分支 PicGo-Web 并构建 dist/）
	@if [ ! -d "$(CORE_DIR)" ]; then \
		echo "✗ 未找到 PicGo-Core：期望位于 $(CORE_DIR)"; \
		echo "  请先 clone 并切到 PicGo-Web 分支："; \
		echo "    git clone Github-me:YeqingKy/PicGo-Core $(CORE_DIR)"; \
		echo "    cd $(CORE_DIR) && git checkout PicGo-Web"; \
		exit 1; \
	fi
	@echo "→ 安装并构建 PicGo-Core（dist/ 被 gitignore，必须构建）"
	@cd "$(CORE_DIR)" && pnpm install && pnpm build

.PHONY: deps-server
deps-server: ## 拉取 Go 依赖
	@cd $(SERVER_DIR) && go mod download

.PHONY: deps-agent
deps-agent: ## 安装 picgo-agent 依赖
	@cd $(AGENT_DIR) && pnpm install

.PHONY: deps-web
deps-web: ## 安装前端依赖
	@cd $(WEB_DIR) && pnpm install

# ===========================================================================
# 开发
# ===========================================================================

.PHONY: dev
dev: ## 启动全部开发进程（Go + agent + Vite）
	@echo "→ 同时启动 server / agent / web（Ctrl-C 全部退出）"
	@$(MAKE) -j3 server agent web

.PHONY: server
server: ## 只启动 Go 后端（默认 :8080）
	@cd $(SERVER_DIR) && go run ./cmd/picgo-web

.PHONY: agent
agent: ## 只启动 picgo-agent 侧车（默认 127.0.0.1:36678）
	@cd $(AGENT_DIR) && pnpm dev

.PHONY: web
web: ## 只启动前端 Vite dev server（/api 代理到 :8080）
	@cd $(WEB_DIR) && pnpm dev

# ===========================================================================
# 构建
# ===========================================================================

.PHONY: build
build: build-web build-agent build-server ## 构建全部产物

.PHONY: build-server
build-server: ## 编译 Go 静态二进制（CGO_ENABLED=0）
	@echo "→ 构建 Go 二进制"
	@cd $(SERVER_DIR) && go build -trimpath -ldflags "-s -w" -o picgo-web ./cmd/picgo-web
	@echo "✓ $(BIN)"

.PHONY: build-web
build-web: ## 构建前端 SPA（内置进二进制）
	@cd $(WEB_DIR) && pnpm build

.PHONY: build-agent
build-agent: ## 构建 picgo-agent 产物
	@cd $(AGENT_DIR) && pnpm build

.PHONY: theme
theme: ## 构建默认主题并打包为 themes/default/（含内嵌归档）
	@echo "→ 构建默认主题"
	@cd $(WEB_DIR) && pnpm build:theme
	@mkdir -p $(THEME_DIR)
	@node $(ROOT)/scripts/pack-theme.mjs
	@echo "✓ $(THEME_DIR)/default/"

.PHONY: pack
pack: build theme ## 产出可发布包（二进制 + 主题 + PicGo-Core tarball）
	@echo "→ 打包 PicGo-Core tarball（供 agent 生产依赖）"
	@cd "$(CORE_DIR)" && pnpm build && pnpm pack --pack-destination $(ROOT)/deploy/vendor

# ===========================================================================
# 质量门禁
# ===========================================================================

.PHONY: check
check: check-core check-server check-agent check-web ## 串行跑三端全部检查（含 PicGo-Core）

.PHONY: check-core
check-core: ## PicGo-Core：lint + 单测
	@echo "=== PicGo-Core ==="
	@cd "$(CORE_DIR)" && pnpm lint && pnpm test

.PHONY: check-server
check-server: ## Go：vet + test + 静态编译
	@echo "=== server ==="
	@cd $(SERVER_DIR) && go vet ./... && go test ./... && CGO_ENABLED=0 go build -o /dev/null ./cmd/picgo-web

.PHONY: check-agent
check-agent: ## agent：lint + typecheck + test
	@echo "=== picgo-agent ==="
	@cd $(AGENT_DIR) && pnpm lint && pnpm typecheck && pnpm test

.PHONY: check-web
check-web: ## 前端：lint + typecheck + build
	@echo "=== web ==="
	@cd $(WEB_DIR) && pnpm lint && pnpm typecheck && pnpm build

.PHONY: fmt
fmt: ## 格式化 Go 代码
	@cd $(SERVER_DIR) && gofmt -s -w .

# ===========================================================================
# 部署
# ===========================================================================

.PHONY: docker-build
docker-build: ## 构建 Docker 镜像
	@docker compose build

.PHONY: docker-up
docker-up: ## 启动（SQLite）
	@docker compose up -d

.PHONY: docker-up-pgsql
docker-up-pgsql: ## 启动（PostgreSQL 覆盖）
	@docker compose -f docker-compose.yml -f docker-compose.pgsql.yml up -d

.PHONY: docker-down
docker-down: ## 停止
	@docker compose down

.PHONY: clean
clean: ## 清理构建产物
	@rm -f $(BIN)
	@rm -rf $(THEME_DIR) $(WEB_DIR)/dist $(AGENT_DIR)/dist
	@echo "✓ 已清理"
