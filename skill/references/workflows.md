# godot-ai-cli canonical workflows

Eight recipes covering the common tasks. All commands assume the editor is already up via `launch` (see SKILL.md). launch records the daemon's ports, so later commands find the daemon without repeating `--http-port` — pass it only to target a DIFFERENT daemon than the last one launched (e.g. parallel CI jobs). Node paths below use the canonical scene-root-absolute form (`/Root/Child`); see "Path and param conventions" in references/commands.md. JSON flags are shown with bash single quotes — on PowerShell single quotes work too; in cmd.exe use double quotes and escape inner ones, or write payloads to files.

## 1. Build a scene from scratch

```bash
godot-ai-cli scene create --path res://levels/level1.tscn --root-type Node3D --root-name Level1
# scene create already made the new scene the edited scene — this open is a
# no-op returning "settle":"already_current" (kept only to show the verb):
godot-ai-cli scene open --path res://levels/level1.tscn
godot-ai-cli node create --type CharacterBody3D --name Player --parent-path /Level1
godot-ai-cli node create --type Camera3D --name MainCamera --parent-path /Level1
godot-ai-cli node set-property --path /Level1/Player --property position --value '{"x":0,"y":1,"z":0}'
godot-ai-cli script create --path res://scripts/player.gd --content 'extends CharacterBody3D
# ...'
godot-ai-cli script attach --path /Level1/Player --script-path res://scripts/player.gd
godot-ai-cli node create --type Area3D --name Hazard --parent-path /Level1
godot-ai-cli signal connect --path /Level1/Hazard --signal body_entered --target /Level1 --method _on_body_entered
godot-ai-cli scene save
```

Verify: `scene get-hierarchy --depth 5`, `node get-properties --path /Level1/Player`, `logs read --source editor --count 20`.

Notes: `node create --scene-path res://foo.tscn` instantiates a packed scene instead of a bare class. `--scene-file` on mutations guards against editing the wrong scene. Building several nodes at once → put the `create_node`/`set_property` plugin commands into `batch execute --file ops.json` for atomic apply with rollback. `script create --content` is fine for a one-liner, but any multi-line or non-ASCII source belongs in a file: write it as UTF-8 and pass `--content-file <path>` — a leading BOM is stripped, `--content` + `--content-file` together is `INVALID_PARAMS`, and only PowerShell 5.1 makes this mandatory (it strips embedded quotes from `--content` and re-encodes the rest through the console code page). The response's `source_bytes` / `source_sha256` prove the bytes that reached the plugin.

## 2. UI + theme

```bash
godot-ai-cli scene create --path res://ui/main_menu.tscn --root-type Control --root-name MainMenu
# (no scene open needed — create already switched the edited scene)
# Declarative layout tree in one call — keys: type (required), name, properties,
# anchor_preset, anchor_margin, theme, children (see ui build-layout -h):
godot-ai-cli ui build-layout --parent-path /MainMenu --tree '{"type":"VBoxContainer","name":"Menu","children":[
  {"type":"Label","name":"Title","properties":{"text":"My Game"}},
  {"type":"Button","name":"StartButton","properties":{"text":"Start"}}]}'
godot-ai-cli ui set-anchor-preset --path /MainMenu/Menu --preset center --resize-mode minsize
godot-ai-cli ui set-text --path /MainMenu/Menu/StartButton --text "Start Game"   # update one control's text
# Theme as a reusable resource:
godot-ai-cli theme create --path res://ui/main_theme.tres
godot-ai-cli theme set-color --theme-path res://ui/main_theme.tres --class-name Button --name font_color --value '{"r":1,"g":1,"b":1,"a":1}'
godot-ai-cli theme set-font-size --theme-path res://ui/main_theme.tres --class-name Label --name font_size --value 32
godot-ai-cli theme set-stylebox-flat --theme-path res://ui/main_theme.tres --class-name Button --name normal --bg-color '{"r":0.2,"g":0.2,"b":0.3,"a":1}' --corners '{"top_left":8,"top_right":8,"bottom_left":8,"bottom_right":8}'
godot-ai-cli theme apply --node-path /MainMenu --theme-path res://ui/main_theme.tres
# Per-node override (no theme resource involved). --fresh starts from an EMPTY
# StyleBoxFlat, so nothing the patch does not mention is inherited:
godot-ai-cli theme stylebox-override --path /MainMenu/Menu/StartButton --slot normal --patch '{"bg_color":{"r":0.2,"g":0.2,"b":0.3,"a":1}}' --fresh
godot-ai-cli scene save
```

Verify: `node get-children --path /MainMenu`, and visually with `editor screenshot --source viewport_2d` (windowed editor only). `theme stylebox-override` patches the node's RESOLVED stylebox for a slot, so a `corner_radius_*` / `content_margin_*` the patch omits is inherited from the theme silently — pass `--fresh` for pixel-art UI that must not inherit them (it starts from an empty `StyleBoxFlat` and stops consulting the theme, so an unknown slot is not an error either). `overrode_existing` in the response is about the node, not about the theme base.

