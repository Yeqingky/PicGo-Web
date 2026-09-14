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
dev: ## 启动全部开发进程（Go 会在自己内部拉起 agent，另起 Vite）
	@echo "→ 启动 server（内含 agent 子进程）+ web（Ctrl-C 全部退出）"
	@echo "  · Go 后端 :8080  · agent 子进程 127.0.0.1:36678  · 前端 5173"
	@echo "  ⚠️  不要把 agent 与 dev 同时跑：端口会冲突（单独调试 agent 用 make dev-no-agent）"
	@$(MAKE) -j2 server web

.PHONY: dev-no-agent
dev-no-agent: ## 三进程分开调试（agent 不由 Go 拉起）
	@echo "→ 同时启动 server / agent / web（agent 走 tsx watch，带热重载）"
	@$(MAKE) -j3 server-no-agent agent web

.PHONY: server
server: ## 启动 Go 后端（默认 :8080；会自动拉起 agent 子进程）
	@cd $(SERVER_DIR) && go run ./cmd/picgo-web

.PHONY: server-no-agent
server-no-agent: ## 启动 Go 后端，但不拉起 agent（配合 make agent 使用）
	@cd $(SERVER_DIR) && PICGO_WEB_AGENT_AUTOSTART=false go run ./cmd/picgo-web

.PHONY: agent
agent: ## 只启动 picgo-agent 侧车（tsx watch 热重载；需与 Go 共享同一令牌）
	@cd $(AGENT_DIR) && PICGO_AGENT_TOKEN="$${PICGO_WEB_AGENT_TOKEN:-$$(cat $(SERVER_DIR)/../data/agent-token.txt 2>/dev/null || true)}" pnpm dev

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
build-web: ## 构建前端 SPA（并同步到 internal/webfs/dist 供 go:embed）
	@cd $(WEB_DIR) && pnpm build
	@echo "→ 同步前端产物到 server/internal/webfs/dist（go:embed 是编译期行为，必须先同步再编译）"
	@rm -rf $(SERVER_DIR)/internal/webfs/dist/assets
	@find $(SERVER_DIR)/internal/webfs/dist -maxdepth 1 -type f ! -name '.gitkeep' -delete
	@cp -r $(WEB_DIR)/dist/. $(SERVER_DIR)/internal/webfs/dist/
	@echo "✓ $(SERVER_DIR)/internal/webfs/dist"

.PHONY: build-agent
build-agent: ## 构建 picgo-agent 产物
	@cd $(AGENT_DIR) && pnpm build

.PHONY: theme
theme: ## 导出默认主题到 themes/default/（源：server/internal/theme/embedded）
	@echo "→ 导出默认主题"
	@mkdir -p $(THEME_DIR)/default
	@cp -r $(SERVER_DIR)/internal/theme/embedded/. $(THEME_DIR)/default/
	@echo "✓ $(THEME_DIR)/default/（可直接放进 <dataDir>/themes/ 使用）"
	@echo "  提示：二进制已内嵌同一份，作为主题缺失时的兜底（D94），无需手动部署。"

.PHONY: vendor
vendor: ## 把打过补丁的 PicGo-Core 打成 tarball 到 deploy/vendor/（Docker 构建需要）
	@echo "→ 打包 PicGo-Core tarball"
	@mkdir -p $(ROOT)/deploy/vendor
	@rm -f $(ROOT)/deploy/vendor/picgo-*.tgz
	@cd "$(CORE_DIR)" && pnpm build && pnpm pack --pack-destination $(ROOT)/deploy/vendor
	@ls -lh $(ROOT)/deploy/vendor/

.PHONY: pack
pack: build theme vendor ## 产出可发布包（二进制 + 主题 + PicGo-Core tarball）
	@echo "✓ 发布包已就绪"

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
# 端到端验证（会真实起服务 + 建临时数据目录，不污染 ./data）
# ===========================================================================

.PHONY: e2e
e2e: e2e-lsky ## 跑全部端到端验证

.PHONY: e2e-lsky
e2e-lsky: ## 验证 Lsky v1 兼容层（9 端点 + 信封一致性，W9/D52）
	@echo "=== Lsky v1 兼容层端到端 ==="
	@$(ROOT)/scripts/e2e-lsky.sh

.PHONY: e2e-theme
e2e-theme: ## 验证主题系统与静态托管（分发算法/兜底/zip 安装，W10/D94-D99）
	@echo "=== 主题系统端到端 ==="
	@$(ROOT)/scripts/e2e-theme.sh

.PHONY: e2e-all
e2e-all: e2e-theme e2e-lsky ## 串行跑两个端到端脚本

# ===========================================================================
# 部署
# ===========================================================================

.PHONY: docker-build
docker-build: vendor ## 构建 Docker 镜像（先 vendor PicGo-Core tarball）
	@docker build -f deploy/docker/Dockerfile -t picgo-web:latest .

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
