#!/usr/bin/env bash
# P6 concurrency smoke: TWO projects sharing one daemon, against real
# headless editors. Covers the multi-project regressions fixed in P6:
#   G1: launching project B while project A is connected must open B's own
#       editor (not silently reuse A's session) and pin B active.
#   G2: a full `stop` must quit EVERY session's editor; `stop --session`
#       must quit exactly one and leave the daemon plus the other session.
#
# Usage (Git Bash, from godot-ai-cli/):
#   bash script/smoke-concurrent.sh
# Parameterized via env:
#   GODOT_BIN   Godot binary (default: the 4.7.2 mono Win64 path below)
#   HTTP_PORT / WS_PORT   custom ports (default 18105/19605; never 8000/9500)
set -u
cd "$(dirname "$0")/.."

CLI=./godot-ai-cli.exe
GODOT="${GODOT_BIN:-/d/software/Godot_v4.7.2-stable_mono_win64/Godot_v4.7.2-stable_mono_win64.exe}"
HTTP="${HTTP_PORT:-18105}"
WS="${WS_PORT:-19605}"

# Two throwaway minimal projects. Windows-style paths: the CLI and Godot do
# not understand MSYS mounts.
SCRATCH="${LOCALAPPDATA:-$HOME/AppData/Local}/Temp/godot-ai-cli-concurrent-smoke"
rm -rf "$SCRATCH"
mkdir -p "$SCRATCH/proj-a" "$SCRATCH/proj-b"
printf 'config_version=5\n\n[application]\n\nconfig/name="SmokeA"\n' > "$SCRATCH/proj-a/project.godot"
printf 'config_version=5\n\n[application]\n\nconfig/name="SmokeB"\n' > "$SCRATCH/proj-b/project.godot"
PROJ_A=$(cygpath -m "$SCRATCH/proj-a")
PROJ_B=$(cygpath -m "$SCRATCH/proj-b")

# Always tear the smoke daemon down, even on early failure: while alive it
# holds an override in the user's global EditorSettings.
cleanup() { "$CLI" stop --http-port "$HTTP" >/dev/null 2>&1 || true; }
trap cleanup EXIT

PASS=0; FAIL=0
stage_pass() { echo "PASS    $1"; PASS=$((PASS+1)); }
stage_fail() { echo "FAIL    $1"; shift; [ $# -gt 0 ] && echo "        $1" | head -3; FAIL=$((FAIL+1)); }

# jget <json> <python-expr on d> — tiny JSON extractor.
jget() { JSON_IN="$1" EXPR="$2" python -c 'import json,os; print(eval(os.environ["EXPR"], {"d": json.loads(os.environ["JSON_IN"])}))'; }

echo "== build =="
go build -o "$CLI" ./cmd/godot-ai-cli || { echo "BUILD FAILED"; exit 1; }

# Clean slate: stop any leftover smoke daemon, ignore failures.
"$CLI" stop --http-port "$HTTP" >/dev/null 2>&1

echo "== launch A (headless, ports $HTTP/$WS) =="
OUT_A=$("$CLI" launch --project "$PROJ_A" --godot "$GODOT" --headless \
  --http-port "$HTTP" --ws-port "$WS" --wait 120 2>&1) \
  && stage_pass "launch A" || { echo "LAUNCH A FAILED: $OUT_A"; exit 1; }
SES_A=$(jget "$OUT_A" "d['session_id']")

echo "== launch B while A is connected (G1) =="
OUT_B=$("$CLI" launch --project "$PROJ_B" --godot "$GODOT" --headless \
  --http-port "$HTTP" --ws-port "$WS" --wait 120 2>&1) \
  && stage_pass "launch B" || { echo "LAUNCH B FAILED: $OUT_B"; exit 1; }
SES_B=$(jget "$OUT_B" "d['session_id']")
PID_B=$(jget "$OUT_B" "d.get('launched_editor_pid', 0)")
if [ "$PID_B" != "0" ]; then
  stage_pass "B opened its own editor (pid $PID_B)"
else
  stage_fail "B opened its own editor" "launch B reused a session: $OUT_B"
fi
case "$SES_B" in
  *proj-b*|*proj_b*) stage_pass "B session id belongs to B ($SES_B)";;
  *) stage_fail "B session id belongs to B" "session_id=$SES_B";; # slug from project dir name
esac

