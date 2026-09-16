#!/usr/bin/env bash
# 同步默认主题：PicGo-Web-Theme 仓库 → server/internal/theme/embedded/（内嵌兜底副本）
#
# 默认主题的**源码真相源**是 https://github.com/Yeqingky/PicGo-Web-Theme；
# 本仓库二进制里随附的内嵌副本（D94「永不白屏」兜底 + 离线 seed 最后回退）
# 由本脚本从主题仓库同步并提交。
#
# 用法：make theme-sync [THEME_REPO=...]
set -euo pipefail

THEME_REPO="${THEME_REPO:-https://github.com/Yeqingky/PicGo-Web-Theme.git}"
DEST_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEST="$DEST_ROOT/server/internal/theme/embedded"
SRC="${THEME_SRC:-}"

if [[ -n "$SRC" ]]; then
  echo "==> 使用本地主题目录: $SRC"
else
  TMP="$(mktemp -d)"
  trap 'rm -rf "$TMP"' EXIT
  echo "==> 浅克隆 $THEME_REPO"
  git clone --depth 1 --single-branch --quiet "$THEME_REPO" "$TMP/repo"
  SRC="$TMP/repo"
fi

[[ -f "$SRC/manifest.json" && -f "$SRC/index.html" ]] || {
  echo "✗ 源目录缺少 manifest.json / index.html（主题必须在仓库根）" >&2
  exit 1
}

mkdir -p "$DEST"
rm -rf "$DEST/manifest.json" "$DEST/index.html" "$DEST/screenshot.png"
cp "$SRC/manifest.json" "$SRC/index.html" "$DEST/"
if [[ -f "$SRC/screenshot.png" ]]; then
  cp "$SRC/screenshot.png" "$DEST/"
fi

echo "==> 已同步到 $DEST"
echo "    若有变更请随主题仓库的版本发布一起提交（manifest.Version 与发布标签一致）。"
git -C "$DEST_ROOT" diff --stat -- server/internal/theme/embedded || true
