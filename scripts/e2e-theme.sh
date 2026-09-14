#!/usr/bin/env bash
# 主题系统的端到端验收脚本（B9.3）
#
# 用**真实二进制 + 真实 HTTP 请求**逐条验证：
#   · 首启 seed、首页由主题渲染、其余页面由内置 SPA 渲染
#   · 认证页与 /admin/** 永远不被主题接管（含「恶意 Pages」场景）
#   · 资源前缀分离（/theme-assets/** vs /assets/**）与路径穿越防护
#   · 主题切换立即生效；Pages 改动后 rescan 即生效（Go 零改动）
#   · 删掉整个主题目录也不白屏（内嵌兜底）
#
# 用法：bash scripts/e2e-theme.sh
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${BIN:-/tmp/pw-theme-e2e}"
RUN="${RUN:-/tmp/pw-e2e}"
PORT="${PORT:-18099}"
BASE="http://127.0.0.1:${PORT}"
API="${BASE}/api/web/v1"

PASS=0
FAIL=0
ok()   { PASS=$((PASS+1)); printf '  \033[32m✓\033[0m %s\n' "$1"; }
bad()  { FAIL=$((FAIL+1)); printf '  \033[31m✗\033[0m %s\n' "$1"; }
step() { printf '\n\033[36m▸ %s\033[0m\n' "$1"; }

# expect_contains <描述> <内容> <期望子串>
expect_contains() {
  if [[ "$2" == *"$3"* ]]; then ok "$1"; else bad "$1（未找到 $(printf '%q' "$3"))"; fi
}
expect_not_contains() {
  if [[ "$2" != *"$3"* ]]; then ok "$1"; else bad "$1（不应包含 $(printf '%q' "$3")）"; fi
}
expect_eq() {
  if [[ "$2" == "$3" ]]; then ok "$1"; else bad "$1（实际 $(printf '%q' "$2")，期望 $(printf '%q' "$3")）"; fi
}

cleanup() {
  [[ -n "${PID:-}" ]] && kill -9 "$PID" 2>/dev/null
  wait 2>/dev/null
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
step "构建二进制"
(cd "$ROOT/server" && CGO_ENABLED=0 go build -o "$BIN" ./cmd/picgo-web) || { echo "构建失败"; exit 1; }
ok "go build -o $BIN"

# ---------------------------------------------------------------------------
step "准备全新数据目录并启动"
rm -rf "$RUN"; mkdir -p "$RUN/data"
cat > "$RUN/.env" <<EOF
PICGO_WEB_LISTEN=127.0.0.1:${PORT}
PICGO_WEB_DATA_DIR=./data
PICGO_WEB_DB_DRIVER=sqlite
PICGO_WEB_LOG_LEVEL=info
PICGO_WEB_AGENT_AUTOSTART=false
EOF

cd "$RUN"
nohup "$BIN" > server.log 2>&1 &
PID=$!

for _ in $(seq 1 40); do
  [[ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/healthz" 2>/dev/null)" == "200" ]] && break
  sleep 0.25
done

HEALTH="$(curl -s "$BASE/healthz")"
expect_contains "服务已就绪（/healthz）" "$HEALTH" '"status":"ok"'
expect_contains "首启已 seed 主题（日志）" "$(cat server.log)" "种子写入"
[[ -f data/themes/default/manifest.json ]] && ok "data/themes/default/manifest.json 存在" || bad "缺 manifest.json"
[[ -f data/themes/default/index.html ]] && ok "data/themes/default/index.html 存在" || bad "缺 index.html"

# ---------------------------------------------------------------------------
step "首页由主题渲染；其余页面由内置 SPA 渲染"

PAGE_HOME="$(curl -s "$BASE/")"
expect_contains "GET / 由主题渲染" "$PAGE_HOME" 'id="heroTitle"'

PAGE_GALLERY="$(curl -s "$BASE/gallery")"
expect_contains "GET /gallery 由内置 SPA 渲染" "$PAGE_GALLERY" 'id="root"'
expect_not_contains "GET /gallery 不是主题页面" "$PAGE_GALLERY" 'id="heroTitle"'

PAGE_LOGIN="$(curl -s "$BASE/login")"
expect_contains "GET /login 由内置 SPA 渲染" "$PAGE_LOGIN" 'id="root"'
expect_not_contains "GET /login 不是主题页面" "$PAGE_LOGIN" 'id="heroTitle"'

PAGE_ADMIN="$(curl -s "$BASE/admin/themes")"
expect_contains "GET /admin/themes 由内置 SPA 渲染" "$PAGE_ADMIN" 'id="root"'

expect_eq "GET /assets 资源前缀属于内置 SPA（路由已注册）" \
  "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/assets/")" "404"
