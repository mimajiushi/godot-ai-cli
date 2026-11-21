#!/usr/bin/env bash
# P5 end-to-end smoke: full product loop against a real headless editor.
# launch -> scene/node/script ops -> save -> full GDScript suite -> screenshot
# probe -> stop -> EditorSettings restore assertion.
#
# Usage (Git Bash, from godot-ai-cli/):
#   bash script/smoke-e2e.sh
# Parameterized via env:
#   GODOT_BIN   Godot binary (default: the 4.7.2 mono Win64 path below)
#   HTTP_PORT / WS_PORT   custom ports (default 18104/19604; never 8000/9500)
#   DEMO_PROJECT          project path (default ../demo)
#
# Expected suite baseline is pinned in demo/tests/README.md.
set -u
cd "$(dirname "$0")/.."

CLI=./godot-ai-cli.exe
GODOT="${GODOT_BIN:-/d/software/Godot_v4.7.2-stable_mono_win64/Godot_v4.7.2-stable_mono_win64.exe}"
HTTP="${HTTP_PORT:-18104}"
WS="${WS_PORT:-19604}"
PROJECT="${DEMO_PROJECT:-../demo}"
SETTINGS="${EDITOR_SETTINGS:-$APPDATA/Godot/editor_settings-4.7.tres}"
# 全量基线钉值 = ../demo/tests 的 t23 隔离车道 pristine 实测（upstream v4.4.2 /
# fork plugin 4.4.3），静态重数与 full.json 一致：102 套件 / 3186 条，
# failed=0、skipped=71、assertions=20263；与 demo/tests/README.md 的 baseline 表同源。
# 旧 88/2877 与 t11 的 102/3183 均为历史值，套件吸收后必须按最新实测更新。
EXPECTED_SUITES=102
EXPECTED_TOTAL=3186

PASS=0; FAIL=0

