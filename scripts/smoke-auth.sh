#!/usr/bin/env bash
#
# W3 鉴权与用户的端到端冒烟测试。
#
# 用法：
#   bash scripts/smoke-auth.sh
#
# 行为：
#   1. 在临时目录启动一个全新的服务实例（独立端口、独立数据库）
#   2. 逐项验证 API.md §1 / §2 / §11 的关键行为
#   3. 结束（无论成败）都会杀掉服务进程并清理临时目录
#
# 依赖：curl、python3。脚本以「在容器/CI 里也能跑」为目标，尽量不做环境假设。

set -uo pipefail

# 端口：默认让内核分配一个空闲端口，避开开发机上已被占用的端口
# （可用 SMOKE_PORT=18xxx 显式指定）
pick_port() {
  python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}

if [[ -n "${SMOKE_PORT:-}" ]]; then
  PORT="${SMOKE_PORT}"
else
  PORT="$(pick_port)"
fi

BASE="http://127.0.0.1:${PORT}"
WORK="$(mktemp -d)"
COOKIES="${WORK}/cookies.txt"
SERVER_PID=""

PASS=0
FAIL=0

cleanup() {
  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" 2>/dev/null || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
  rm -rf "${WORK}"
}
trap cleanup EXIT

ok()   { PASS=$((PASS+1)); printf '  \033[32m✓\033[0m %s\n' "$1"; }
bad()  { FAIL=$((FAIL+1)); printf '  \033[31m✗\033[0m %s\n' "$1"; }
info() { printf '\n\033[36m▸ %s\033[0m\n' "$1"; }

# assert_eq <描述> <期望> <实际>
assert_eq() {
  if [[ "$2" == "$3" ]]; then ok "$1"; else bad "$1（期望 [$2] 实际 [$3]）"; fi
}

# json <json 字符串> <点路径>：取出字段（仅支持顶层字段）
json() {
  python3 -c 'import json,sys
try:
    d = json.loads(sys.argv[1])
except Exception:
    print(""); sys.exit(0)
for k in sys.argv[2].split("."):
    if isinstance(d, dict) and k in d: d = d[k]
    else: print(""); sys.exit(0)
print(d if d is not None else "")' "$1" "$2"
}

# httpcode <curl 参数...>：只输出状态码
httpcode() { curl -s -o /dev/null -w '%{http_code}' "$@"; }

# body <curl 参数...>：输出响应体
body() { curl -s "$@"; }

# ============================================================================
info "0. 构建并启动服务（端口 ${PORT}）"
# ============================================================================
cd "$(dirname "$0")/.." || exit 1
ROOT="$(pwd)"
BIN="${WORK}/picgo-web"

# Go 模块根在 server/ 下（不是仓库根）
if ! ( cd "${ROOT}/server" && CGO_ENABLED=0 go build -o "${BIN}" ./cmd/picgo-web ) 2>"${WORK}/build.log"; then
  echo "构建失败："; cat "${WORK}/build.log"; exit 1
fi
ok "go build 成功（CGO_ENABLED=0）"

mkdir -p "${WORK}/data"
cat > "${WORK}/env" <<EOF
PICGO_WEB_LISTEN=127.0.0.1:${PORT}
PICGO_WEB_DATA_DIR=${WORK}/data
PICGO_WEB_DB_DRIVER=sqlite
PICGO_WEB_LOG_LEVEL=warn
PICGO_WEB_DEV=false
PICGO_WEB_AGENT_AUTOSTART=false
EOF

( cd "${WORK}" && set -a && . "${WORK}/env" && set +a && exec "${BIN}" ) >"${WORK}/server.log" 2>&1 &
SERVER_PID=$!

for _ in $(seq 1 50); do
  if curl -sf "${BASE}/healthz" >/dev/null 2>&1; then break; fi
  sleep 0.2
done

if ! curl -sf "${BASE}/healthz" >/dev/null 2>&1; then
  echo "服务未能在 10 秒内就绪："; cat "${WORK}/server.log"; exit 1
