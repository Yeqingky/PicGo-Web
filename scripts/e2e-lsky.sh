#!/usr/bin/env bash
#
# Lsky v1 兼容层端到端验证（W9 / D52）
#
# 用途：用**真实启动的服务**验证 Lsky 契约的 9 个端点，
# 以及「信封一致性」这条最容易出错、又最难排查的约束。
#
# 用法：
#   ./scripts/e2e-lsky.sh              # 自动构建并从零起服务
#   KEEP=1 ./scripts/e2e-lsky.sh       # 结束后保留临时目录以便排查
#
# 依赖：curl、python3、node
#
# 退出码：0 全部通过；非 0 表示有失败项（会打印明细）。

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVER_DIR="$ROOT/server"
AGENT_DIR="$ROOT/picgo-agent"
WORK="${WORK:-$(mktemp -d /tmp/picgo-web-lsky-e2e.XXXXXX)}"

# 与 Go 默认端口一致；若被占用会启动失败并提示
LISTEN_PORT="${LISTEN_PORT:-18080}"
AGENT_PORT="${AGENT_PORT:-36679}"
MOCK_BED_PORT="${MOCK_BED_PORT:-39003}"

GO_API="http://127.0.0.1:${LISTEN_PORT}/api/web/v1"
LSKY_API="http://127.0.0.1:${LISTEN_PORT}/api/v1"

ADMIN_EMAIL="admin@localhost"
ADMIN_PASS="Passw0rd!2026"

PASS=0
FAIL=0
FAILED_CASES=()

# ---------------------------------------------------------------------------
# 输出助手
# ---------------------------------------------------------------------------
c_ok()   { printf '\033[32m%s\033[0m' "$1"; }
c_bad()  { printf '\033[31m%s\033[0m' "$1"; }
c_dim()  { printf '\033[2m%s\033[0m' "$1"; }
c_head() { printf '\n\033[1;36m%s\033[0m\n' "$1"; }

