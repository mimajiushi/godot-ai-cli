# godot-ai-cli op catalog

Generated from `godot-ai-cli commands --json` (148 ops). Regenerate against a newer binary with:

```bash
godot-ai-cli commands --json --pretty
```

## Path and param conventions

- **Node paths are scene-root-absolute**: `/Root/Child` — e.g. `--path /Level1/Player` in a scene whose root is named `Level1`. Responses emit this same `/Root/...` form (`path`, `parent_path`, …). A bare `Level1/Player` (no leading slash, root name first) does NOT resolve — it reads as "child `Level1` of the root" and fails with `NODE_NOT_FOUND`.
- **`""` (empty string) means the scene root** for parent-style params: `node create` with no `--parent-path` (or `--parent-path ""`) parents to the root. To name the root itself as a target use `/Root` (e.g. `/Level1`).
- **Resources always use `res://` project paths**: scenes, scripts, materials, themes, textures (`--path res://ui/main.tscn`).
- **`--params '<json>'` merge semantics**: the JSON object is the base of the wire params; explicit typed flags override colliding keys. Optional flags at their zero value stay off the wire unless passed via `--params`.
- **`scene create` switches the edited scene immediately** — the new scene is already open when the call returns; a following `scene open` of the same path is a no-op answering `"settle":"already_current"`.
- **`material get` reads SAVED resource files only**: `--path` must be an on-disk `.tres` / `.material` / `.res`. In-memory or node-attached materials are not readable through it — inspect those via the saved `.tscn` or `node get-properties --path /Root/Node`.
- **Mutation responses may carry a `reason` field** (e.g. `"reason":"File save cannot be undone via editor undo"` on `scene save`): it explains the accompanying `"undoable":false` — informational, not an error.

Conventions applying to every op:

- Output is one JSON object on stdout; exit 0 on success. Errors are `{"status":"error","error":{"code","message","data"}}` with exit 1. Add `--pretty` before the subcommand for indented JSON.
- Every op also accepts `--session <id>` (pin to one connected editor when several are attached) and `--params '<json>'` (base wire params; explicit flags override colliding keys).
- Optional flags left at their zero value are omitted from the wire params.
- `[write]` ops are gated on editor writability: while the editor is importing or playing they fail with `EDITOR_NOT_READY` (see references/troubleshooting.md).
- Timeouts are the daemon-side per-op budget. Long ops: `test run` 300s, `editor screenshot` 30s, `filesystem scan` 30s, `batch execute` 30s, `game input-sequence` 30s.
- Daemon-level flags (`--http-port`) are accepted by every op command. Port resolution: explicit `--http-port` > port recorded by the last `launch`/`serve` (`last-daemon.json` in the user cache dir) > default 8000, with the default retried when the recorded port is unreachable. So after a custom-port launch you can omit `--http-port` entirely.
- CLI-side extras not in the wire params: `batch execute` also accepts `--file <path>` (a JSON file holding the commands array).

Non-op leaves (not in this catalog): `session list` / `session activate` (daemon-side), `custom list` / `custom invoke` (third-party editor tools), `call <plugin_command>` (escape hatch), plus `launch` / `stop` / `status` / `serve` / `godot detect` / `godot use` / `plugin install` / `update` / `version` / `commands`.

## editor (8 ops)

### `editor eval` — Evaluate GDScript code inside the running game
`game_eval` · 15s · --code string (required)

### `editor monitors` — Read Godot performance monitor values
`get_performance_monitors` · 8s · --monitors json

### `editor quit` — Ask the connected editor to quit gracefully
`quit_editor` · 8s · no flags

### `editor reload-plugin` — Reload the godot_ai plugin and wait for reconnect
`reload_plugin` · 8s · no flags

### `editor screenshot` — Capture the editor viewport (3D/2D), a cinematic Camera3D render, or the game framebuffer
`take_screenshot` · 30s · --source string (default "viewport"), --max-resolution int (default "640"), --include-image bool (default "true"), --view-target string, --coverage bool (default "false"), --elevation float, --azimuth float, --fov float, --user-prompt string