fi
ok "服务已就绪（/healthz）"

# ============================================================================
info "1. 首启引导：初始管理员与密码文件（D32）"
# ============================================================================
PWFILE="${WORK}/data/initial-admin-password.txt"
assert_eq "密码文件已创建" "yes" "$([[ -f "${PWFILE}" ]] && echo yes || echo no)"

PERM="$(stat -c '%a' "${PWFILE}" 2>/dev/null || stat -f '%A' "${PWFILE}" 2>/dev/null)"
assert_eq "密码文件权限为 0600" "600" "${PERM}"

ADMIN_PW="$(tr -d '\n' < "${PWFILE}")"
assert_eq "密码长度 16" "16" "${#ADMIN_PW}"

if grep -q "${ADMIN_PW}" "${WORK}/server.log"; then
  ok "日志中也打印了初始密码（WARN 级）"
else
  bad "日志中未见初始密码"
fi

# ============================================================================
info "2. 登录：初始密码（应成功，且 MustChangePassword=true）"
# ============================================================================
RESP="$(body -X POST "${BASE}/api/web/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -c "${COOKIES}" \
  -d "{\"Email\":\"admin@localhost\",\"Password\":\"${ADMIN_PW}\"}")"
assert_eq "登录返回 Code=0" "0" "$(json "${RESP}" Code)"
ACCESS="$(json "${RESP}" Data.AccessToken)"
[[ -n "${ACCESS}" ]] && ok "返回了 AccessToken" || bad "未返回 AccessToken"
assert_eq "ExpiresIn=900（默认 15 分钟）" "900" "$(json "${RESP}" Data.ExpiresIn)"
assert_eq "MustChangePassword=true" "True" "$(json "${RESP}" Data.User.MustChangePassword)"
assert_eq "角色为 admin" "admin" "$(json "${RESP}" Data.User.Role)"

if grep -q 'pcw_at' "${COOKIES}" && grep -q 'pcw_rt' "${COOKIES}"; then
  ok "下发了 pcw_at / pcw_rt Cookie"
else
  bad "未下发预期 Cookie"
fi

# ============================================================================
info "3. /auth/me：未登录与已登录"
# ============================================================================
CODE="$(httpcode "${BASE}/api/web/v1/auth/me")"
assert_eq "不带凭据访问 /auth/me → HTTP 401" "401" "${CODE}"
RESP="$(body "${BASE}/api/web/v1/auth/me")"
assert_eq "错误码为 40102" "40102" "$(json "${RESP}" Code)"

CODE="$(httpcode -H "Authorization: Bearer ${ACCESS}" "${BASE}/api/web/v1/auth/me")"
assert_eq "带 Bearer 访问 /auth/me → HTTP 200" "200" "${CODE}"

# ============================================================================
info "4. 强制改密拦截（D32）：必须改密的账号访问业务接口应 40301"
# ============================================================================
CODE="$(httpcode -H "Authorization: Bearer ${ACCESS}" "${BASE}/api/web/v1/users")"
assert_eq "访问 /users → HTTP 403" "403" "${CODE}"
RESP="$(body -H "Authorization: Bearer ${ACCESS}" "${BASE}/api/web/v1/users")"
assert_eq "错误码为 40301" "40301" "$(json "${RESP}" Code)"
assert_eq "提示为「请先修改密码」" "请先修改密码" "$(json "${RESP}" Message)"

# ============================================================================
info "5. 登录限流（D30）：窗口内失败达阈值 → 42901"
# ============================================================================
# 用不存在的邮箱测试，避免把真实管理员锁住（限流按 Email + ClientIP 统计）
BOGUS='ghost@example.com'
for i in 1 2 3 4 5; do
  body -X POST "${BASE}/api/web/v1/auth/login" -H 'Content-Type: application/json' \
    -d "{\"Email\":\"${BOGUS}\",\"Password\":\"wrong-password\"}" >/dev/null
done
RESP="$(body -X POST "${BASE}/api/web/v1/auth/login" -H 'Content-Type: application/json' \
  -d "{\"Email\":\"${BOGUS}\",\"Password\":\"wrong-password\"}")"