expect_eq "GET /themes/** 一律 404（不暴露主题目录）" \
  "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/themes/default/manifest.json")" "404"

# ---------------------------------------------------------------------------
step "登录并取管理员令牌"
INIT_PW="$(cat data/initial-admin-password.txt)"
LOGIN="$(curl -s -H 'content-type: application/json' -X POST "$API/auth/login" \
  -d "{\"Email\":\"admin@localhost\",\"Password\":\"${INIT_PW}\"}")"
TOKEN="$(printf '%s' "$LOGIN" | python3 -c 'import sys,json;print(json.load(sys.stdin)["Data"]["AccessToken"])' 2>/dev/null)"
if [[ -n "$TOKEN" ]]; then ok "登录成功并拿到 AccessToken"; else bad "登录失败: $LOGIN"; fi

curl -s -o /dev/null -H "Authorization: Bearer ${TOKEN}" -H 'content-type: application/json' \
  -X PATCH "$API/auth/password" -d "{\"OldPassword\":\"${INIT_PW}\",\"NewPassword\":\"E2ePassw0rd!2026\"}"

LOGIN2="$(curl -s -H 'content-type: application/json' -X POST "$API/auth/login" \
  -d '{"Email":"admin@localhost","Password":"E2ePassw0rd!2026"}')"
TOKEN="$(printf '%s' "$LOGIN2" | python3 -c 'import sys,json;print(json.load(sys.stdin)["Data"]["AccessToken"])' 2>/dev/null)"
[[ -n "$TOKEN" ]] && ok "改密并重新登录成功" || bad "改密后登录失败: $LOGIN2"
AUTH=(-H "Authorization: Bearer ${TOKEN}")

# ---------------------------------------------------------------------------
step "公开站点信息 GET /site/config"
SITE="$(curl -s "$API/site/config")"
expect_contains "site/config 返回成功" "$SITE" '"Code":0'
expect_contains "含 Theme.Settings.BackgroundURL" "$SITE" '"BackgroundURL"'
expect_contains "Theme.AssetBase 指向 /theme-assets" "$SITE" '"AssetBase":"/theme-assets"'
expect_contains "Theme.Pages 为 [\"/\"]" "$SITE" '"Pages":["/"]'
expect_not_contains "AllowSelfRegistration 恒为 false" "$SITE" '"AllowSelfRegistration":true'
expect_not_contains "不泄露密钥" "$SITE" 'clientSecret'

# ---------------------------------------------------------------------------
step "后台主题列表 GET /themes（admin）"
THEMES="$(curl -s "${AUTH[@]}" "$API/themes")"
expect_contains "列表含 default" "$THEMES" '"ID":"default"'
expect_contains "default 为当前启用" "$THEMES" '"IsActive":true'
expect_contains "default 标记为内置" "$THEMES" '"IsBuiltin":true'
expect_contains "default 不可卸载" "$THEMES" '"CanUninstall":false'

# ---------------------------------------------------------------------------
step "手动放入第二个主题并 rescan → 切换立即生效"

mkdir -p data/themes/mytheme
cat > data/themes/mytheme/manifest.json <<'EOF'
{
  "ID": "mytheme",
  "Name": { "zh-CN": "我的主题", "en": "My Theme" },
  "Version": "9.9.9",
  "Pages": ["/"],
  "Configuration": {
    "Type": "managed",
    "Items": [
      { "Key": "BackgroundURL", "Name": "背景图", "Type": "string", "Default": "https://example/bg.png" }
    ]
  }
}
EOF
echo '<html><body>MYTHEME-INDEX-MARKER</body></html>' > data/themes/mytheme/index.html
mkdir -p data/themes/mytheme/assets
echo 'console.log("theme-asset")' > data/themes/mytheme/assets/app.js

curl -s -o /dev/null "${AUTH[@]}" -X POST "$API/themes/rescan"
RESCAN="$(curl -s "${AUTH[@]}" "$API/themes")"
expect_contains "rescan 后发现 mytheme" "$RESCAN" '"ID":"mytheme"'
expect_contains "mytheme 的 Pages 展示正确" "$RESCAN" '"Pages":["/"]'

SWITCH="$(curl -s "${AUTH[@]}" -H 'content-type: application/json' -X PUT "$API/themes/active" \
  -d '{"ThemeID":"mytheme"}')"
expect_contains "切换主题成功" "$SWITCH" '"Active":"mytheme"'