echo "== session routing =="
STATUS=$("$CLI" status --http-port "$HTTP" 2>&1)
N_SESSIONS=$(jget "$STATUS" "len(d['sessions'])")
[ "$N_SESSIONS" = "2" ] && stage_pass "2 sessions connected" || stage_fail "2 sessions connected" "$STATUS"
B_ACTIVE=$(jget "$STATUS" "[s['active'] for s in d['sessions'] if s['session_id']=='$SES_B'][0]")
[ "$B_ACTIVE" = "True" ] && stage_pass "B pinned active after launch" || stage_fail "B pinned active after launch" "$STATUS"

# Unpinned op must hit the ACTIVE project (B); a --session pin must hit A.
"$CLI" scene create --path res://smoke_b.tscn --root-type Node2D --root-name RootB \
  --http-port "$HTTP" >/dev/null 2>&1 && stage_pass "unpinned scene create (targets B)" \
  || stage_fail "unpinned scene create (targets B)"
"$CLI" scene create --path res://smoke_a.tscn --root-type Node2D --root-name RootA \
  --session "$SES_A" --http-port "$HTTP" >/dev/null 2>&1 && stage_pass "pinned scene create (targets A)" \
  || stage_fail "pinned scene create (targets A)"
# On-disk proof of isolation: each scene must land in ITS project only.
if [ -f "$SCRATCH/proj-b/smoke_b.tscn" ] && [ ! -f "$SCRATCH/proj-a/smoke_b.tscn" ]; then
  stage_pass "B scene isolated to B"
else
  stage_fail "B scene isolated to B" "$(ls "$SCRATCH"/proj-*/*.tscn 2>/dev/null)"
fi
if [ -f "$SCRATCH/proj-a/smoke_a.tscn" ] && [ ! -f "$SCRATCH/proj-b/smoke_a.tscn" ]; then
  stage_pass "A scene isolated to A"
else
  stage_fail "A scene isolated to A" "$(ls "$SCRATCH"/proj-*/*.tscn 2>/dev/null)"
fi

echo "== stop --session A (G2 per-session teardown) =="
STOP_A=$("$CLI" stop --http-port "$HTTP" --session "$SES_A" 2>&1) \
  && stage_pass "stop --session A" || stage_fail "stop --session A" "$STOP_A"
STATUS=$("$CLI" status --http-port "$HTTP" 2>&1)
LEFT=$(jget "$STATUS" "[s['session_id'] for s in d['sessions']]")
[ "$LEFT" = "['$SES_B']" ] && stage_pass "only B remains connected" || stage_fail "only B remains connected" "$STATUS"

echo "== relaunch A after per-session stop =="
OUT_A2=$("$CLI" launch --project "$PROJ_A" --godot "$GODOT" --headless \
  --http-port "$HTTP" --ws-port "$WS" --wait 120 2>&1) \
  && stage_pass "relaunch A" || stage_fail "relaunch A" "$OUT_A2"

echo "== full stop quits every editor (G2) =="
PIDS=$("$CLI" status --http-port "$HTTP" | python -c "import json,sys; print(' '.join(str(s['editor_pid']) for s in json.load(sys.stdin)['sessions']))")
STOP_ALL=$("$CLI" stop --http-port "$HTTP" 2>&1) \
  && stage_pass "full stop" || stage_fail "full stop" "$STOP_ALL"
echo "$STOP_ALL" | grep -q '"settings_restored":true' \
  && stage_pass "settings restored" || stage_fail "settings restored" "$STOP_ALL"
# Headless editors take a while to exit; allow a grace window. The image
# match uses a prefix of the actual binary name (tasklist truncates long
# image names, and a custom GODOT_BIN need not contain "godot").
GODOT_IMG=$(basename "$GODOT")
if [ -z "${PIDS// /}" ]; then
  stage_fail "no orphaned editors" "no editor pids captured from status — cannot check"
else
  DEADLINE=$((SECONDS + 45))
  while [ $SECONDS -lt $DEADLINE ]; do
    ALIVE=0
    for pid in $PIDS; do
      tasklist //FI "PID eq $pid" 2>/dev/null | grep -qiF "${GODOT_IMG:0:15}" && ALIVE=$((ALIVE+1))
    done
    [ "$ALIVE" -eq 0 ] && break
    sleep 3
  done
  [ "${ALIVE:-0}" -eq 0 ] && stage_pass "no orphaned editors" \
    || stage_fail "no orphaned editors" "$ALIVE editor(s) survived a full stop: $PIDS"
fi

# Keep the scratch projects on failure — they are the failure evidence.
if [ "$FAIL" -eq 0 ]; then
  rm -rf "$SCRATCH"
else
  echo "scratch kept for inspection: $SCRATCH"
fi

echo
echo "== summary: PASS=$PASS FAIL=$FAIL =="
[ "$FAIL" -eq 0 ]