assert_eq "第 6 次失败 → 错误码 42901" "42901" "$(json "${RESP}" Code)"
CODE="$(httpcode -X POST "${BASE}/api/web/v1/auth/login" -H 'Content-Type: application/json' \
  -d "{\"Email\":\"${BOGUS}\",\"Password\":\"wrong-password\"}")"
assert_eq "HTTP 状态码为 429" "429" "${CODE}"

# 失败的提示不能泄露账号是否存在
RESP="$(body -X POST "${BASE}/api/web/v1/auth/login" -H 'Content-Type: application/json' \
  -d '{"Email":"admin@localhost","Password":"definitely-wrong"}')"
assert_eq "密码错误 → 40101" "40101" "$(json "${RESP}" Code)"

# ============================================================================
info "6. 修改密码（PATCH /auth/password）"
# ============================================================================
NEW_PW='SmokeTest-Password-1'
CODE="$(httpcode -X PATCH "${BASE}/api/web/v1/auth/password" \
  -H "Authorization: Bearer ${ACCESS}" -H 'Content-Type: application/json' \
  -d "{\"OldPassword\":\"${ADMIN_PW}\",\"NewPassword\":\"${NEW_PW}\"}")"
assert_eq "改密 → HTTP 200" "200" "${CODE}"

RESP="$(body -X POST "${BASE}/api/web/v1/auth/login" -H 'Content-Type: application/json' \
  -d "{\"Email\":\"admin@localhost\",\"Password\":\"${ADMIN_PW}\"}")"
assert_eq "旧密码登录 → 40101" "40101" "$(json "${RESP}" Code)"

RESP="$(body -X POST "${BASE}/api/web/v1/auth/login" -H 'Content-Type: application/json' \
  -c "${COOKIES}" \
  -d "{\"Email\":\"admin@localhost\",\"Password\":\"${NEW_PW}\"}")"
assert_eq "新密码登录 → Code=0" "0" "$(json "${RESP}" Code)"
assert_eq "MustChangePassword 已置 false" "False" "$(json "${RESP}" Data.User.MustChangePassword)"
ACCESS="$(json "${RESP}" Data.AccessToken)"

CODE="$(httpcode -H "Authorization: Bearer ${ACCESS}" "${BASE}/api/web/v1/users")"
assert_eq "改密后访问 /users → HTTP 200（拦截已解除）" "200" "${CODE}"

# ============================================================================
info "7. refresh 轮换（D30）：旧 token 立即失效"
# ============================================================================
REFRESH_OLD="$(awk '$6=="pcw_rt" {print $7}' "${COOKIES}" | tail -1)"
[[ -n "${REFRESH_OLD}" ]] && ok "拿到 pcw_rt" || bad "cookie jar 里没有 pcw_rt"

RESP="$(body -X POST "${BASE}/api/web/v1/auth/refresh" -b "${COOKIES}" -c "${WORK}/cookies2.txt")"
assert_eq "刷新成功 → Code=0" "0" "$(json "${RESP}" Code)"
REFRESH_NEW="$(awk '$6=="pcw_rt" {print $7}' "${WORK}/cookies2.txt" | tail -1)"
if [[ -n "${REFRESH_NEW}" && "${REFRESH_NEW}" != "${REFRESH_OLD}" ]]; then
  ok "refresh token 已轮换（新旧不同）"
else
  bad "refresh token 未轮换"
fi

# 用已被轮换掉的旧 token 再刷一次 → 应失效
RESP="$(body -X POST "${BASE}/api/web/v1/auth/refresh" \
  -H 'Content-Type: application/json' -d "{\"RefreshToken\":\"${REFRESH_OLD}\"}")"
assert_eq "复用旧 refresh token → 40103" "40103" "$(json "${RESP}" Code)"