PAGE_HOME2="$(curl -s "$BASE/")"
expect_contains "切换后首页**立即**变为 mytheme（无需重启）" "$PAGE_HOME2" 'MYTHEME-INDEX-MARKER'

# ---------------------------------------------------------------------------
step "改动 Pages 加 /gallery → rescan 后 Go 零改动即生效"

curl -s -o /dev/null "${AUTH[@]}" -X DELETE "$API/themes/mytheme/settings"
python3 - <<'PY'
import json, pathlib
p = pathlib.Path("data/themes/mytheme/manifest.json")
m = json.loads(p.read_text())
m["Pages"] = ["/", "/gallery"]
p.write_text(json.dumps(m, ensure_ascii=False, indent=2))
PY
curl -s -o /dev/null "${AUTH[@]}" -X POST "$API/themes/rescan"

PAGE_GALLERY2="$(curl -s "$BASE/gallery")"
expect_contains "改动 Pages 后 /gallery 改由主题渲染（Go 未改动）" "$PAGE_GALLERY2" 'MYTHEME-INDEX-MARKER'
PAGE_UPLOAD="$(curl -s "$BASE/upload")"
expect_contains "未注册的 /upload 仍由内置 SPA 渲染" "$PAGE_UPLOAD" 'id="root"'

# ---------------------------------------------------------------------------
step "恶意 Pages（/login、/admin）必须被拒，且登录页/后台仍由内置 SPA 渲染"

python3 - <<'PY'
import json, pathlib
p = pathlib.Path("data/themes/mytheme/manifest.json")
m = json.loads(p.read_text())
m["Pages"] = ["/", "/login", "/admin"]
p.write_text(json.dumps(m, ensure_ascii=False, indent=2))
PY
curl -s -o /dev/null "${AUTH[@]}" -X POST "$API/themes/rescan"

THEMES3="$(curl -s "${AUTH[@]}" "$API/themes")"
expect_contains "登录页冲突的主题被标记为不合法" "$THEMES3" '"Valid":false'
expect_contains "错误原因指出 /login 冲突" "$THEMES3" '/login'

PAGE_LOGIN3="$(curl -s "$BASE/login")"
expect_not_contains "GET /login 未被主题接管" "$PAGE_LOGIN3" 'MYTHEME-INDEX-MARKER'
expect_contains "GET /login 仍由内置 SPA 渲染" "$PAGE_LOGIN3" 'id="root"'
PAGE_ADMIN3="$(curl -s "$BASE/admin/users")"
expect_not_contains "GET /admin/users 未被主题接管" "$PAGE_ADMIN3" 'MYTHEME-INDEX-MARKER'

expect_eq "不合法主题不可启用（应 40001）" \
  "$(curl -s -o /dev/null -w '%{http_code}' "${AUTH[@]}" -H 'content-type: application/json' \
     -X PUT "$API/themes/active" -d '{"ThemeID":"mytheme"}')" "400"

ERROR_LOGS="$(python3 - <<'PY'
import sqlite3
con = sqlite3.connect("data/picgo-web.db")
n = con.execute("SELECT COUNT(*) FROM OperationLogs WHERE Type='theme.error'").fetchone()[0]
print(n)
con.close()
PY
)"
[[ "$ERROR_LOGS" -ge 1 ]] && ok "OperationLogs 已记录 theme.error（$ERROR_LOGS 条）" \
                          || bad "缺少 theme.error 审计"

# 恢复成合法主题，供后续步骤使用
python3 - <<'PY'
import json, pathlib
p = pathlib.Path("data/themes/mytheme/manifest.json")
m = json.loads(p.read_text())
m["Pages"] = ["/"]
p.write_text(json.dumps(m, ensure_ascii=False, indent=2))
PY
curl -s -o /dev/null "${AUTH[@]}" -X POST "$API/themes/rescan"

# ---------------------------------------------------------------------------
step "主题资源前缀与路径穿越防护"

ASSET_CODE="$(curl -s -o /dev/null -w '%{http_code}' "$BASE/theme-assets/app.js")"
expect_eq "GET /theme-assets/app.js → 200" "$ASSET_CODE" "200"
expect_contains "主题资源带 immutable 长缓存" \
  "$(curl -s -D - -o /dev/null "$BASE/theme-assets/app.js" | tr -d '\r')" "immutable"

