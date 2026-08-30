#!/usr/bin/env bash
# P4 smoke pass: launch the demo project headless on custom ports, run at
# least one command per CLI domain against the real editor, then stop and
# verify the EditorSettings backup/restore round trip.
#
# Usage (Git Bash, from godot-ai-cli/):  bash script/smoke-p4.sh
#
# Result taxonomy per command:
#   PASS    — exit 0 (data payload returned)
#   WIRE-OK — exit 1 with a structured plugin error envelope (proves the
#             command reached the plugin and was answered; used where the
#             demo project lacks the assets a full success would need)
#   FAIL    — anything else (transport error, wrong envelope, timeout)
set -u
cd "$(dirname "$0")/.."

CLI=./godot-ai-cli.exe
GODOT=/d/software/Godot_v4.7.2-stable_mono_win64/Godot_v4.7.2-stable_mono_win64.exe
PROJECT=../demo
HTTP=18102
WS=19602
SETTINGS="$APPDATA/Godot/editor_settings-4.7.tres"

PASS=0; FAIL=0; WIREOK=0

# run_op <domain> <description> <args...>
run_op() {
  local domain="$1" desc="$2"; shift 2
  local out rc
  out=$("$CLI" "$@" --http-port "$HTTP" 2>&1); rc=$?
  if [ "$rc" -eq 0 ]; then
    echo "PASS    $domain — $desc"
    PASS=$((PASS+1))
  elif echo "$out" | grep -q '"code"'; then
    echo "WIRE-OK $domain — $desc  ($(echo "$out" | grep -o '"code":"[A-Z_]*"' | head -1))"
    WIREOK=$((WIREOK+1))
  else
    echo "FAIL    $domain — $desc"
    echo "        $out" | head -3
    FAIL=$((FAIL+1))
  fi
}

echo "== build =="
go build -o "$CLI" ./cmd/godot-ai-cli || exit 1

# Snapshot the user's real EditorSettings: launch with custom ports mutates
# it, stop must restore it byte-identically.
BEFORE=$(sha256sum "$SETTINGS" | cut -d' ' -f1)

echo "== launch (headless, ports $HTTP/$WS) =="
"$CLI" launch --project "$PROJECT" --godot "$GODOT" --headless \
  --http-port "$HTTP" --ws-port "$WS" --wait 90 || { echo "LAUNCH FAILED"; exit 1; }

echo "== per-domain ops =="
run_op editor    "state"                 editor state
run_op scene     "create scratch scene"  scene create --path res://p4_smoke.tscn --root-type Node3D --root-name P4Smoke
run_op scene     "save scratch scene"    scene save
run_op scene     "get-hierarchy"         scene get-hierarchy
run_op node      "create SmokeNode"      node create --type Node3D --name SmokeNode
run_op node      "find SmokeNode"        node find --name SmokeNode
run_op script    "create + diagnostics"  script create --path res://p4_smoke_script.gd --content "extends Node"
run_op script    "read back"             script read --path res://p4_smoke_script.gd
run_op project   "settings-get"          project settings-get --key application/config/name
run_op session   "list"                  session list
run_op test      "results-get"           test results-get
run_op animation "player-create"         animation player-create --parent-path . --name SmokePlayer
run_op animation "list"                  animation list --player-path SmokePlayer
run_op material  "create"                material create --path res://p4_smoke_material.tres
run_op material  "list"                  material list
run_op audio     "list"                  audio list
run_op particle  "create"                particle create --parent-path . --name SmokeParticles --type gpu_3d
run_op camera    "create + list"         camera create --parent-path . --name SmokeCam --type 3d
run_op signal    "list on root"          signal list --path .
run_op input-map "list"                  input-map list
run_op autoload  "list"                  autoload list
run_op filesystem "read scratch script"  filesystem read-text --path res://p4_smoke_script.gd
run_op filesystem "scan"                 filesystem scan
run_op theme     "create"                theme create --path res://p4_smoke_theme.tres
run_op ui        "create Label for ui"   node create --type Label --name SmokeLabel
run_op ui        "label set-text"        ui set-text --path SmokeLabel --text p4
run_op resource  "create TileSet"        resource create --type TileSet --resource-path res://p4_smoke_tileset.tres
run_op resource  "search"                resource search --type Script
run_op api       "get-class Node3D"      api get-class --class-name Node3D
run_op tilemap   "create TileMapLayer"   node create --type TileMapLayer --name SmokeTileMap
run_op tilemap   "get-cells"             tilemap get-cells --path SmokeTileMap
run_op tileset   "get-atlas-tiles"       tileset get-atlas-tiles --tileset-path res://p4_smoke_tileset.tres --source-id 0
run_op gridmap   "create GridMap"        node create --type GridMap --name SmokeGridMap
run_op gridmap   "get-used-cells"        gridmap get-used-cells --path SmokeGridMap
run_op csg       "create box"            csg create --parent-path . --shape box --name SmokeCSG
run_op custom    "list"                  custom list
run_op batch     "execute editor state"  batch execute --params '{"commands":[{"command":"get_editor_state","params":{}}]}'
run_op logs      "read"                  logs read --count 5
run_op editor    "screenshot (2d)"       editor screenshot --source viewport_2d --include-image=false
run_op editor    "selection-get"         editor selection-get
run_op call      "escape hatch"          call get_open_scenes

echo "== game domain (run the scratch scene) =="
# The demo project has no main scene, so --scene would be ignored by the
# default mode; --mode current plays the open scratch scene instead.
run_op project   "run scratch scene"     project run --mode current
# The running game needs a moment before the plugin answers game commands;
# poll get-scene-tree instead of trusting a fixed sleep.
GAME_OK=0
for i in $(seq 1 15); do
  out=$("$CLI" game get-scene-tree --http-port "$HTTP" 2>&1)
  if [ $? -eq 0 ]; then
    echo "PASS    game — get-scene-tree (attempt $i)"
    PASS=$((PASS+1)); GAME_OK=1
    break
  fi
  sleep 1
done
if [ "$GAME_OK" -eq 0 ]; then
  echo "FAIL    game — get-scene-tree (never succeeded)"
  echo "        $out" | head -3
  FAIL=$((FAIL+1))
fi
run_op game      "input-state"           game input-state
run_op project   "stop"                  project stop

echo "== stop + settings restore =="
"$CLI" stop --http-port "$HTTP"
AFTER=$(sha256sum "$SETTINGS" | cut -d' ' -f1)
if [ "$BEFORE" = "$AFTER" ]; then
  echo "PASS    settings — EditorSettings byte-identical after stop"
  PASS=$((PASS+1))
else
  echo "FAIL    settings — EditorSettings changed across launch/stop"
  FAIL=$((FAIL+1))
fi

# The plugin surface has no file-delete op (upstream filesystem_manage has
# none either): scratch files are removed from disk here, after the editor
# is down, and the import cache is left for the next editor start.
rm -f "$PROJECT"/p4_smoke.tscn "$PROJECT"/p4_smoke_script.gd \
      "$PROJECT"/p4_smoke_material.tres "$PROJECT"/p4_smoke_theme.tres \
      "$PROJECT"/p4_smoke_tileset.tres

echo
echo "== summary: PASS=$PASS WIRE-OK=$WIREOK FAIL=$FAIL =="
[ "$FAIL" -eq 0 ]