# ============================================================================
info "8. API Token（D31）：明文只出现一次，可鉴权，可吊销"
# ============================================================================
RESP="$(body -X POST "${BASE}/api/web/v1/settings/api-tokens" \
  -H "Authorization: Bearer ${ACCESS}" -H 'Content-Type: application/json' \
  -d '{"Name":"CI 冒烟","ExpiresInDays":0}')"
API_TOKEN="$(json "${RESP}" Data.Token)"
API_UID="$(json "${RESP}" Data.UID)"
assert_eq "创建令牌 → Code=0" "0" "$(json "${RESP}" Code)"
if [[ "${API_TOKEN}" == pcw_* ]]; then ok "返回了 pcw_ 前缀明文的令牌"; else bad "令牌格式不对：${API_TOKEN}"; fi

RESP="$(body -H "Authorization: Bearer ${API_TOKEN}" "${BASE}/api/web/v1/auth/me")"
assert_eq "用 API token 鉴权 → Code=0" "0" "$(json "${RESP}" Code)"

RESP="$(body -H "Authorization: Bearer ${ACCESS}" "${BASE}/api/web/v1/settings/api-tokens")"
assert_eq "列表返回 1 条" "1" "$(python3 -c 'import json,sys;print(len(json.loads(sys.argv[1])["Data"]["Items"]))' "${RESP}")"
if grep -q "${API_TOKEN}" <<<"${RESP}"; then bad "列表里泄露了令牌明文"; else ok "列表不含明文（只有 Prefix）"; fi

CODE="$(httpcode -X DELETE -H "Authorization: Bearer ${ACCESS}" "${BASE}/api/web/v1/settings/api-tokens/${API_UID}")"
assert_eq "吊销令牌 → HTTP 200" "200" "${CODE}"
RESP="$(body -H "Authorization: Bearer ${API_TOKEN}" "${BASE}/api/web/v1/auth/me")"
assert_eq "吊销后该令牌不可用 → 40102/40103" "yes" \
  "$([[ "$(json "${RESP}" Code)" == "40102" || "$(json "${RESP}" Code)" == "40103" ]] && echo yes || echo no)"

# ============================================================================
info "9. 用户管理（admin）：创建 / 列表 / 配额 / 权限隔离"
# ============================================================================
RESP="$(body -X POST "${BASE}/api/web/v1/users" \
  -H "Authorization: Bearer ${ACCESS}" -H 'Content-Type: application/json' \
  -d '{"Email":"smoke-user@example.com","Password":"UserPass-123456","Nickname":"冒烟用户"}')"
assert_eq "创建普通用户 → Code=0" "0" "$(json "${RESP}" Code)"
USER_UID="$(json "${RESP}" Data.UID)"
assert_eq "默认配额为 5 GiB" "5368709120" "$(json "${RESP}" Data.CapacityBytes)"
assert_eq "默认角色为 user" "user" "$(json "${RESP}" Data.Role)"

RESP="$(body -X POST "${BASE}/api/web/v1/users" \
  -H "Authorization: Bearer ${ACCESS}" -H 'Content-Type: application/json' \
  -d '{"Email":"smoke-user@example.com","Password":"UserPass-123456"}')"
assert_eq "重复邮箱 → 40901" "40901" "$(json "${RESP}" Code)"

RESP="$(body "${BASE}/api/web/v1/users?Page=1&PageSize=10" -H "Authorization: Bearer ${ACCESS}")"
assert_eq "用户列表 Total=2" "2" "$(json "${RESP}" Data.Total)"

RESP="$(body -X PATCH "${BASE}/api/web/v1/users/${USER_UID}" \
  -H "Authorization: Bearer ${ACCESS}" -H 'Content-Type: application/json' \
  -d '{"CapacityBytes":1073741824}')"
assert_eq "调整配额 → Code=0" "0" "$(json "${RESP}" Code)"
assert_eq "配额已更新为 1 GiB" "1073741824" "$(json "${RESP}" Data.CapacityBytes)"

# 普通用户登录后访问管理接口应 40301
RESP="$(body -X POST "${BASE}/api/web/v1/auth/login" -H 'Content-Type: application/json' \
  -d '{"Email":"smoke-user@example.com","Password":"UserPass-123456"}')"