# ⚠️ 必须用 --path-as-is：curl（以及多数 HTTP 客户端）默认会在**客户端**把 `..`
# 折叠掉，于是服务端收到的其实是 `/manifest.json` 这类正常路径 ——
# 那样测出来的是「客户端行为」，根本不是服务端防护。
for p in "/theme-assets/../manifest.json" "/theme-assets/../../etc/passwd" "/theme-assets/a/../../manifest.json"; do
  BODY="$(curl -s --path-as-is "$BASE$p")"
  CODE="$(curl -s --path-as-is -o /dev/null -w '%{http_code}' "$BASE$p")"
  expect_contains "$p → 40401" "$BODY" '"Code":40401'
  [[ "$CODE" != "200" ]] && ok "$p 未返回 200" || bad "$p 返回了 200"
done

JS_NAME="$(cd "$ROOT/server/internal/webfs/dist/assets" 2>/dev/null && ls *.js 2>/dev/null | head -1)"
if [[ -n "$JS_NAME" ]]; then
  CODE="$(curl -s -o /dev/null -w '%{http_code}' "$BASE/assets/$JS_NAME")"
  expect_eq "GET /assets/<内置 SPA 的 js> → 200" "$CODE" "200"
else
  echo "  (跳过 /assets 断言：内置 SPA 未构建)"
fi
# /assets/../index.html：若「先 Clean 再检查」会被归一为 index.html（在 dist 内）而放行，
# 于是把一个 HTML 当成 JS 资源返回 —— 正是契约警告的反模式。
AT_CODE="$(curl -s --path-as-is -o /dev/null -w '%{http_code}' "$BASE/assets/../index.html")"
AT_BODY="$(curl -s --path-as-is "$BASE/assets/../index.html")"
[[ "$AT_CODE" != "200" ]] && ok "/assets/../index.html 未返回 200（不把 index.html 当资源）" \
                          || bad "/assets/../index.html 返回了 200（HTML 被当成 JS 发出去）"
expect_not_contains "/assets/../index.html 不是 SPA 页面" "$AT_BODY" 'id="root"'

# ---------------------------------------------------------------------------
step "主题设置读写 + Source 徽章"
curl -s -o /dev/null "${AUTH[@]}" -H 'content-type: application/json' \
  -X PUT "$API/themes/active" -d '{"ThemeID":"mytheme"}'

SET="$(curl -s "${AUTH[@]}" "$API/themes/mytheme/settings")"
expect_contains "取设置成功" "$SET" '"ThemeID":"mytheme"'
expect_contains "初始 Source 为 default" "$SET" '"Source":"default"'

curl -s -o /dev/null "${AUTH[@]}" -H 'content-type: application/json' \
  -X PUT "$API/themes/mytheme/settings" -d '{"Values":{"BackgroundURL":"https://cdn.example/bg.png"}}'
SET2="$(curl -s "${AUTH[@]}" "$API/themes/mytheme/settings")"
expect_contains "写入后 Source 变为 db" "$SET2" '"Source":"db"'
expect_contains "值已生效" "$SET2" 'https://cdn.example/bg.png'

expect_eq "未声明的键被拒（40001）" \
  "$(curl -s -o /dev/null -w '%{http_code}' "${AUTH[@]}" -H 'content-type: application/json' \
     -X PUT "$API/themes/mytheme/settings" -d '{"Values":{"Nope":1}}')" "400"

# ---------------------------------------------------------------------------
step "zip 安装：正常包成功；含 ../ 的包被拒且无残留"

make_zips() {
python3 - <<'ZIPPER'
import json, zipfile

MANIFEST = {
    "ID": "zipped",
    "Name": {"zh-CN": "压缩包主题"},
    "Version": "2.0.0",
    "Pages": ["/"],
    "Configuration": {"Type": "managed", "Items": [
        {"Key": "BackgroundURL", "Name": "背景图", "Type": "string", "Default": "https://x/bg.png"}
    ]},
}

with zipfile.ZipFile("good.zip", "w") as z:
    z.writestr("manifest.json", json.dumps(MANIFEST, ensure_ascii=False))
    z.writestr("index.html", "<html>ZIPPED-THEME-MARKER</html>")
    z.writestr("assets/app.js", "console.log(1)")

# 恶意包：多两个越界 entry
evil = dict(MANIFEST, ID="evil")
with zipfile.ZipFile("evil.zip", "w") as z:
    z.writestr("manifest.json", json.dumps(evil, ensure_ascii=False))
    z.writestr("index.html", "<html>evil</html>")
    z.writestr("../escape.txt", "PWNED")
    z.writestr("../../escape2.txt", "PWNED")
ZIPPER
}
make_zips