## 3. Animation

```bash
godot-ai-cli animation player-create --parent-path /Level1 --name AnimationPlayer
# Compact tween-list form for simple clips:
godot-ai-cli animation create-simple --player-path /Level1/AnimationPlayer --name spawn --length 0.5 \
  --tweens '[{"target":"Player","property":"scale","from":{"x":0,"y":0,"z":0},"to":{"x":1,"y":1,"z":1},"duration":0.5}]'
# Or explicit property tracks for full control:
godot-ai-cli animation create --player-path /Level1/AnimationPlayer --name idle --length 1.0 --loop-mode loop
godot-ai-cli animation add-property-track --player-path /Level1/AnimationPlayer --animation-name idle \
  --track-path 'Player:position:y' --keyframes '[{"time":0,"value":1.0},{"time":0.5,"value":1.2},{"time":1.0,"value":1.0}]'
godot-ai-cli animation validate --player-path /Level1/AnimationPlayer --animation-name idle
godot-ai-cli animation play --player-path /Level1/AnimationPlayer --animation-name idle
```

Presets exist for common motions: `animation preset-fade|preset-pulse|preset-shake|preset-slide`. Their `--target-path` takes the same scene-root-absolute node path as every other op (slide/shake/pulse accept Control, Node2D, or Node3D targets; fade needs a `modulate` property — Control/Node2D/Sprite3D only):

```bash
godot-ai-cli animation preset-shake --player-path /Level1/AnimationPlayer --target-path /Level1/Player --duration 0.3
```

(`--track-path` and tween `target` keys are different: they are animation-track paths relative to the AnimationPlayer's root_node, e.g. `Player:position:y` or `Player`.) Verify structure with `animation list --player-path ...` and `animation get`.

## 4. Run the GDScript test suites

Scene-dependent suites read nodes under the project's main scene (upstream design: the plugin's runner annotates results with `edited_scene` and a `scene_warning` when it differs from `application/run/main_scene`). Open the main scene BEFORE running, or dozens of suites report phantom failures:

```bash
godot-ai-cli project settings-get --key application/run/main_scene    # discover the main scene
godot-ai-cli scene open --path res://main.tscn                        # substitute your main scene
godot-ai-cli test run                                                  # budget: 300s — set a long shell timeout
godot-ai-cli test run --suite test_animation --test-name test_fade    # narrow when iterating
godot-ai-cli test results-get                                          # re-read the last run
```

Read the summary fields: `suite_count`, `total`, `passed`, `failed`, `skipped`, `assertions` (Σ per-test `assertion_count`, no `--verbose` needed), `duration_ms`, `load_errors`, `edited_scene`, `scene_warning`, `game_status`, `play_state_warning`. Note `load_errors`, `scene_warning` and `play_state_warning` are OMITTED from the payload when empty — absence means "none", not "forgot to check" (`edited_scene` and `game_status` are always present, `""`/`active:false` when idle). `game_status.active:true` means a game run is live in this session: suites that assert the no-game precondition (editor/project/manual_play_adoption) fail deterministically — `project stop` and re-run, or pass `--require-idle-session` to refuse up front with `EDITOR_NOT_READY/EDITOR_PLAYING`. Suites are discovered from `res://tests/` (McpTestSuite subclasses). A run aborted mid-way returns `TEST_RUN_TIMEOUT` with partial results retrievable via `test results-get`.

## 5. Run the game and drive it with input

```bash
godot-ai-cli project run --mode main          # autosaves in-memory edits by default
godot-ai-cli game get-scene-tree --depth 4    # the RUNNING game's tree, not the edited scene
godot-ai-cli game get-ui-elements
godot-ai-cli game input-action --action move_right --pressed true    # project input actions
godot-ai-cli game input-key --key Space                              # raw keys
godot-ai-cli game ui-activate --node-path /MainMenu/Menu/StartButton  # press a Button directly (emits its pressed signal)
godot-ai-cli game input-sequence --steps '[{"at_frame":0,"action":"move_right","pressed":true},{"at_frame":30,"action":"move_right","pressed":false}]' --settle-frames 10
godot-ai-cli game input-state               # what is currently pressed
godot-ai-cli editor eval --code 'print(get_tree().current_scene.name)'   # GDScript inside the running game
godot-ai-cli project stop
```

