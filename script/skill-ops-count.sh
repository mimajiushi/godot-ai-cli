#!/usr/bin/env bash
# 同源刷新 / 断言 skill 包里的 ops 数（需求 R-8）。
#
# 唯一数据源 = 二进制的 `-v` 覆盖行（`plugin command coverage: N ops`，即
# ops.All() 的长度）。同一个数在 skill 包里落三处，本脚本让它们恒等：
#   1. SKILL.md frontmatter 描述行：`~N editor ops`
#   2. SKILL.md Need/Read 表：`for any of the N ops`
#   3. references/commands.md 标题行：`Generated from ... (N ops).`
#      （由 `godot-ai-cli commands --format md` 从同一张 op 表生成）
#
# 本地与 .github/workflows/release.yml 共用这一份逻辑：发布步骤先 --check
# 断言仓库里的 SKILL.md 没漂移（漂移即流水线失败），再渲染成二进制的
# coverage 后打包，最后对解包目录再跑一次 --check 做三方断言（R-8b）。
#
# 为什么是「数字就地渲染 + 断言」而不是留 <!--OPS_COUNT--> 占位标记：
#   - SKILL.md 第 4 行是 frontmatter 的 description（skill 的路由文本），
#     占位标记会原样进到那段 YAML 标量里，变成描述的一部分；
#   - 占位标记一旦漏渲染就会被发出去（一个可见缺陷），而「数字陈旧」会被
#     --check / 发布期断言拦下 —— R-8b 要的正是这种失败。
#   渲染仍按需求建议在打包步骤里用构建产物执行（见 release.yml）。
#
# 用法（Git Bash；脚本自身会 cd 到 godot-ai-cli/，所以 --bin/--dir 都相对它
# 解析；不传 --bin 时用当前目录的 godot-ai-cli[.exe]）：
#   bash script/skill-ops-count.sh --check  --bin <bin> --dir skill
#   bash script/skill-ops-count.sh --render --bin <bin> --dir skill
# --check  只读；任何一处数字不等于二进制 coverage 就打印 FAIL 并 exit 1。
# --render 就地改写 SKILL.md 的两处数字（幂等），随后仍执行同一套断言
#          （commands.md 不由本脚本生成，只能断言——它漂移时同样 exit 1）。
#
# 本地日常流程（workspace 根）：先渲染源目录，再同步到仓库快照：
#   (cd godot-ai-cli && bash script/skill-ops-count.sh --bin ./godot-ai-cli.exe --dir ../godot-ai-skill \
#      && bash script/sync-skill.sh)
set -euo pipefail
cd "$(dirname "$0")/.."

MODE="render"
BIN=""
DIR="skill"

usage() { sed -n '2,30p' "$0"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --check) MODE="check" ;;
    --render) MODE="render" ;;
    --bin)
      [ $# -ge 2 ] || { echo "skill-ops-count: ERROR: --bin needs a path" >&2; exit 2; }
      BIN="$2"; shift ;;
    --dir)
      [ $# -ge 2 ] || { echo "skill-ops-count: ERROR: --dir needs a path" >&2; exit 2; }
      DIR="$2"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "skill-ops-count: ERROR: unknown argument '$1'" >&2; exit 2 ;;
  esac
  shift
done

if [ -z "$BIN" ]; then
  for cand in ./godot-ai-cli.exe ./godot-ai-cli; do
    if [ -f "$cand" ]; then BIN="$cand"; break; fi
  done
fi
[ -n "$BIN" ] || { echo "skill-ops-count: ERROR: no binary found - pass --bin <path>" >&2; exit 2; }
[ -f "$BIN" ] || { echo "skill-ops-count: ERROR: binary '$BIN' not found" >&2; exit 2; }

SKILL="$DIR/SKILL.md"
CATALOG="$DIR/references/commands.md"
[ -f "$SKILL" ] || { echo "skill-ops-count: ERROR: $SKILL not found" >&2; exit 2; }
[ -f "$CATALOG" ] || { echo "skill-ops-count: ERROR: $CATALOG not found" >&2; exit 2; }

# 唯一数据源：二进制自己报的覆盖数。读不到就硬失败，绝不回落到「文件里
# 现有数字」——那样只会把一个陈旧数字又写回去。
count="$("$BIN" -v 2>/dev/null | sed -nE 's/.*plugin command coverage: *([0-9]+) ops.*/\1/p' | head -n1 || true)"
case "$count" in
  ''|*[!0-9]*)
    echo "skill-ops-count: ERROR: cannot read 'plugin command coverage: N ops' from '$BIN' -v" >&2
    exit 1 ;;
esac

if [ "$MODE" = "render" ]; then
  tmp="$SKILL.tmp.$$"
  # sed 只匹配 ASCII 片段，中文/UTF-8 字节原样透传；-E 让模式与 SKILL.md 的
  # 实际措辞一一对应（措辞被改写时下面的「恰好一处」断言会立刻报出来）。
  sed -E "s/~[0-9]+ editor ops/~$count editor ops/; s/for any of the [0-9]+ ops/for any of the $count ops/" \
    "$SKILL" > "$tmp"
  if cmp -s "$tmp" "$SKILL"; then
    rm -f "$tmp"
    echo "skill-ops-count: SKILL.md already renders $count ops"
  else
    chmod --reference="$SKILL" "$tmp" 2>/dev/null || true
    mv "$tmp" "$SKILL"
    echo "skill-ops-count: rendered $SKILL -> $count ops"
  fi
fi

# 断言 1/2：SKILL.md 两处（各恰好一处，且等于 coverage）。
fm_nums="$(grep -oE '~[0-9]+ editor ops' "$SKILL" | sed -E 's/[^0-9]//g' || true)"
tbl_nums="$(grep -oE 'for any of the [0-9]+ ops' "$SKILL" | sed -E 's/[^0-9]//g' || true)"
fm_count="$(printf '%s' "$fm_nums" | grep -c . || true)"
tbl_count="$(printf '%s' "$tbl_nums" | grep -c . || true)"

failures=0
fail() { echo "FAIL: $*"; failures=$((failures+1)); }

[ "$fm_count" -eq 1 ] || fail "$SKILL must contain exactly one '~N editor ops' (frontmatter), found $fm_count"
[ "$tbl_count" -eq 1 ] || fail "$SKILL must contain exactly one 'for any of the N ops' (Need/Read table), found $tbl_count"
if [ "$fm_count" -eq 1 ] && [ "$fm_nums" != "$count" ]; then
  fail "$SKILL frontmatter says ~$fm_nums editor ops but $BIN reports $count - re-render: bash script/skill-ops-count.sh --render --bin $BIN --dir $DIR"
fi
if [ "$tbl_count" -eq 1 ] && [ "$tbl_nums" != "$count" ]; then
  fail "$SKILL Need/Read table says $tbl_nums ops but $BIN reports $count - re-render: bash script/skill-ops-count.sh --render --bin $BIN --dir $DIR"
fi

# 断言 3：references/commands.md 的标题行（只认标题行，域标题的 "## node (30 ops)"
# 也长得像，不能误取）。
cat_num="$(grep -m1 -oE '^Generated from .*\([0-9]+ ops\)' "$CATALOG" | sed -E 's/.*\(([0-9]+) ops\)/\1/' || true)"
if [ "$cat_num" != "$count" ]; then
  fail "$CATALOG title says '${cat_num:-nothing}' ops but $BIN reports $count - regenerate: $BIN commands --format md > $CATALOG"
fi

[ "$failures" -eq 0 ] || exit 1
echo "ok:   ops count $count == SKILL.md (~$count editor ops / the $count ops) == references/commands.md"