INSTALL="$(curl -s "${AUTH[@]}" -F "File=@good.zip" "$API/themes/install")"
expect_contains "正常 zip 安装成功" "$INSTALL" '"Installed":true'
expect_contains "安装返回 ID" "$INSTALL" '"ID":"zipped"'
[[ -f data/themes/zipped/index.html ]] && ok "主题目录已就绪" || bad "主题目录未创建"

curl -s -o /dev/null "${AUTH[@]}" -X POST "$API/themes/rescan"
curl -s -o /dev/null "${AUTH[@]}" -H 'content-type: application/json' \
  -X PUT "$API/themes/active" -d '{"ThemeID":"zipped"}'
expect_contains "安装的主题可立即启用并渲染首页" "$(curl -s "$BASE/")" 'ZIPPED-THEME-MARKER'

expect_eq "重复安装（未勾选覆盖）返回 40901" \
  "$(curl -s -o /dev/null -w '%{http_code}' "${AUTH[@]}" -F "File=@good.zip" "$API/themes/install")" "409"

EVIL="$(curl -s "${AUTH[@]}" -F "File=@evil.zip" "$API/themes/install")"
expect_contains "含 ../ 的 zip 被拒（40001）" "$EVIL" '"Code":40001'
[[ ! -d data/themes/evil ]] && ok "恶意包未创建主题目录" || bad "恶意包创建了主题目录"
[[ ! -f ../escape.txt && ! -f escape.txt ]] && ok "未写出主题目录之外的文件" || bad "写出了越界文件！"
RESIDUE="$(ls -a data/themes 2>/dev/null | grep -c '^\.tmp-' || true)"
expect_eq "无 .tmp-* 临时目录残留" "$RESIDUE" "0"

# ---------------------------------------------------------------------------
step "卸载规则"
expect_eq "不能卸载 default（40901）" \
  "$(curl -s -o /dev/null -w '%{http_code}' "${AUTH[@]}" -X DELETE "$API/themes/default")" "409"

# 当前启用主题的 ID 可能是前面步骤切过去的，因此动态读取而不是写死
ACTIVE_ID="$(curl -s "${AUTH[@]}" "$API/themes" | python3 -c 'import sys,json;print(json.load(sys.stdin)["Data"]["Active"])')"
echo "  （当前启用：${ACTIVE_ID}）"
if [[ "$ACTIVE_ID" != "default" ]]; then
  expect_eq "不能卸载当前启用主题（40901）" \
    "$(curl -s -o /dev/null -w '%{http_code}' "${AUTH[@]}" -X DELETE "$API/themes/${ACTIVE_ID}")" "409"
  [[ -d "data/themes/${ACTIVE_ID}" ]] && ok "失败的卸载未删除目录" || bad "失败的卸载删掉了目录"
fi

expect_eq "卸载不存在的主题返回 40401" \
  "$(curl -s -o /dev/null -w '%{http_code}' "${AUTH[@]}" -X DELETE "$API/themes/ghost")" "404"

# 非启用主题可以正常卸载
curl -s -o /dev/null "${AUTH[@]}" -X POST "$API/themes/rescan"
VICTIM="$(curl -s "${AUTH[@]}" "$API/themes" | python3 -c '
import sys, json
d = json.load(sys.stdin)["Data"]
for it in d["Items"]:
    if it["CanUninstall"]:
        print(it["ID"]); break
')"
if [[ -n "$VICTIM" ]]; then
  expect_eq "非启用主题可卸载（${VICTIM}）" \
    "$(curl -s -o /dev/null -w '%{http_code}' "${AUTH[@]}" -X DELETE "$API/themes/${VICTIM}")" "200"
  [[ ! -d "data/themes/${VICTIM}" ]] && ok "卸载后目录已删除" || bad "卸载后目录仍在"
else
  echo "  (跳过：没有可卸载的主题)"
fi

# ---------------------------------------------------------------------------
step "删掉整个主题目录也不白屏（内嵌兜底）"
rm -rf data/themes
PAGE_AFTER="$(curl -s "$BASE/")"
expect_eq "GET / 仍返回 200" "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/")" "200"
expect_contains "首页由内嵌默认主题渲染" "$PAGE_AFTER" 'id="heroTitle"'
expect_contains "GET /login 仍由内置 SPA 渲染" "$(curl -s "$BASE/login")" 'id="root"'
SITE_AFTER="$(curl -s "$API/site/config")"
expect_contains "site/config 仍可用" "$SITE_AFTER" '"Code":0'

# ---------------------------------------------------------------------------
step "结果"
printf '\n  通过 %d 项，失败 %d 项\n' "$PASS" "$FAIL"
[[ "$FAIL" -eq 0 ]] || exit 1