### `editor selection-get` — List the currently selected editor nodes
`get_selection` · 8s · no flags

### `editor selection-set` — Select nodes in the editor by scene path
`set_selection` · 8s · **[write]** · --paths json (required)

### `editor state` — Show editor version, project, current scene, readiness, and play state
`get_editor_state` · 8s · no flags

## scene (6 ops)

### `scene create` — Create a new scene file with a given root node type
`create_scene` · 8s · **[write]** · --path string (required), --root-type string (default "Node3D"), --root-name string

### `scene get-hierarchy` — Paginated scene tree walk of the edited scene
`get_scene_tree` · 8s · --depth int (default "10"), --offset int (default "0"), --limit int (default "100")

### `scene get-roots` — List the scenes currently open in editor tabs
`get_open_scenes` · 8s · no flags

### `scene open` — Open a scene in the editor
`open_scene` · 8s · **[write]** · --path string (required), --force-reload bool (default "false")

### `scene save` — Save the currently edited scene
`save_scene` · 8s · **[write]** · no flags

### `scene save-as` — Save the current scene to a new path
`save_scene_as` · 8s · **[write]** · --path string (required)

## node (13 ops)

### `node add-to-group` — Add a node to a group
`add_to_group` · 8s · **[write]** · --path string (required), --group string (required), --scene-file string

### `node create` — Create a node in the edited scene
`create_node` · 8s · **[write]** · --type string, --name string, --parent-path string, --scene-path string, --scene-file string

### `node delete` — Delete a node from the edited scene
`delete_node` · 8s · **[write]** · --path string (required), --scene-file string

### `node duplicate` — Duplicate a node (and its subtree)
`duplicate_node` · 8s · **[write]** · --path string (required), --name string, --scene-file string

### `node find` — Find nodes by name, type, and/or group
`find_nodes` · 8s · --name string, --type string, --group string, --offset int (default "0"), --limit int (default "100")

### `node get-children` — List a node's direct children (name, type, path)
`get_children` · 8s · --path string (required)

### `node get-groups` — List a node's group memberships
`get_groups` · 8s · --path string (required)

### `node get-properties` — Full property snapshot of a node
`get_node_properties` · 8s · --path string (required), --fields json

### `node move` — Move a node to a different index among its siblings
`move_node` · 8s · **[write]** · --path string (required), --index int (required), --scene-file string

### `node remove-from-group` — Remove a node from a group
`remove_from_group` · 8s · **[write]** · --path string (required), --group string (required), --scene-file string

### `node rename` — Rename a node
`rename_node` · 8s · **[write]** · --path string (required), --new-name string (required), --scene-file string

### `node reparent` — Move a node under a new parent
`reparent_node` · 8s · **[write]** · --path string (required), --new-parent string (required), --scene-file string

### `node set-property` — Set one property on a node
`set_property` · 8s · **[write]** · --path string (required), --property string (required), --value json (required), --scene-file string

## script (6 ops)

### `script attach` — Attach a script to a node
`attach_script` · 8s · **[write]** · --path string (required), --script-path string (required)

### `script create` — Create a GDScript file (response includes per-write diagnostics)
`create_script` · 8s · **[write]** · --path string (required), --content string

### `script detach` — Detach the script from a node
`detach_script` · 8s · **[write]** · --path string (required)

### `script find-symbols` — List the symbols (functions, vars, signals) of a script
`find_symbols` · 8s · --path string (required)

### `script patch` — Anchor-edit a GDScript file (old_text → new_text)
`patch_script` · 8s · **[write]** · --path string (required), --old-text string (required), --new-text string (required), --replace-all bool (default "false")

### `script read` — Read a GDScript source file
`read_script` · 8s · --path string (required)

## project (5 ops)

### `project run` — Play the project and wait briefly for game liveness
`run_project` · 8s · **[write]** · --mode string (default "main"), --scene string, --autosave bool (default "true")

