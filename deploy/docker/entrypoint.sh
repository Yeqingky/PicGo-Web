#!/bin/sh
# PicGo-Web 容器入口。
#
# 职责：
#  1. 准备数据目录
#  2. 生成 / 读取 agent 共享令牌（Go 与 agent 必须一致）
#  3. 拉起 picgo-agent 子进程（后台）
#  4. 启动 Go 主进程（前台，接收信号）
#  5. 收到 TERM/INT 时优雅关停两者
#
# 为什么需要这个脚本：
#   `PICGO_WEB_AGENT_AUTOSTART` 的子进程管理尚未在 Go 侧实现（见 docs/PLAN.md），
#   因此镜像里由 entrypoint 负责拉起 agent。
set -eu

DATA_DIR="${PICGO_WEB_DATA_DIR:-/data}"
AGENT_DIR="/app/picgo-agent"
AGENT_TOKEN_FILE="${DATA_DIR}/agent-token.txt"

log() { echo "[entrypoint] $*"; }

# ---------------------------------------------------------------- 1. 数据目录
mkdir -p "${DATA_DIR}" "${DATA_DIR}/picgo"

# ---------------------------------------------------------------- 2. agent 令牌
#
# 优先级：环境变量 PICGO_WEB_AGENT_TOKEN > 数据目录里的文件 > 新生成
if [ -n "${PICGO_WEB_AGENT_TOKEN:-}" ]; then
    AGENT_TOKEN="${PICGO_WEB_AGENT_TOKEN}"
    log "使用环境变量提供的 agent 令牌"
elif [ -f "${AGENT_TOKEN_FILE}" ]; then
    AGENT_TOKEN=$(cat "${AGENT_TOKEN_FILE}")
    log "复用已有 agent 令牌：${AGENT_TOKEN_FILE}"
else
    # 用 openssl 而非 /dev/urandom + tr，避免 busybox 差异
    AGENT_TOKEN=$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')
    umask 077
    printf '%s\n' "${AGENT_TOKEN}" > "${AGENT_TOKEN_FILE}"
    log "已生成新的 agent 令牌：${AGENT_TOKEN_FILE}"
fi
export PICGO_WEB_AGENT_TOKEN="${AGENT_TOKEN}"

# ---------------------------------------------------------------- 3. 主题 seed
#
# 主题目录为空时，把镜像里那份默认主题种进去（D94：升级不覆盖用户主题）。
THEMES_DIR="${PICGO_WEB_THEMES_DIR:-${DATA_DIR}/themes}"
mkdir -p "${THEMES_DIR}"
if [ -z "$(ls -A "${THEMES_DIR}" 2>/dev/null)" ] && [ -d /app/themes-seed/default ]; then
    log "主题目录为空，种入默认主题"
    cp -r /app/themes-seed/default "${THEMES_DIR}/default"
fi

# ---------------------------------------------------------------- 4. agent
if [ "${PICGO_WEB_AGENT_MOCK:-false}" = "true" ]; then
    log "PICGO_WEB_AGENT_MOCK=true，跳过启动 agent"
    AGENT_PID=""
else
    if [ ! -d "${AGENT_DIR}/node_modules" ]; then
        log "警告：${AGENT_DIR}/node_modules 不存在，agent 无法启动"
        AGENT_PID=""
    else
        log "启动 picgo-agent（127.0.0.1:${PICGO_WEB_AGENT_PORT:-36678}）"
        (
            cd "${AGENT_DIR}"
            exec node dist/index.js
        ) &
        AGENT_PID=$!
        log "agent PID = ${AGENT_PID}"
    fi
fi

# ---------------------------------------------------------------- 5. 关停
shutdown() {
    log "收到退出信号，开始关停"
    if [ -n "${SERVER_PID:-}" ]; then
        kill -TERM "${SERVER_PID}" 2>/dev/null || true
        wait "${SERVER_PID}" 2>/dev/null || true
    fi
    if [ -n "${AGENT_PID}" ]; then
        kill -TERM "${AGENT_PID}" 2>/dev/null || true
        sleep 1
        kill -KILL "${AGENT_PID}" 2>/dev/null || true
    fi
    log "已停止"
    exit 0
}
trap shutdown TERM INT

# ---------------------------------------------------------------- 6. Go 主进程
log "启动 picgo-web（listen=${PICGO_WEB_LISTEN:-0.0.0.0:8080}）"
/app/picgo-web &
SERVER_PID=$!

# 若 agent 意外退出，记录但不连带杀掉主进程
if [ -n "${AGENT_PID}" ]; then
    wait -n "${SERVER_PID}" "${AGENT_PID}" 2>/dev/null || true
else
    wait "${SERVER_PID}" 2>/dev/null || true
fi

shutdown
