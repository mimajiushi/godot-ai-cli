#!/usr/bin/env bash
# Build the demo dogfooding scenes (demo/scenes/01..08) by driving a running
# headless editor through godot-ai-cli itself. Procedural assets must already
# exist (run demo/tools/gen_procedural_assets.gd once first).
#
# Usage (Git Bash, from godot-ai-cli/, editor already launched):
#   bash script/build-demo-scenes.sh [http-port]
#
# Not idempotent: scene create/theme create fail on existing files. Delete
# demo/scenes/ and the generated assets before a rebuild.
set -u
cd "$(dirname "$0")/.."

CLI=./godot-ai-cli.exe
HTTP="${1:-18103}"

PASS=0; FAIL=0

# op <description> <args...>
op() {
  local desc="$1"; shift
  local out
  out=$("$CLI" "$@" --http-port "$HTTP" 2>&1)
  if [ $? -eq 0 ]; then
    PASS=$((PASS+1))
  else
    echo "FAIL $desc"
    echo "     $out" | head -3
    FAIL=$((FAIL+1))
  fi
}

echo "== 01_scene_node.tscn =="
op "scene"   scene create --path res://scenes/01_scene_node.tscn --root-type Node2D --root-name SceneNodeDemo
op "nodes"   node create --type Node2D --name Player
op "nodes"   node create --type Node2D --name Enemy
op "nodes"   node create --type Marker2D --name SpawnPoint --parent-path Player
op "nodes"   node create --type Timer --name TickTimer
op "timer"   node set-property --path TickTimer --property wait_time --value 0.5
op "timer"   node set-property --path TickTimer --property autostart --value true
op "script"  script attach --path . --script-path res://scripts/scene_node_demo.gd
op "signal"  signal connect --path TickTimer --signal timeout --target . --method _on_tick_timer_timeout
op "save"    scene save

echo "== 02_ui_theme.tscn =="
op "scene"   scene create --path res://scenes/02_ui_theme.tscn --root-type Control --root-name UIThemeDemo
op "anchor"  node set-property --path . --property anchor_right --value 1.0
op "anchor"  node set-property --path . --property anchor_bottom --value 1.0
op "nodes"   node create --type Panel --name MainPanel
op "nodes"   node set-property --path MainPanel --property custom_minimum_size --value '{"x":320,"y":200}'
op "nodes"   node create --type VBoxContainer --name VBox --parent-path MainPanel
op "nodes"   node create --type Label --name TitleLabel --parent-path MainPanel/VBox
op "label"   node set-property --path MainPanel/VBox/TitleLabel --property text --value '"Demo UI"'
op "nodes"   node create --type Button --name PlayButton --parent-path MainPanel/VBox
op "button"  node set-property --path MainPanel/VBox/PlayButton --property text --value '"Play"'
op "nodes"   node create --type Button --name QuitButton --parent-path MainPanel/VBox
op "button"  node set-property --path MainPanel/VBox/QuitButton --property text --value '"Quit"'
op "theme"   theme create --path res://assets/ui_theme.tres
op "theme"   theme set-color --theme-path res://assets/ui_theme.tres --class-name Label --name font_color --value '"#f0e68c"'
op "theme"   theme set-font-size --theme-path res://assets/ui_theme.tres --class-name Label --name font_size --value 24
op "theme"   theme set-stylebox-flat --theme-path res://assets/ui_theme.tres --class-name Panel --name panel \
               --bg-color '{"r":0.15,"g":0.17,"b":0.22,"a":1}' --corners '{"top_left":8,"top_right":8,"bottom_left":8,"bottom_right":8}'
op "theme"   theme set-stylebox-flat --theme-path res://assets/ui_theme.tres --class-name Button --name normal \
               --bg-color '{"r":0.25,"g":0.45,"b":0.75,"a":1}' --corners '{"top_left":6,"top_right":6,"bottom_left":6,"bottom_right":6}'
op "theme"   theme apply --node-path . --theme-path res://assets/ui_theme.tres
op "save"    scene save

echo "== 03_material_particle.tscn =="
op "scene"    scene create --path res://scenes/03_material_particle.tscn --root-type Node3D --root-name MaterialParticleDemo
op "camera"   camera create --parent-path . --name Camera3D --type 3d --make-current
op "camera"   node set-property --path Camera3D --property position --value '{"x":0,"y":2,"z":5}'
op "light"    node create --type DirectionalLight3D --name Sun
op "light"    node set-property --path Sun --property rotation --value '{"x":-0.8,"y":0.4,"z":0}'
op "mesh"     node create --type MeshInstance3D --name DemoMesh
op "mesh"     node set-property --path DemoMesh --property mesh --value '{"__class__":"SphereMesh","radius":1.0,"height":2.0}'
op "material" material apply-to-node --node-path DemoMesh --save-to res://assets/demo_material.tres \
                --props '{"albedo_color":{"r":0.8,"g":0.3,"b":0.3,"a":1},"metallic":0.6,"roughness":0.3}'
op "particle" particle apply-preset --parent-path . --name SmokeParticles --preset smoke --type gpu_3d
op "particle" node set-property --path SmokeParticles --property position --value '{"x":0,"y":1.5,"z":0}'
op "save"     scene save