### `project set-main-scene` — Set the project main scene (application/run/main_scene)
`set_main_scene` · 8s · **[write]** · --path string (required)

### `project settings-get` — Read one project setting by key
`get_project_setting` · 8s · --key string (required)

### `project settings-set` — Write one project setting
`set_project_setting` · 8s · **[write]** · --key string (required), --value json (required)

### `project stop` — Stop the running game
`stop_project` · 8s · no flags

## test (2 ops)

### `test results-get` — Read the most recent test run's results
`get_test_results` · 8s · --verbose bool (default "false")

### `test run` — Run GDScript test suites in the editor
`run_tests` · 300s · --suite string, --test-name string, --exclude-test-name string, --verbose bool (default "false")

## animation (16 ops)

### `animation add-method-track` — Add a method-call track to an animation
`animation_add_method_track` · 8s · **[write]** · --player-path string (required), --animation-name string (required), --target-node-path string (required), --keyframes json (required)

### `animation add-property-track` — Add a property track with keyframes to an animation
`animation_add_property_track` · 8s · **[write]** · --player-path string (required), --animation-name string (required), --track-path string (required), --keyframes json (required), --interpolation string (default "linear")

### `animation create` — Create an Animation clip (auto-creates player + library if missing)
`animation_create` · 8s · **[write]** · --player-path string (required), --name string (required), --length float (required), --loop-mode string (default "none"), --overwrite bool (default "false")

### `animation create-simple` — Create an animation from a compact tween list
`animation_create_simple` · 8s · **[write]** · --player-path string (required), --name string (required), --tweens json (required), --length float, --loop-mode string (default "none"), --overwrite bool (default "false")

### `animation delete` — Delete an animation
`animation_delete` · 8s · **[write]** · --player-path string (required), --animation-name string (required)

### `animation get` — Read one animation's tracks and settings
`animation_get` · 8s · --player-path string (required), --animation-name string (required)

### `animation list` — List an AnimationPlayer's animations
`animation_list` · 8s · --player-path string (required)

### `animation play` — Play an animation in the editor
`animation_play` · 8s · --player-path string (required), --animation-name string

### `animation player-create` — Create an AnimationPlayer node
`animation_player_create` · 8s · **[write]** · --parent-path string (required), --name string (default "AnimationPlayer")

### `animation preset-fade` — Create a fade in/out animation preset
`animation_preset_fade` · 8s · **[write]** · --player-path string (required), --target-path string (required), --mode string (default "in"), --duration float (default "0.5"), --animation-name string, --overwrite bool (default "false")

### `animation preset-pulse` — Create a scale-pulse animation preset
`animation_preset_pulse` · 8s · **[write]** · --player-path string (required), --target-path string (required), --from-scale float (default "1.0"), --to-scale float (default "1.1"), --duration float (default "0.4"), --animation-name string, --overwrite bool (default "false")

### `animation preset-shake` — Create a shake animation preset
`animation_preset_shake` · 8s · **[write]** · --player-path string (required), --target-path string (required), --intensity float, --duration float (default "0.3"), --frequency float (default "30.0"), --seed int (default "0"), --animation-name string, --overwrite bool (default "false")

### `animation preset-slide` — Create a slide in/out animation preset
`animation_preset_slide` · 8s · **[write]** · --player-path string (required), --target-path string (required), --direction string (default "left"), --mode string (default "in"), --distance float, --duration float (default "0.4"), --animation-name string, --overwrite bool (default "false")

### `animation set-autoplay` — Set an AnimationPlayer's autoplay animation
`animation_set_autoplay` · 8s · **[write]** · --player-path string (required), --animation-name string

### `animation stop` — Stop an AnimationPlayer
`animation_stop` · 8s · --player-path string (required)

### `animation validate` — Validate an animation's tracks against the scene
`animation_validate` · 8s · --player-path string (required), --animation-name string (required)

## material (8 ops)

### `material apply-preset` — Apply a named material preset
`material_apply_preset` · 8s · **[write]** · --preset string (required), --path string, --node-path string, --overrides json