stage_pass() { echo "PASS    $1"; PASS=$((PASS+1)); }
stage_fail() { echo "FAIL    $1"; shift; [ $# -gt 0 ] && echo "        $1" | head -3; FAIL=$((FAIL+1)); }

# stage <name> <args...> — run a CLI op, PASS on exit 0.
stage() {
  local name="$1"; shift
  local out
  out=$("$CLI" "$@" --http-port "$HTTP" 2>&1)
  if [ $? -eq 0 ]; then stage_pass "$name"; else stage_fail "$name" "$out"; fi
}

echo "== build =="
go build -o "$CLI" ./cmd/godot-ai-cli || { echo "BUILD FAILED"; exit 1; }

BEFORE=$(sha256sum "$SETTINGS" | cut -d' ' -f1)

echo "== launch (headless, ports $HTTP/$WS) =="
"$CLI" launch --project "$PROJECT" --godot "$GODOT" --headless \
  --http-port "$HTTP" --ws-port "$WS" --wait 120 >/dev/null \
  && stage_pass "launch" || { echo "LAUNCH FAILED"; exit 1; }

# The filesystem scan must settle before test discovery can see the suites.
sleep 6

echo "== scene / node / script ops =="
stage "scene create"  scene create --path res://e2e_smoke.tscn --root-type Node3D --root-name E2eSmoke
stage "node create"   node create --type Node3D --name SmokeNode
stage "node set"      node set-property --path SmokeNode --property position --value '{"x":1,"y":2,"z":3}'
stage "script create" script create --path res://e2e_smoke_script.gd --content "extends Node3D"
stage "script attach" script attach --path SmokeNode --script-path res://e2e_smoke_script.gd
stage "scene save"    scene save

echo "== full GDScript suite (baseline: $EXPECTED_SUITES suites / $EXPECTED_TOTAL tests) =="
stage "open main.tscn" scene open --path res://main.tscn
TEST_OUT=$("$CLI" test run --http-port "$HTTP" 2>&1)
if [ $? -ne 0 ]; then
  stage_fail "test run" "$TEST_OUT"
else
  VERDICT=$(TEST_OUT="$TEST_OUT" EXPECTED_SUITES="$EXPECTED_SUITES" EXPECTED_TOTAL="$EXPECTED_TOTAL" python -c '
import json, os
d = json.loads(os.environ["TEST_OUT"])
problems = []
if d.get("failed", 1) != 0:
    problems.append("failed=%s: %s" % (d.get("failed"), json.dumps(d.get("failures"))[:300]))
if d.get("load_errors"):
    problems.append("load_errors=%s" % json.dumps(d.get("load_errors"))[:300])
if d.get("suite_count") != int(os.environ["EXPECTED_SUITES"]):
    problems.append("suite_count=%s (expected %s — discovery truncated?)" % (d.get("suite_count"), os.environ["EXPECTED_SUITES"]))
if d.get("total") != int(os.environ["EXPECTED_TOTAL"]):
    problems.append("total=%s (expected %s — in-suite discovery truncated?)" % (d.get("total"), os.environ["EXPECTED_TOTAL"]))
if problems:
    print("FAIL " + "; ".join(problems))
else:
    print("OK suites=%s total=%s passed=%s skipped=%s duration_ms=%s"
          % (d.get("suite_count"), d.get("total"), d.get("passed"), d.get("skipped"), d.get("duration_ms")))
')
  case "$VERDICT" in
    OK*) stage_pass "test run ($VERDICT)";;
    *)   stage_fail "test run" "$VERDICT";;
  esac
fi

echo "== screenshot probe =="
# Headless has no rendered viewport: the plugin answers with a structured
# EDITOR_NOT_READY (viewport_empty) envelope. That proves the wire path; a
# real capture needs a windowed editor. SKIP with a warning either way.
SHOT_OUT=$("$CLI" editor screenshot --source viewport --include-image=false --http-port "$HTTP" 2>&1)
if [ $? -eq 0 ]; then
  stage_pass "editor screenshot (unexpectedly works headless)"
elif echo "$SHOT_OUT" | grep -q 'EDITOR_NOT_READY'; then
  echo "SKIP    editor screenshot — headless viewport is empty (EDITOR_NOT_READY); run windowed for a real capture"
else
  stage_fail "editor screenshot" "$SHOT_OUT"
fi

echo "== stop + settings restore =="
STOP_OUT=$("$CLI" stop --http-port "$HTTP" 2>&1)
# godot-ai-cli fork：改用 stop 回执的现代契约。settings_restored 只在遗留 pre-3.2.9
# 备份被还原时才置位（internal/cli/status_stop.go），插件 >=3.2.9 的现代路径不再产生
# 备份，该键永不出现，旧断言不可满足会误报；EditorSettings 是否被改动由紧随其后的
# byte-identical 阶段实测覆盖，遗留还原路径由 internal/godot/launch_backup*_test.go 单测覆盖。
if [ $? -ne 0 ]; then
  stage_fail "stop" "$STOP_OUT"
elif echo "$STOP_OUT" | grep -q '"status":"stopped"'; then
  stage_pass "stop (status stopped)"
else
  stage_fail "stop" "$STOP_OUT"
fi
AFTER=$(sha256sum "$SETTINGS" | cut -d' ' -f1)
if [ "$BEFORE" = "$AFTER" ]; then
  stage_pass "settings byte-identical"
else
  stage_fail "settings byte-identical" "EditorSettings changed across launch/stop"
fi

# Scratch cleanup: the plugin surface has no file-delete op, so remove the
# smoke files from disk once the editor is down.
rm -f "$PROJECT"/e2e_smoke.tscn "$PROJECT"/e2e_smoke_script.gd \
      "$PROJECT"/e2e_smoke.tscn.uid "$PROJECT"/e2e_smoke_script.gd.uid

echo
echo "== summary: PASS=$PASS FAIL=$FAIL =="
[ "$FAIL" -eq 0 ]