echo "== 04_animation.tscn =="
op "scene"  scene create --path res://scenes/04_animation.tscn --root-type Node2D --root-name AnimationDemo
op "nodes"  node create --type Polygon2D --name Blob
op "nodes"  node set-property --path Blob --property polygon --value '[{"x":-25,"y":-25},{"x":25,"y":-25},{"x":25,"y":25},{"x":-25,"y":25}]'
op "player" animation player-create --parent-path . --name AnimationPlayer
op "anim"   animation create --player-path AnimationPlayer --name bob --length 1.0 --loop-mode pingpong
op "track"  animation add-property-track --player-path AnimationPlayer --animation-name bob --track-path "Blob:position" \
              --keyframes '[{"time":0,"value":{"x":0,"y":0}},{"time":1,"value":{"x":0,"y":-50}}]'
op "anim"   animation create --player-path AnimationPlayer --name spin --length 2.0 --loop-mode linear
op "track"  animation add-property-track --player-path AnimationPlayer --animation-name spin --track-path "Blob:rotation" \
              --keyframes '[{"time":0,"value":0},{"time":2,"value":6.2831853}]'
op "auto"   animation set-autoplay --player-path AnimationPlayer --animation-name bob
op "save"   scene save

echo "== 05_tilemap_gridmap.tscn =="
op "scene"   scene create --path res://scenes/05_tilemap_gridmap.tscn --root-type Node --root-name TilemapGridmapDemo
op "tilemap" node create --type TileMapLayer --name Ground
op "tilemap" node set-property --path Ground --property tile_set --value '"res://assets/demo_tileset.tres"'
op "tilemap" tilemap set-cells-rect --path Ground --rect-x 0 --rect-y 0 --rect-w 8 --rect-h 4 --source-id 0 --atlas-col 0 --atlas-row 0
op "tilemap" tilemap set-cell --path Ground --map-x 2 --map-y 1 --source-id 0 --atlas-col 1 --atlas-row 0
op "gridmap" node create --type GridMap --name Blocks
op "gridmap" node set-property --path Blocks --property mesh_library --value '"res://assets/demo_meshlib.tres"'
op "gridmap" gridmap fill --path Blocks --item 0 --rect-x 0 --rect-y 0 --rect-z 0 --rect-w 4 --rect-h 1 --rect-d 4
op "save"    scene save

echo "== 06_physics_csg.tscn =="
op "scene"  scene create --path res://scenes/06_physics_csg.tscn --root-type Node3D --root-name PhysicsCsgDemo
op "floor"  node create --type StaticBody3D --name Floor
op "floor"  node create --type CollisionShape3D --name FloorShape --parent-path Floor
op "floor"  node set-property --path Floor/FloorShape --property shape --value '{"__class__":"BoxShape3D","size":{"x":10,"y":0.2,"z":10}}'
op "ball"   node create --type RigidBody3D --name Ball
op "ball"   node set-property --path Ball --property position --value '{"x":0,"y":2,"z":0}'
op "ball"   node create --type CollisionShape3D --name BallShape --parent-path Ball
op "ball"   node set-property --path Ball/BallShape --property shape --value '{"__class__":"SphereShape3D","radius":0.5}'
op "csg"    csg create --parent-path . --shape box --name CsgBox
op "csg"    node set-property --path CsgBox --property position --value '{"x":3,"y":0.5,"z":0}'
op "camera" camera create --parent-path . --name Camera3D --type 3d --make-current
op "camera" node set-property --path Camera3D --property position --value '{"x":0,"y":4,"z":8}'
op "camera" node set-property --path Camera3D --property rotation --value '{"x":-0.45,"y":0,"z":0}'
op "light"  node create --type DirectionalLight3D --name Sun
op "light"  node set-property --path Sun --property rotation --value '{"x":-0.8,"y":0.4,"z":0}'
op "save"   scene save

echo "== 07_game_input.tscn =="
for action in demo_move_left:A demo_move_right:D demo_move_up:W demo_move_down:S; do
  name="${action%%:*}"; key="${action##*:}"
  op "input" input-map ensure-action --action "$name"
  op "input" input-map ensure-binding --action "$name" --event-type key --keycode "$key"
done
op "scene"  scene create --path res://scenes/07_game_input.tscn --root-type Node2D --root-name GameInputDemo
op "player" node create --type CharacterBody2D --name Player
op "script" script attach --path Player --script-path res://scripts/game_input_player.gd
op "shape"  node create --type CollisionShape2D --name Shape --parent-path Player
op "shape"  node set-property --path Player/Shape --property shape --value '{"__class__":"RectangleShape2D","size":{"x":32,"y":32}}'
op "visual" node create --type Polygon2D --name Body --parent-path Player
op "visual" node set-property --path Player/Body --property polygon --value '[{"x":-16,"y":-16},{"x":16,"y":-16},{"x":16,"y":16},{"x":-16,"y":16}]'
op "visual" node set-property --path Player/Body --property color --value '{"r":0.4,"g":0.7,"b":1.0,"a":1}'
op "camera" camera create --parent-path Player --name Camera2D --type 2d --make-current
op "save"   scene save

echo "== 08_scripting.tscn =="
op "scene"  scene create --path res://scenes/08_scripting.tscn --root-type Node --root-name ScriptingDemo
op "nodes"  node create --type Node --name Target
op "script" script attach --path Target --script-path res://scripts/scripting_target.gd
op "save"   scene save

echo
echo "== summary: PASS=$PASS FAIL=$FAIL =="
[ "$FAIL" -eq 0 ]