### `material apply-to-node` — Create and apply a material to a node in one step
`material_apply_to_node` · 8s · **[write]** · --node-path string (required), --type string (default "standard"), --props json, --slot string (default "override"), --save-to string, --overwrite bool (default "false")

### `material assign` — Assign a material resource to a node's material slot
`material_assign` · 8s · **[write]** · --node-path string (required), --resource-path string, --slot string (default "override"), --create-if-missing bool (default "false"), --type string (default "standard")

### `material create` — Create a material resource file
`material_create` · 8s · **[write]** · --path string (required), --type string (default "standard"), --shader-path string, --overwrite bool (default "false")

### `material get` — Read a material's properties
`material_get` · 8s · --path string (required)

### `material list` — List material resources under res://
`material_list` · 8s · --root string (default "res://"), --type string

### `material set-param` — Set a standard material property
`material_set_param` · 8s · **[write]** · --path string (required), --param string (required), --value json (required)

### `material set-shader-param` — Set a shader uniform on a ShaderMaterial
`material_set_shader_param` · 8s · **[write]** · --path string (required), --param string (required), --value json (required)

## audio (6 ops)

### `audio list` — List audio streams under res://
`audio_list` · 8s · --root string (default "res://"), --include-duration bool (default "true")

### `audio play` — Start playback on an audio player
`audio_play` · 8s · --player-path string (required), --from-position float (default "0.0")

### `audio player-create` — Create an AudioStreamPlayer node
`audio_player_create` · 8s · **[write]** · --parent-path string (required), --name string (default "AudioStreamPlayer"), --type string (default "1d")

### `audio player-set-playback` — Configure playback settings (volume, pitch, autoplay, bus)
`audio_player_set_playback` · 8s · **[write]** · --player-path string (required), --volume-db float, --pitch-scale float, --autoplay bool, --bus string

### `audio player-set-stream` — Assign an audio stream to a player
`audio_player_set_stream` · 8s · **[write]** · --player-path string (required), --stream-path string (required)

### `audio stop` — Stop playback on an audio player
`audio_stop` · 8s · --player-path string (required)

## particle (7 ops)

### `particle apply-preset` — Create particles from a named preset (fire, smoke, rain, ...)
`particle_apply_preset` · 8s · **[write]** · --parent-path string (required), --name string (required), --preset string (required), --type string (default "gpu_3d"), --overrides json

### `particle create` — Create a particles node
`particle_create` · 8s · **[write]** · --parent-path string (required), --name string (default "Particles"), --type string (default "gpu_3d")

### `particle get` — Read a particles node's configuration
`particle_get` · 8s · --node-path string (required)

### `particle restart` — Restart particle emission
`particle_restart` · 8s · --node-path string (required)

### `particle set-draw-pass` — Configure a draw pass (mesh, texture, material)
`particle_set_draw_pass` · 8s · **[write]** · --node-path string (required), --pass int (default "1"), --mesh string, --texture string, --material string

### `particle set-main` — Set main particle node properties (amount, lifetime, ...)
`particle_set_main` · 8s · **[write]** · --node-path string (required), --properties json (required)

### `particle set-process` — Set process material properties (gravity, velocity, ...)
`particle_set_process` · 8s · **[write]** · --node-path string (required), --properties json (required)

## camera (8 ops)

### `camera apply-preset` — Create a camera from a named preset (side_scroll, top_down, ...)
`camera_apply_preset` · 8s · **[write]** · --parent-path string (required), --name string (required), --preset string (required), --type string, --make-current bool (default "true"), --overrides json

### `camera configure` — Set arbitrary camera properties
`camera_configure` · 8s · **[write]** · --camera-path string (required), --properties json (required)

### `camera create` — Create a camera node
`camera_create` · 8s · **[write]** · --parent-path string (required), --name string (default "Camera"), --type string (default "2d"), --make-current bool (default "false")

### `camera follow-2d` — Make a Camera2D follow a target node
`camera_follow_2d` · 8s · **[write]** · --camera-path string (required), --target-path string (required), --smoothing-speed float (default "5.0"), --zero-transform bool (default "true")