`game input-sequence` drives the game one frame per step (30s budget). `game input-action` only reaches `Input.is_action_*` consumers — it synthesizes no InputEvent, so GUI Controls never see it (`sent:true` is not a click). To drive UI use `game input-key` (a real key event the focused Control consumes as `ui_accept`), `game input-mouse` (a real click at canvas coordinates), or `game ui-activate --node-path <Control>` to emit a specific Button's `pressed` signal directly (`toggled` too when the button is a toggle); omit `--node-path` to activate the current GUI focus owner. `ui-activate` refuses with a structured `activated:false` + `reason` (`not_found` / `not_a_control` / `no_focus_owner` / `disabled` / `not_visible` / `unsupported_control`) instead of pretending to click, and it works on a paused dialog: the signal is emitted directly, so a handler behind `PROCESS_MODE_WHEN_PAUSED` still runs. A Control that is not a BaseButton (a Slider, a LineEdit) still needs `input-key` / `input-mouse`. `editor eval` failures are attributed: `EVAL_COMPILE_ERROR` / `EVAL_RUNTIME_ERROR` / `EVAL_GAME_NOT_READY` / `EVAL_HUNG` / `EVAL_RESULT_TOO_LARGE` — see references/troubleshooting.md.

## 6. Windowed screenshot verification loop

Requires a headed editor (`launch` without `--headless`). Loop after visual mutations:

```bash
godot-ai-cli editor screenshot                                  # 3D viewport, longest edge 640px
godot-ai-cli editor screenshot --source viewport_2d             # 2D viewport
godot-ai-cli editor screenshot --source cinematic --view-target /Level1/MainCamera --coverage
godot-ai-cli editor screenshot --source game                    # framebuffer of the RUNNING game
```

The response embeds the image by default (`--include-image true`, `--max-resolution 640`). An empty capture comes back as `EDITOR_NOT_READY` with `sub_code: EDITOR_VIEWPORT_EMPTY`, `retryable: false` — that is the headless signature; do not retry-loop it, relaunch windowed. A 3D-source screenshot against a 2D-rooted scene fails with `EDITOR_VIEWPORT_NOT_3D`.

## 7. Headless CI session

Headless is for build/verify/test automation that never looks at pixels:

```bash
godot-ai-cli launch --project . --headless --wait 90 --http-port 18100 --ws-port 19600
# launch recorded the ports, so later commands find this daemon without --http-port:
godot-ai-cli scene open --path res://main.tscn
godot-ai-cli test run
godot-ai-cli scene get-hierarchy
godot-ai-cli logs read --source all --count 100
godot-ai-cli stop
```

Caveats: screenshots always fail (`EDITOR_VIEWPORT_EMPTY`); newly written scripts need `filesystem scan` (or `filesystem reimport` for assets) before the editor sees them — the script ops' responses say so via diagnostics; `--wait 90`+ because cold editor startup plus import on CI is slow; custom ports keep a CI job from colliding with a developer session on 8000/9500. Two port-sharing rules for one machine: all jobs share a single last-daemon.json, so parallel jobs must keep passing `--http-port <N>` explicitly on every command (the explicit flag always wins over the record); and only one custom-port OVERRIDE SET may be live at a time per machine — the port override lives in the shared global EditorSettings, so an overlapping launch on DIFFERENT ports fails fast with `SETTINGS_OVERRIDE_ACTIVE` (several jobs may still share one custom-port daemon; jobs on different ports must run sequentially or be isolated per OS user/runner).

## 8. Catch collateral file rewrites

Saving is never a single-file write: the editor re-serializes the scene AND every resource it holds, which is how hand-written comments disappear from an unrelated `.tres` and why a diff-scoped review cannot see the damage. Two channels answer "what did that write actually touch", neither needs a daemon:

```bash
# 1) Per-call receipts — a save names exactly what IT wrote:
godot-ai-cli scene save
#   → {"path":"res://levels/level1.tscn","written_paths":["res://levels/level1.tscn"],
#      "resaved_resources":["res://ui/main_theme.tres"],...}
#   project run reports the same pair for its autosave window (--autosave, default true).

# 2) Whole-project diff around any batch of ops (local, no editor):
godot-ai-cli filesystem snapshot --tag before-batch --project .
#   ... the mutation batch (scene save, node set-property, resource set-property, ...)
godot-ai-cli filesystem changed --tag before-batch --project .
#   → {"changed":[{"path":"res://ui/main_theme.tres","status":"modified",
#                  "sha_before":"...","sha_after":"..."}],"added":[],"removed":[],
#      "scanned":N,"ignored_count":M,...}
```

`written_paths` are the files the call wrote on purpose; anything in `resaved_resources` is collateral — review it before committing. Both lists are size+mtime based (second-granular mtimes) and PARTIAL when `write_track_truncated:true` is present, so use the snapshot diff when you need a provable answer: `filesystem changed` decides on content alone (a file rewritten with identical bytes is not reported even though its mtime moved), needs no git (untracked and git-ignored files are covered), and counts its walk exclusions in `ignored_count` (`.godot/` and `.git/` at any depth, plus root-level `.mono/` `bin/` `obj/` `.vs/` `.idea/`). Snapshots live in `<project>/.godot/godot_ai_snapshots/<tag>.json`; `--tag` takes one snapshot name per baseline, and `filesystem changed` writes nothing.
