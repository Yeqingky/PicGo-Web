# PicGo-Web 统一入口
#
# ⚠️ 本项目**只提供 docker compose 部署**（D76）。
#    开发同样走 compose（docker-compose-dev.yml），不再提供宿主直跑的方式。
#
#   make dev          起开发环境（前端/后端/侧车三容器 + 热重载）
#   make up           起生产环境（SQLite）
#   make help         看全部命令

SHELL := /bin/bash
.DEFAULT_GOAL := help

ROOT      := $(CURDIR)
COMPOSE   := docker compose
DEV_FILE  := docker-compose-dev.yml
PROD_FILE := docker-compose.yml
PG_FILE   := docker-compose.pgsql.yml

# 开发环境用独立的数据目录，与生产的 ./data 隔离；
# 全部用宿主目录挂载（bind mount），不用存储卷
DATA_DEV  := $(ROOT)/data-dev
DEV_DIRS  := $(DATA_DEV)/picgo \
             $(DATA_DEV)/picgo-agent/node_modules \
             $(DATA_DEV)/web/node_modules \
             $(DATA_DEV)/go/mod \
             $(DATA_DEV)/go/build-cache \
             $(DATA_DEV)/corepack \
             $(DATA_DEV)/e2e/picgo-agent/node_modules \
             $(DATA_DEV)/e2e/web/node_modules

.PHONY: help
help: ## 显示本帮助
	@echo "PicGo-Web 可用命令（全部基于 docker compose）："
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

# ===========================================================================
# 开发（docker compose）
# ===========================================================================

.PHONY: dev
dev: ## 起开发环境（三容器 + 热重载；前台，Ctrl-C 停止）
	@mkdir -p $(DEV_DIRS)
	@echo "→ 启动开发环境（前端 :5173 / 后端 :8080 / 侧车 :36678）"
	@echo "  直接用基础镜像跑源码：首次启动会自动安装依赖，缓存走 ./data-dev/ 挂载目录"
	@echo "  管理员初始密码：docker compose -f $(DEV_FILE) logs server | grep password"
	@$(COMPOSE) -f $(DEV_FILE) up

.PHONY: dev-build
dev-build: ## 【已废弃】dev 不再构建镜像（基础镜像 + 启动时自动装依赖）
	@echo "✓ dev 环境不构建镜像：改依赖后直接 make dev-up 重启，容器启动时会自动 pnpm install"

.PHONY: dev-up
dev-up: ## 起开发环境（后台）
	@mkdir -p $(DEV_DIRS)
	@$(COMPOSE) -f $(DEV_FILE) up -d
	@echo "✓ 前端 http://localhost:5173  后端 http://localhost:8080"

.PHONY: dev-down
dev-down: ## 停开发环境（保留数据）
	@$(COMPOSE) -f $(DEV_FILE) down

.PHONY: dev-reset
dev-reset: ## 停开发环境并**删除数据**（data-dev 挂载目录；不可恢复）
	@echo "⚠️  将删除 $(DATA_DEV)（数据库/依赖/缓存/主题全部丢失）"
	@read -p "确认？输入 yes 继续：" ans; [ "$$ans" = "yes" ] || { echo "已取消"; exit 1; }
	@$(COMPOSE) -f $(DEV_FILE) down
	@rm -rf $(DATA_DEV)
	@echo "✓ 已重置"

.PHONY: dev-logs
dev-logs: ## 跟踪开发环境全部日志
	@$(COMPOSE) -f $(DEV_FILE) logs -f --tail=100

.PHONY: dev-logs-server
dev-logs-server: ## 只看后端日志（含管理员初始密码）
	@$(COMPOSE) -f $(DEV_FILE) logs -f --tail=100 server

.PHONY: dev-shell-server
dev-shell-server: ## 进入后端容器
	@$(COMPOSE) -f $(DEV_FILE) exec server sh

.PHONY: dev-shell-agent
dev-shell-agent: ## 进入侧车容器
	@$(COMPOSE) -f $(DEV_FILE) exec agent sh