### `camera get` — Read a camera's configuration (default: the active camera)
`camera_get` · 8s · --camera-path string

### `camera list` — List cameras in the edited scene
`camera_list` · 8s · no flags

### `camera set-damping-2d` — Set Camera2D follow damping and drag margins
`camera_set_damping_2d` · 8s · **[write]** · --camera-path string (required), --position-speed float, --rotation-speed float, --drag-margins json, --drag-horizontal-enabled bool, --drag-vertical-enabled bool

### `camera set-limits-2d` — Set Camera2D scroll limits
`camera_set_limits_2d` · 8s · **[write]** · --camera-path string (required), --left int, --right int, --top int, --bottom int, --smoothed bool

## signal (3 ops)

### `signal connect` — Connect a signal to a target method
`connect_signal` · 8s · **[write]** · --path string (required), --signal string (required), --target string (required), --method string (required)

### `signal disconnect` — Disconnect a signal from a target method
`disconnect_signal` · 8s · **[write]** · --path string (required), --signal string (required), --target string (required), --method string (required)

### `signal list` — List a node's signals
`list_signals` · 8s · --path string (required), --include-editor bool (default "false")

## input-map (6 ops)

### `input-map add-action` — Add an input action (fails if it exists)
`add_action` · 8s · **[write]** · --action string (required), --deadzone float (default "0.5")

### `input-map bind-event` — Bind an input event to an action (fails on duplicates)
`bind_event` · 8s · **[write]** · --action string (required), --event-type string (required), --keycode string, --button int, --axis int, --axis-value float, --ctrl bool, --alt bool, --shift bool

### `input-map ensure-action` — Add an input action if missing (idempotent)
`ensure_action` · 8s · **[write]** · --action string (required), --deadzone float (default "0.5")

### `input-map ensure-binding` — Bind an input event if not already bound (idempotent)
`ensure_binding` · 8s · **[write]** · --action string (required), --event-type string (required), --deadzone float (default "0.5"), --keycode string, --button int, --axis int, --axis-value float, --ctrl bool, --alt bool, --shift bool

### `input-map list` — List project input actions and their bindings
`list_actions` · 8s · --include-builtin bool (default "false")

### `input-map remove-action` — Remove an input action and its bindings
`remove_action` · 8s · **[write]** · --action string (required)

## game (9 ops)

### `game get-node-info` — Property snapshot of a node in the running game
`game_command` · 15s · --path string (required), --include-properties bool (default "true")

### `game get-scene-tree` — Scene tree of the running game
`game_command` · 15s · --depth int (default "10"), --root-path string

### `game get-ui-elements` — UI element tree of the running game
`game_command` · 15s · --root-path string, --include-hidden bool (default "false"), --include-disabled bool (default "true"), --max-depth int (default "10")

### `game input-action` — Press/release an input action in the running game
`game_command` · 15s · --action string (required), --pressed bool (default "true"), --strength float (default "1.0")

### `game input-gamepad` — Send a gamepad event to the running game
`game_command` · 15s · --device int (default "0"), --control string (default "button"), --index int (default "0"), --pressed bool (default "true"), --value float (default "0.0")

### `game input-key` — Send a key press/release to the running game
`game_command` · 15s · --key string (required), --pressed bool (default "true"), --echo bool (default "false")

### `game input-mouse` — Send a mouse event to the running game
`game_command` · 15s · --event string (required), --position json, --button string (default "left"), --pressed bool (default "true")

### `game input-sequence` — Drive a frame-timed action timeline in the running game
`game_command` · 30s · --steps json (required), --settle-frames int (default "0")

### `game input-state` — Read currently pressed input actions in the running game
`game_command` · 15s · --actions json

## autoload (3 ops)

### `autoload add` — Register an autoload singleton
`add_autoload` · 8s · **[write]** · --name string (required), --path string (required), --singleton bool (default "true")

### `autoload list` — List the project's autoload singletons
`list_autoloads` · 8s · no flags