assert_eq "普通用户登录 → Code=0" "0" "$(json "${RESP}" Code)"
USER_ACCESS="$(json "${RESP}" Data.AccessToken)"
CODE="$(httpcode -H "Authorization: Bearer ${USER_ACCESS}" "${BASE}/api/web/v1/users")"
assert_eq "普通用户访问 /users → HTTP 403" "403" "${CODE}"

# ============================================================================
info "10. 管理员保护：不可删自己 / 不可删最后一个 admin"
# ============================================================================
RESP="$(body "${BASE}/api/web/v1/auth/me" -H "Authorization: Bearer ${ACCESS}")"
ADMIN_UID="$(json "${RESP}" Data.UID)"

RESP="$(body -X POST "${BASE}/api/web/v1/users/${ADMIN_UID}/reset-password" \
  -H "Authorization: Bearer ${ACCESS}")"
assert_eq "重置密码 → Code=0" "0" "$(json "${RESP}" Code)"
assert_eq "重置后强制改密" "True" "$(json "${RESP}" Data.MustChangePassword)"

# 用重置后的密码登录 admin（会再次被强制改密门禁挡住管理接口）
RESET_PW="$(json "${RESP}" Data.NewPassword)"
RESP="$(body -X POST "${BASE}/api/web/v1/auth/login" -H 'Content-Type: application/json' \
  -d "{\"Email\":\"admin@localhost\",\"Password\":\"${RESET_PW}\"}")"
assert_eq "用重置密码登录 → Code=0" "0" "$(json "${RESP}" Code)"
ADMIN_ACCESS2="$(json "${RESP}" Data.AccessToken)"
CODE="$(httpcode -X DELETE -H "Authorization: Bearer ${ADMIN_ACCESS2}" "${BASE}/api/web/v1/users/${ADMIN_UID}")"
assert_eq "删除自己 → HTTP 403" "403" "${CODE}"

# ============================================================================
info "11. OAuth：未配置时 providers 为空数组"
# ============================================================================
RESP="$(body "${BASE}/api/web/v1/auth/oauth/providers")"
assert_eq "providers → Code=0" "0" "$(json "${RESP}" Code)"
assert_eq "未配置 github → 空列表" "0" \
  "$(python3 -c 'import json,sys;print(len(json.loads(sys.argv[1])["Data"]["Providers"]))' "${RESP}")"
CODE="$(httpcode "${BASE}/api/web/v1/auth/oauth/github/start")"
assert_eq "未启用时 start → 404" "404" "${CODE}"

# ============================================================================
info "12. 统一响应与路由约定（D80/D81）"
# ============================================================================
RESP="$(body "${BASE}/api/web/v1/system/info")"
assert_eq "system/info → Code=0" "0" "$(json "${RESP}" Code)"
assert_eq "字段为大写 SchemaVersion（D81）" "1" "$(json "${RESP}" Data.SchemaVersion)"

CODE="$(httpcode "${BASE}/api/web/v1/nonexistent")"
assert_eq "不存在的内部接口 → HTTP 404" "404" "${CODE}"
RESP="$(body "${BASE}/api/web/v1/nonexistent")"
assert_eq "错误码为 40401" "40401" "$(json "${RESP}" Code)"

RESP="$(body "${BASE}/healthz")"
assert_eq "healthz 无信封（字段小写）" "ok" "$(json "${RESP}" status)"

CODE="$(httpcode "${BASE}/api/v1/upload")"
assert_eq "Lsky 保留集不被内部 API 占用（本阶段未注册 → 404）" "404" "${CODE}"

# ============================================================================
printf '\n\033[1m结果：\033[0m \033[32m%d 通过\033[0m，\033[31m%d 失败\033[0m\n' "${PASS}" "${FAIL}"
if [[ "${FAIL}" -gt 0 ]]; then
  echo
  echo "服务日志（末尾 40 行）："
  tail -40 "${WORK}/server.log"
  exit 1
fi
echo "全部通过 ✓"