.PHONY: dev-ps
dev-ps: ## 查看开发环境容器状态
	@$(COMPOSE) -f $(DEV_FILE) ps

# ===========================================================================
# 生产（docker compose）
# ===========================================================================

.PHONY: up
up: ## 起生产环境（SQLite；后台）
	@[ -f .env ] || { echo "✗ 缺少 .env（cp .env.example .env 后按需修改）"; exit 1; }
	@$(COMPOSE) -f $(PROD_FILE) up -d
	@echo "✓ 打开 http://localhost:8080"

.PHONY: up-pgsql
up-pgsql: ## 起生产环境（PostgreSQL 覆盖）
	@[ -f .env ] || { echo "✗ 缺少 .env"; exit 1; }
	@$(COMPOSE) -f $(PROD_FILE) -f $(PG_FILE) up -d

.PHONY: up-build
up-build: ## 从源码构建镜像并起生产环境
	@[ -f .env ] || { echo "✗ 缺少 .env"; exit 1; }
	@$(COMPOSE) -f $(PROD_FILE) up -d --build

.PHONY: down
down: ## 停生产环境（保留数据）
	@$(COMPOSE) -f $(PROD_FILE) down

.PHONY: logs
logs: ## 跟踪生产日志
	@$(COMPOSE) -f $(PROD_FILE) logs -f --tail=100

# ===========================================================================
# 质量门禁（在容器里跑，保证与部署环境一致）
# ===========================================================================

.PHONY: check
check: check-server check-agent check-web ## 三端全部检查

.PHONY: theme-sync
theme-sync: ## 从 PicGo-Web-Theme 仓库同步默认主题到内嵌兕底副本
	@bash scripts/sync-default-theme.sh

.PHONY: check-server
check-server: ## Go：vet + test + 静态编译
	@echo "=== server ==="
	@$(COMPOSE) -f $(DEV_FILE) run --rm --no-deps server sh -c \
		'apk add --no-cache git >/dev/null 2>&1; go vet ./... && go test ./... && CGO_ENABLED=0 go build -o /dev/null ./cmd/picgo-web'

.PHONY: check-agent
check-agent: ## 侧车：install + lint + typecheck + test
	@echo "=== picgo-agent ==="
	@$(COMPOSE) -f $(DEV_FILE) run --rm --no-deps agent sh -c \
		'corepack enable && pnpm install --frozen-lockfile=false && pnpm lint && pnpm typecheck && pnpm test'

.PHONY: check-web
check-web: ## 前端：install + lint + typecheck + build
	@echo "=== web ==="
	@$(COMPOSE) -f $(DEV_FILE) run --rm --no-deps web sh -c \
		'corepack enable && pnpm install --frozen-lockfile=false && pnpm lint && pnpm typecheck && pnpm build'

# ===========================================================================
# 端到端验证（在 server 容器内跑，用临时数据目录，不污染 data-dev）
# ===========================================================================

.PHONY: e2e
e2e: e2e-auth e2e-theme e2e-lsky ## 跑全部端到端验证

.PHONY: e2e-auth
e2e-auth: ## 验证鉴权与用户（登录/刷新/改密/API Token/限流/首启引导）
	@echo "=== 鉴权与用户冒烟 ==="
	@$(COMPOSE) -f $(DEV_FILE) --profile e2e run --rm --no-deps e2e ./scripts/smoke-auth.sh

.PHONY: e2e-theme
e2e-theme: ## 验证主题系统（分发算法/兜底/zip 九条校验）
	@echo "=== 主题系统端到端 ==="
	@$(COMPOSE) -f $(DEV_FILE) --profile e2e run --rm --no-deps e2e ./scripts/e2e-theme.sh

.PHONY: e2e-lsky
e2e-lsky: ## 验证 Lsky v1 兼容层（9 端点 + 信封一致性）
	@echo "=== Lsky v1 兼容层端到端 ==="
	@$(COMPOSE) -f $(DEV_FILE) --profile e2e run --rm --no-deps e2e ./scripts/e2e-lsky.sh