### `autoload remove` — Remove an autoload
`remove_autoload` · 8s · **[write]** · --name string (required)

## filesystem (5 ops)

### `filesystem read-text` — Read a project text file
`read_file` · 8s · --path string (required)

### `filesystem reimport` — Reimport assets (textures, models, audio — NOT scripts)
`reimport` · 8s · **[write]** · --paths json (required)

### `filesystem scan` — Scan the project filesystem and settle imports
`scan_filesystem` · 30s · no flags

### `filesystem search` — Search project files by name, type, and path prefix
`search_filesystem` · 8s · --name string, --type string, --path string, --offset int (default "0"), --limit int (default "100")

### `filesystem write-text` — Write a project text file
`write_file` · 8s · **[write]** · --path string (required), --content string

## theme (6 ops)

### `theme apply` — Apply a theme to a Control node (empty theme-path clears)
`apply_theme` · 8s · **[write]** · --node-path string (required), --theme-path string

### `theme create` — Create a Theme resource file
`create_theme` · 8s · **[write]** · --path string (required), --overwrite bool (default "false")

### `theme set-color` — Set a color item on a theme
`theme_set_color` · 8s · **[write]** · --theme-path string (required), --class-name string (required), --name string (required), --value json (required)

### `theme set-constant` — Set a constant item on a theme
`theme_set_constant` · 8s · **[write]** · --theme-path string (required), --class-name string (required), --name string (required), --value int (required)

### `theme set-font-size` — Set a font size item on a theme
`theme_set_font_size` · 8s · **[write]** · --theme-path string (required), --class-name string (required), --name string (required), --value int (required)

### `theme set-stylebox-flat` — Set a StyleBoxFlat item on a theme
`theme_set_stylebox_flat` · 8s · **[write]** · --theme-path string (required), --class-name string (required), --name string (required), --bg-color json, --border-color json, --border json, --corners json, --margins json, --shadow json, --anti-aliasing bool

## ui (4 ops)

### `ui build-layout` — Build a Control subtree from a declarative layout tree
`build_layout` · 8s · **[write]** · --tree json (required), --parent-path string

### `ui draw-recipe` — Apply a custom-draw recipe to a Control
`control_draw_recipe` · 8s · **[write]** · --path string (required), --ops json (required), --clear-existing bool (default "true")

### `ui set-anchor-preset` — Apply an anchor preset to a Control
`set_anchor_preset` · 8s · **[write]** · --path string (required), --preset string (required), --resize-mode string (default "minsize"), --margin int (default "0")

### `ui set-text` — Set the text of a Label/Button/RichTextLabel
`set_text` · 8s · **[write]** · --path string (required), --text string (required)

## resource (10 ops)

### `resource assign` — Assign a resource to a node's property
`assign_resource` · 8s · **[write]** · --path string (required), --property string (required), --resource-path string (required)

### `resource create` — Create a resource, optionally saved and/or assigned
`create_resource` · 8s · **[write]** · --type string (required), --properties json, --path string, --property string, --resource-path string, --overwrite bool (default "false")

### `resource curve-set-points` — Set the points of a Curve resource
`curve_set_points` · 8s · **[write]** · --points json (required), --path string, --property string, --resource-path string

### `resource environment-create` — Create an Environment resource from a preset
`environment_create` · 8s · **[write]** · --path string, --preset string (default "default"), --properties json, --sky json, --resource-path string, --overwrite bool (default "false")

### `resource get-info` — Inspect a resource class (properties and defaults)
`get_resource_info` · 8s · --type string (required)

### `resource gradient-texture-create` — Create a GradientTexture2D resource
`gradient_texture_create` · 8s · **[write]** · --stops json (required), --width int (default "256"), --height int (default "1"), --fill string (default "linear"), --path string, --property string, --resource-path string, --overwrite bool (default "false")

### `resource load` — Load a resource and read its properties
`load_resource` · 8s · --path string (required)