ok()   { PASS=$((PASS+1)); printf '  %s %s\n' "$(c_ok '✓')" "$1"; }
bad()  { FAIL=$((FAIL+1)); FAILED_CASES+=("$1"); printf '  %s %s\n' "$(c_bad '✗')" "$1"; [ $# -gt 1 ] && printf '      %s\n' "$(c_dim "$2")"; }

PIDS=()

cleanup() {
    # 只杀我们自己起的进程（按 PID，不用 pkill，避免误伤）
    for pid in "${PIDS[@]}"; do
        [ -n "${pid:-}" ] && kill -9 "$pid" 2>/dev/null || true
    done
    if [ "${KEEP:-0}" != "1" ]; then
        rm -rf "$WORK"
    else
        printf '\n%s\n' "$(c_dim "临时目录保留：$WORK")"
    fi
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# 0. 准备
# ---------------------------------------------------------------------------
c_head "0. 准备"

if ! command -v curl >/dev/null; then echo "缺少 curl"; exit 2; fi
if ! command -v python3 >/dev/null; then echo "缺少 python3"; exit 2; fi

printf '  %s 构建 Go 二进制\n' "$(c_dim '·')"
if ! (cd "$SERVER_DIR" && CGO_ENABLED=0 go build -o picgo-web ./cmd/picgo-web 2>&1 | tail -5); then
    echo "  构建失败"; exit 2
fi
if [ ! -x "$SERVER_DIR/picgo-web" ]; then echo "  二进制不存在"; exit 2; fi
printf '  %s 构建 picgo-agent\n' "$(c_dim '·')"
if ! (cd "$AGENT_DIR" && pnpm build >/dev/null 2>&1); then
    echo "  agent 构建失败（先跑一次 make build-agent）"; exit 2
fi

# mock 图床（不依赖任何外部服务，保证脚本可离线复现）
mkdir -p "$WORK/data/picgo/node_modules/picgo-plugin-mockupload"
cat > "$WORK/mock-bed.cjs" <<'EOF'
const http = require('http')
http.createServer((req, res) => {
  let chunks = []
  req.on('data', c => chunks.push(c))
  req.on('end', () => {
    let j = {}
    try { j = JSON.parse(Buffer.concat(chunks).toString()) } catch (e) {}
    res.setHeader('content-type', 'application/json')
    res.end(JSON.stringify({ ok: true, url: 'https://mock.cdn/' + (j.bucket || 'b') + '/' + (j.fileName || 'x') }))
  })
}).listen(Number(process.env.MOCK_BED_PORT), '127.0.0.1')
EOF

# mock 上传器插件（装进 agent 的 baseDir）
cat > "$WORK/data/picgo/node_modules/picgo-plugin-mockupload/package.json" <<'EOF'
{ "name": "picgo-plugin-mockupload", "version": "1.0.0", "main": "index.js",
  "description": "端到端验证用的本地 mock 图床", "author": "e2e" }
EOF
cat > "$WORK/data/picgo/node_modules/picgo-plugin-mockupload/index.js" <<'EOF'
module.exports = (ctx) => ({
  register() {
    ctx.helper.uploader.register('mockupload', {
      name: 'Mock Uploader',
      config: () => [
        { name: 'bucket', type: 'input', required: true, default: 'default', alias: 'Bucket' },
        { name: 'path', type: 'input', required: false, default: 'img/', alias: 'Path' }
      ],
      handle: async (c) => {
        const cfg = c.getConfig('picBed.mockupload') || {}
        for (const img of c.output) {
          const body = JSON.stringify({
            fileName: img.fileName, bucket: cfg.bucket,
            base64: (img.buffer || Buffer.from(img.base64Image || '', 'base64')).toString('base64')
          })
          const res = await c.request({
            method: 'POST', url: 'http://127.0.0.1:' + process.env.MOCK_BED_PORT + '/upload',
            headers: { 'content-type': 'application/json' }, body
          })
          const parsed = typeof res === 'string' ? JSON.parse(res) : res
          img.imgUrl = parsed.url
          delete img.base64Image; delete img.buffer
        }
        return c
      }
    })
  },
  uploader: 'mockupload'
})
EOF
cat > "$WORK/data/picgo/package.json" <<'EOF'
{ "name": "picgo-plugins", "version": "1.0.0",
  "dependencies": { "picgo-plugin-mockupload": "^1.0.0" } }
EOF

cat > "$WORK/.env" <<EOF
PICGO_WEB_LISTEN=127.0.0.1:${LISTEN_PORT}
PICGO_WEB_DATA_DIR=./data
PICGO_WEB_DB_DRIVER=sqlite
PICGO_WEB_LOG_LEVEL=info
PICGO_WEB_AGENT_AUTOSTART=true
EOF

MOCK_BED_PORT="$MOCK_BED_PORT" setsid nohup node "$WORK/mock-bed.cjs" >"$WORK/mock-bed.log" 2>&1 < /dev/null &
PIDS+=($!)
sleep 1

# ---------------------------------------------------------------------------
# 1. 起服务
# ---------------------------------------------------------------------------
c_head "1. 启动服务"

if command -v ss >/dev/null && ss -tln 2>/dev/null | grep -q ":${LISTEN_PORT} "; then
    echo "  端口 ${LISTEN_PORT} 已被占用，请设 LISTEN_PORT=<其他端口>"; exit 2
fi

(
    cd "$WORK"
    # Go 从 PICGO_WEB_AGENT_URL 推导 agent 的监听 host/port（见 agent.childEnv）
    PICGO_WEB_AGENT_URL="http://127.0.0.1:${AGENT_PORT}" \
    MOCK_BED_PORT="$MOCK_BED_PORT" \
        setsid nohup "$SERVER_DIR/picgo-web" >"$WORK/server.log" 2>&1 < /dev/null &
    echo $! > "$WORK/server.pid"
)
PIDS+=("$(cat "$WORK/server.pid")")

# 等服务就绪（最多 40 秒）
READY=0
for _ in $(seq 1 40); do
    sleep 1
    if curl -sS --max-time 2 "http://127.0.0.1:${LISTEN_PORT}/healthz" >/dev/null 2>&1; then
        READY=1; break
    fi
done
if [ "$READY" != "1" ]; then
    echo "  服务未在 40 秒内就绪；日志："
    tail -20 "$WORK/server.log"
    exit 2
fi
ok "服务已启动（:${LISTEN_PORT}）"

# 确认 agent 自动拉起
if grep -q '"msg":"picgo-agent 已就绪"' "$WORK/server.log" 2>/dev/null; then
    ok "agent 已由 Go 自动拉起并在期限内就绪"
else
    bad "agent 未在启动阶段就绪" "$(grep -c 'agent' "$WORK/server.log" 2>/dev/null) 条 agent 相关日志"
fi

# 契约自检日志
if grep -q 'Lsky 契约自检通过' "$WORK/server.log" 2>/dev/null; then
    ok "启动时 Lsky 契约自检通过（9 条端点齐全）"
else
    bad "未看到 Lsky 契约自检通过日志"
fi

# ---------------------------------------------------------------------------
# 2. 初始化（内部 API：改密 + 建存储）
# ---------------------------------------------------------------------------
c_head "2. 初始化（内部 API）"

ADMIN_INIT_PASS="$(cat "$WORK/data/initial-admin-password.txt" 2>/dev/null)"
if [ -z "$ADMIN_INIT_PASS" ]; then
    bad "未找到初始管理员密码文件"; exit 2
fi
ok "初始管理员密码已生成（文件权限 $(stat -c '%a' "$WORK/data/initial-admin-password.txt" 2>/dev/null || echo '?'))"

login_internal() {
    curl -sS --max-time 15 -H 'content-type: application/json' \
        -X POST "$GO_API/auth/login" \
        -d "{\"Email\":\"$ADMIN_EMAIL\",\"Password\":\"$1\"}"
}

AT="$(login_internal "$ADMIN_INIT_PASS" | python3 -c 'import sys,json;print(json.load(sys.stdin)["Data"]["AccessToken"])' 2>/dev/null)"
if [ -n "$AT" ]; then
    ok "内部登录成功（初始密码）"
else
    bad "内部登录失败"; exit 2
fi

curl -sS --max-time 15 -H "Authorization: Bearer $AT" -H 'content-type: application/json' \
    -X PATCH "$GO_API/auth/password" \
    -d "{\"OldPassword\":\"$ADMIN_INIT_PASS\",\"NewPassword\":\"$ADMIN_PASS\"}" >/dev/null
AT="$(login_internal "$ADMIN_PASS" | python3 -c 'import sys,json;print(json.load(sys.stdin)["Data"]["AccessToken"])' 2>/dev/null)"
[ -n "$AT" ] && ok "改密后重新登录成功" || { bad "改密后登录失败"; exit 2; }

curl -sS --max-time 15 -H "Authorization: Bearer $AT" -H 'content-type: application/json' \
    -X POST "$GO_API/storage/configs" -d '{
      "Name":"E2E Mock","Type":"mockupload","PicgoConfigName":"Default",
      "Enabled":true,"IsDefault":true,
      "PathTemplate":"{Y}/{m}/{d}","FileTemplate":"{filename}-{md5-8}",
      "Config":{"bucket":"pics","path":"img/"}}' >/dev/null

STUID="$(curl -sS --max-time 15 -H "Authorization: Bearer $AT" "$GO_API/storage/configs" \
    | python3 -c 'import sys,json;print(json.load(sys.stdin)["Data"]["Items"][0]["UID"])' 2>/dev/null)"
[ -n "$STUID" ] && ok "已创建 mock 存储（UID=${STUID:0:12}…）" || { bad "创建存储失败"; exit 2; }

# 测试图片（1x1 PNG）
python3 - "$WORK/pic.png" <<'PY'
import base64, pathlib, sys
pathlib.Path(sys.argv[1]).write_bytes(base64.b64decode(
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=='))
PY

# ---------------------------------------------------------------------------
# 3. Lsky 端点
# ---------------------------------------------------------------------------
c_head "3. Lsky 契约端点"

# ---- 3.1 POST /tokens（含「可重复签发」这条实测踩到的语义）----
# 响应必须是 Lsky 信封
lsky_token() {
    curl -sS --max-time 15 -H 'content-type: application/json' \
        -X POST "$LSKY_API/tokens" -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASS\"}"
}

TOKRESP="$(lsky_token)"
if printf '%s' "$TOKRESP" | python3 -c '
import sys,json
d=json.load(sys.stdin)
assert d.get("status") is True, "status 不是 true"
assert isinstance(d.get("data",{}).get("token"), str), "data.token 不是字符串"
assert "Code" not in d, "出现了内部信封字段 Code"
' 2>/dev/null; then
    ok "POST /tokens 返回 Lsky 信封且含 data.token"
else
    bad "POST /tokens 响应不符合契约" "$(printf '%s' "$TOKRESP" | head -c 160)"
fi
TOK="$(printf '%s' "$TOKRESP" | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["token"])' 2>/dev/null)"

# 再签发一次：**必须也成功**（客户端会反复登录）
TOK2RESP="$(lsky_token)"
TOK2="$(printf '%s' "$TOK2RESP" | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["token"])' 2>/dev/null)"
if [ -n "$TOK2" ] && [ "$TOK2" != "$TOK" ]; then
    ok "重复签发成功且令牌不同（同名旧令牌自动吊销）"
else
    bad "重复签发失败（应返回新令牌而不是 409 Conflict）" "$(printf '%s' "$TOK2RESP" | head -c 160)"
fi

# 旧令牌应失效（同名吊销语义）
OLD_CODE="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 15 -H "Authorization: Bearer $TOK" "$LSKY_API/profile")"
[ "$OLD_CODE" = "401" ] && ok "同名旧令牌已失效（401）" || bad "旧令牌仍可用（HTTP $OLD_CODE）"
TOK="$TOK2"

# ---- 3.2 GET /strategies（免鉴权）----
STRAT="$(curl -sS --max-time 15 "$LSKY_API/strategies")"
if printf '%s' "$STRAT" | python3 -c '
import sys,json
d=json.load(sys.stdin)
assert d.get("status") is True
items=d["data"]
assert isinstance(items,list) and len(items)>=1, "策略列表为空"
it=items[0]
for k in ("id","name","intro","key","default"):
    assert k in it, f"缺少字段 {k}"
assert isinstance(it["id"], str), "id 应为字符串（StorageUID）"
' 2>/dev/null; then
    ok "GET /strategies（免鉴权）返回字符串 id 与 default 标记"
else
    bad "GET /strategies 不符合契约" "$(printf '%s' "$STRAT" | head -c 160)"
fi

# ---- 3.3 GET /profile（字节 + KB 双套字段）----
PROF="$(curl -sS --max-time 15 -H "Authorization: Bearer $TOK" "$LSKY_API/profile")"
if printf '%s' "$PROF" | python3 -c '
import sys,json
d=json.load(sys.stdin)
assert d.get("status") is True
p=d["data"]
# 字节口径
for k in ("id","email","name","capacityBytes","usedBytes","imageNum","albumNum"):
    assert k in p, f"缺少字段 {k}"
# KB 口径（lsky 兼容）
assert "capacity" in p and "useCapacity" in p, "缺少 lsky 的 KB 字段"
' 2>/dev/null; then
    ok "GET /profile 同时提供字节与 KB 两套字段"
else
    bad "GET /profile 字段不全" "$(printf '%s' "$PROF" | head -c 200)"
fi

# ---- 3.4 POST /upload（**同步**返回 URL）----
T0=$(date +%s%N)
UP="$(curl -sS --max-time 90 -H "Authorization: Bearer $TOK" \
    -X POST "$LSKY_API/upload" \
    -F "file=@$WORK/pic.png" -F "strategy_id=$STUID" -F "permission=1")"
T1=$(date +%s%N)
ELAPSED_MS=$(( (T1 - T0) / 1000000 ))

if printf '%s' "$UP" | python3 -c '
import sys,json
d=json.load(sys.stdin)
assert d.get("status") is True, "status 不是 true"
img=d["data"]
for k in ("key","name","origin_name","pathname","size","mimetype","extension",
          "width","height","url","links"):
    assert k in img, f"缺少字段 {k}"
links=img["links"]
# lsky 的六种外链都要有
for k in ("url","html","markdown","bbcode","markdown_with_link","thumbnail_url"):
    assert k in links, f"links 缺少 {k}"
assert links["url"] == img["url"], "links.url 与 url 不一致"
# 本项目不做缩略图（D84）→ 必须为空串
assert links["thumbnail_url"] == "", "thumbnail_url 应为空（D84）"
assert img["url"].startswith("http"), "url 不是 http(s)"
' 2>/dev/null; then
    ok "POST /upload 同步返回 URL 与六种 links（耗时 ${ELAPSED_MS}ms）"
else
    bad "POST /upload 响应不符合契约" "$(printf '%s' "$UP" | head -c 240)"
fi
UPLOAD_KEY="$(printf '%s' "$UP" | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["key"])' 2>/dev/null)"

# ---- 3.5 GET /images（Laravel 分页形状）----
IMGS="$(curl -sS --max-time 15 -H "Authorization: Bearer $TOK" "$LSKY_API/images?page=1&per_page=5")"
if printf '%s' "$IMGS" | python3 -c '
import sys,json
d=json.load(sys.stdin)
assert d.get("status") is True
p=d["data"]
# Laravel 分页器的字段名（客户端依赖）
for k in ("data","current_page","last_page","per_page","total","from","to"):
    assert k in p, f"缺少分页字段 {k}"
assert isinstance(p["data"], list), "data.data 应为数组（双重 data）"
assert p["total"] >= 1, "total 应 >= 1"
if p["data"]:
    it=p["data"][0]
    ca = it.get("created_at")
    assert isinstance(ca, str), "created_at 应为字符串（Y-m-d H:i:s）"
    # 必须是 19 字符的 `Y-m-d H:i:s`，不能是 RFC3339
    assert len(ca) == 19, "created_at 格式不对: " + str(ca)
' 2>/dev/null; then
    ok "GET /images 返回 Laravel 分页形状（双重 data + 字符串时间）"
else
    bad "GET /images 不符合契约" "$(printf '%s' "$IMGS" | head -c 240)"
fi

# ---- 3.6 GET /albums（伪造响应：相册功能已移除，恒返回空数组）----
ALBUMS="$(curl -sS --max-time 15 -H "Authorization: Bearer $TOK" "$LSKY_API/albums")"
printf '%s' "$ALBUMS" | python3 -c '
import sys,json
d=json.load(sys.stdin)
assert d.get("status") is True and isinstance(d["data"], list)
assert len(d["data"]) == 0, "albums 应为伪造的空列表"
' 2>/dev/null && ok "GET /albums 返回伪造的空数组" || bad "GET /albums 不符合契约" "$(printf '%s' "$ALBUMS" | head -c 160)"

# ---- 3.7 DELETE /images/{key} ----
if [ -n "$UPLOAD_KEY" ]; then
    DEL="$(curl -sS --max-time 20 -H "Authorization: Bearer $TOK" -X DELETE "$LSKY_API/images/$UPLOAD_KEY")"
    if printf '%s' "$DEL" | python3 -c 'import sys,json;d=json.load(sys.stdin);assert d.get("status") is True' 2>/dev/null; then
        LEFT="$(curl -sS --max-time 15 -H "Authorization: Bearer $TOK" "$LSKY_API/images" \
            | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["total"])' 2>/dev/null)"
        [ "$LEFT" = "0" ] && ok "DELETE /images/{key} 删除成功（剩余 $LEFT）" || bad "删除后 total 应为 0，实际 $LEFT"
    else
        bad "DELETE /images/{key} 失败" "$(printf '%s' "$DEL" | head -c 160)"
    fi
else
    bad "未拿到上传 key，跳过删除测试"
fi

# ---- 3.8 DELETE /tokens（清空令牌）----
if curl -sS --max-time 15 -H "Authorization: Bearer $TOK" -X DELETE "$LSKY_API/tokens" \
    | python3 -c 'import sys,json;assert json.load(sys.stdin).get("status") is True' 2>/dev/null; then
    ok "DELETE /tokens 清空成功"
    C="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 15 -H "Authorization: Bearer $TOK" "$LSKY_API/profile")"
    [ "$C" = "401" ] && ok "清空后令牌立即失效（401）" || bad "清空后令牌仍可用（HTTP $C）"
else
    bad "DELETE /tokens 失败"
fi

# ---------------------------------------------------------------------------
# 4. 信封一致性（**最易错、最难排查**）
# ---------------------------------------------------------------------------
c_head "4. 信封一致性"

TOK="$(lsky_token | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["token"])' 2>/dev/null)"

python3 - "$LSKY_API" "$TOK" "$LISTEN_PORT" <<'PY'
import json, subprocess, sys

lsky_api, tok, port = sys.argv[1], sys.argv[2], sys.argv[3]
go_api = f"http://127.0.0.1:{port}/api/web/v1"
fails = []

def call(*args):
    r = subprocess.run(["curl", "-sS", "-w", "\n%{http_code}", "--max-time", "15", *args],
                       capture_output=True, text=True)
    out = (r.stdout or "") + (r.stderr or "")
    lines = out.rstrip().split("\n")
    code = lines[-1] if lines else "?"
    body = "\n".join(lines[:-1])
    try:
        return code, json.loads(body)
    except Exception:
        return code, None

def check(desc, expect_lsky, *args):
    code, d = call(*args)
    if d is None:
        fails.append(f"{desc}: 响应不是合法 JSON（HTTP {code}）")
        print(f"  ✗ {desc}（非 JSON，HTTP {code}）")
        return
    is_lsky = isinstance(d.get("status"), bool)
    is_internal = "Code" in d
    if expect_lsky:
        good = is_lsky and not is_internal
        want = "Lsky 信封"
    else:
        good = is_internal and not is_lsky
        want = "内部信封"
    if good:
        print(f"  ✓ {desc}")
    else:
        fails.append(f"{desc}: 期望 {want}，实际 {json.dumps(d, ensure_ascii=False)[:100]}")
        print(f"  ✗ {desc}: 期望 {want}，实际 {json.dumps(d, ensure_ascii=False)[:100]}")

print("  ── /api/v1/** 必须全部是 Lsky 信封 ──")
check("未认证 GET /profile",        True,  lsky_api + "/profile")
check("错令牌 GET /profile",        True,  "-H", "Authorization: Bearer pcw_bad", lsky_api + "/profile")
check("不存在图片 DELETE",         True,  "-H", f"Authorization: Bearer {tok}", "-X", "DELETE", lsky_api + "/images/up_nope")
check("不存在的路由 GET",           True,  "-H", f"Authorization: Bearer {tok}", lsky_api + "/nonexistent")
check("不存在的路由 POST",          True,  "-H", f"Authorization: Bearer {tok}", "-X", "POST", lsky_api + "/nope")
check("方法不对 PUT /upload",       True,  "-H", f"Authorization: Bearer {tok}", "-X", "PUT", lsky_api + "/upload")
check("错密码 POST /tokens",        True,  "-H", "content-type: application/json", "-X", "POST",
      lsky_api + "/tokens", "-d", '{"email":"a@b.c","password":"wrong"}')
check("缺字段 POST /tokens",        True,  "-H", "content-type: application/json", "-X", "POST",
      lsky_api + "/tokens", "-d", "{}")
check("不存在相册 DELETE",          True,  "-H", f"Authorization: Bearer {tok}", "-X", "DELETE", lsky_api + "/albums/al_nope")

print("  ── /api/web/v1/** 必须仍是内部信封 ──")
check("GET /api/web/v1/auth/me",    False, go_api + "/auth/me")
check("GET /api/web/v1/nonexistent", False, go_api + "/nonexistent")

sys.exit(1 if fails else 0)
PY
if [ $? -eq 0 ]; then
    ok "信封一致性全部通过（Lsky 层与内部 API 各自正确）"
else
    bad "信封一致性检查有失败项（见上方明细）"
fi

# ---------------------------------------------------------------------------
# 5. 冲突检测
# ---------------------------------------------------------------------------
c_head "5. 路由冲突检测"

if grep -q '路由冲突\|Lsky 保留区被闯入' "$WORK/server.log" 2>/dev/null; then
    bad "启动日志中出现路由冲突"
else
    ok "无路由冲突（/api/v1 与 /api/web/v1 前缀隔离，D80）"
fi

# ---------------------------------------------------------------------------
# 汇总
# ---------------------------------------------------------------------------
c_head "汇总"
printf '  通过 %s / 失败 %s\n' "$(c_ok "$PASS")" "$( [ "$FAIL" -eq 0 ] && c_ok "$FAIL" || c_bad "$FAIL" )"
if [ "$FAIL" -gt 0 ]; then
    printf '\n  失败项：\n'
    for c in "${FAILED_CASES[@]}"; do printf '    · %s\n' "$c"; done
    printf '\n  服务日志：%s\n' "$WORK/server.log"
    exit 1
fi

printf '\n%s\n' "$(c_ok '全部通过 ✓')"
exit 0
