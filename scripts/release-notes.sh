#!/usr/bin/env bash
# release-notes.sh <tag>
# Categorize conventional commits (feat/fix/refactor/...) since the previous
# tag into a markdown changelog. Run from the repo root; the tag itself does
# not need to exist yet (it is excluded from the "previous tag" lookup).
set -euo pipefail

tag="${1:?usage: release-notes.sh <tag>}"
repo_url="${GITHUB_SERVER_URL:-https://github.com}/${GITHUB_REPOSITORY:-RoyOfficial233/MessageSender}"

prev=$(git tag --list --sort=-v:refname | grep -vxF "$tag" | head -n 1 || true)
if [ -n "$prev" ]; then
  range="$prev..HEAD"
  tail_note="自 \`$prev\` 以来的变更（按 commit 类型归类，自动生成）："
  compare_note="**Full diff**: [$prev...$tag](${repo_url}/compare/${prev}...${tag})"
else
  range="HEAD"
  tail_note="首个发布，包含全部变更（按 commit 类型归类，自动生成）："
  compare_note=""
fi

title_for() {
  case "$1" in
    feat)     echo "✨ 新功能 · Features" ;;
    fix)      echo "🐛 问题修复 · Bug Fixes" ;;
    refactor) echo "♻️ 重构 · Refactoring" ;;
    perf)     echo "⚡ 性能 · Performance" ;;
    docs)     echo "📝 文档 · Documentation" ;;
    test)     echo "✅ 测试 · Tests" ;;
    style)    echo "🎨 样式 · Style" ;;
    build)    echo "📦 构建 · Build" ;;
    ci)       echo "👷 CI / 部署 · CI & Deploy" ;;
    chore)    echo "🔧 其他维护 · Chores" ;;
    *)        echo "❓ 其他 · Other" ;;
  esac
}

order=(feat fix refactor perf docs test style build ci chore other)
declare -A lines

while IFS=$'\t' read -r sha subject; do
  [ -z "$sha" ] && continue
  low=$(printf '%s' "$subject" | tr '[:upper:]' '[:lower:]')
  type=$(printf '%s' "$low" | sed -nE 's/^(feat|fix|refactor|perf|docs|doc|test|style|build|ci|chore)(\([^)]*\))?!?:.*/\1/p')
  [ -z "$type" ] && type=other
  [ "$type" = "doc" ] && type=docs
  lines[$type]+="- ${subject} (${sha:0:7})"$'\n'
done < <(git log --no-merges --format='%H%x09%s' "$range")

echo "## 教室传话系统 ${tag}"
echo
echo "${tail_note}"
echo
for t in "${order[@]}"; do
  [ -n "${lines[$t]:-}" ] || continue
  echo "### $(title_for "$t")"
  echo
  printf '%s' "${lines[$t]}"
  echo
done
if [ -n "$compare_note" ]; then
  echo "${compare_note}"
fi