### `resource noise-texture-create` — Create a NoiseTexture2D resource
`noise_texture_create` · 8s · **[write]** · --noise-type string (default "simplex_smooth"), --width int (default "512"), --height int (default "512"), --frequency float (default "0.01"), --seed int (default "0"), --fractal-octaves int (default "0"), --path string, --property string, --resource-path string, --overwrite bool (default "false")

### `resource physics-shape-autofit` — Auto-fit a collision shape to a node's geometry
`physics_shape_autofit` · 8s · **[write]** · --path string (required), --source-path string, --shape-type string

### `resource search` — Search resources by type and path prefix
`search_resources` · 8s · --type string, --path string, --offset int (default "0"), --limit int (default "100")

## api (1 op)

### `api get-class` — Inspect ClassDB metadata for a Godot class (default: properties only)
`get_class_info` · 8s · --class-name string (required), --sections json, --include-inherited bool (default "false"), --include-inheritors bool (default "false"), --offset int (default "0"), --limit int (default "100")

## tilemap (4 ops)

### `tilemap clear` — Clear all cells of a TileMapLayer
`tilemap_clear` · 8s · **[write]** · --path string (required)

### `tilemap get-cells` — List the used cells of a TileMapLayer
`tilemap_get_cells` · 8s · --path string (required)

### `tilemap set-cell` — Set one tile cell
`tilemap_set_cell` · 8s · **[write]** · --path string (required), --source-id int (required), --atlas-col int (required), --atlas-row int (required), --map-x int (required), --map-y int (required)

### `tilemap set-cells-rect` — Fill a rectangle of cells with one tile
`tilemap_set_cells_rect` · 8s · **[write]** · --path string (required), --source-id int (required), --atlas-col int (required), --atlas-row int (required), --rect-x int (required), --rect-y int (required), --rect-w int (required), --rect-h int (required)

## tileset (2 ops)

### `tileset get-atlas-image` — Read a TileSet atlas image
`tileset_get_atlas_image` · 8s · --tileset-path string (required), --source-id int (required), --max-size int (default "0")

### `tileset get-atlas-tiles` — List the tiles of a TileSet atlas source
`tileset_get_atlas_tiles` · 8s · --tileset-path string (required), --source-id int (required)

## gridmap (5 ops)

### `gridmap clear` — Clear all cells of a GridMap
`gridmap_clear` · 8s · **[write]** · --path string (required)

### `gridmap fill` — Fill a box of GridMap cells with one item
`gridmap_fill` · 8s · **[write]** · --path string (required), --item int (required), --rect-x int (required), --rect-y int (required), --rect-z int (required), --rect-w int (required), --rect-h int (required), --rect-d int (required), --orientation int (default "0")

### `gridmap get-used-cells` — List the used cells of a GridMap
`gridmap_get_used_cells` · 8s · --path string (required)

### `gridmap list-library-items` — List the MeshLibrary items available to a GridMap
`gridmap_list_library_items` · 8s · --path string (required)

### `gridmap set-item` — Set one GridMap cell item
`gridmap_set_item` · 8s · **[write]** · --path string (required), --item int (required), --map-x int (required), --map-y int (required), --map-z int (required), --orientation int (default "0")

## csg (2 ops)

### `csg create` — Create a CSG shape node
`csg_create` · 8s · **[write]** · --parent-path string (required), --name string, --shape string (default "box"), --operation string (default "union")

### `csg set-operation` — Change a CSG node's boolean operation
`csg_set_operation` · 8s · **[write]** · --path string (required), --operation string (required)

## batch (1 op)

### `batch execute` — Run multiple plugin commands atomically (rollback on first error); use --file or --params
`batch_execute` · 30s · **[write]** · --commands json, --undo bool (default "true")

## logs (2 ops)

### `logs clear` — Clear the plugin log buffers
`clear_logs` · 8s · --clear-debugger-errors bool (default "false")

### `logs read` — Read plugin / game / editor / combined log buffers
`get_logs` · 8s · --count int (default "50"), --offset int (default "0"), --source string (default "plugin"), --since-run-id string, --since-cursor int, --include-details bool (default "false")

