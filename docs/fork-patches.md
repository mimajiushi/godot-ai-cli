# Fork patches vs upstream v3.2.5

`plugin/godot_ai/` is a fork of [hi-godot/godot-ai](https://github.com/hi-godot/godot-ai)
**v3.2.5** (MIT, "Godot AI contributors" — see `UPSTREAM-LICENSE.txt`). This
is the complete list of divergences. The behavioral patches (§2–§4) are
marked in the GDScript source with a `godot-ai-cli fork patch` comment —
`grep -rn "godot-ai-cli fork patch" plugin/godot_ai` audits them — and gated
behind `utils/fork_config.gd` so dormant upstream code keeps compiling and
upstream diffs stay reviewable. §1 is a whole-file rewrite (marked
"STRIPPED in the godot-ai-cli fork" instead) and §6 is comment-only
rewording, so neither shows up in that grep.

## 1. `telemetry.gd` — rewritten as no-ops

Upstream relays plugin events (dock startup, self-update outcome, plugin
reload, dev-server toggle) to the Python server via
`send_event("plugin_event", …)`. The fork removes reporting entirely: no
event is buffered, sent or persisted, and no data ever leaves the editor.
The original public interface is kept as no-ops so callers (`plugin.gd`,
`mcp_dock.gd`, handlers, `update_reload_runner.gd`) need no changes;
`_drain_editor_setting_dict` is unchanged because it only touches local
EditorSettings. Rationale: the fork's hard rule is *no telemetry, ever*.

## 2. `utils/server_lifecycle.gd` — godot-ai-cli daemon spawn (~line 968)

Upstream spawns its own Python MCP server when the port is free. The fork
instead prefers the godot-ai-cli Go daemon: the startup walk resolves the
binary via `utils/cli_daemon.gd` (see §11) and, when one is found, spawns
`godot-ai-cli serve --http-port <port> --ws-port <ws_port>` in place of the
Python server (`_spawn_cli_daemon`, with its own `godot-ai-cli fork patch`
comment). A plugin-spawned daemon is adopted by a later CLI launch
(`EnsureRunning` matches version/ws_port), so a manually opened editor
session stays reusable — a spawned Python server would collide with that
launch as `FOREIGN_SERVER`. Only when no godot-ai-cli binary can be located
does the walk fall back to the upstream Python spawn below (unchanged). The
adoption branch above the patch stays intact — a running godot-ai-cli daemon
is adopted exactly like an upstream compatible server.

## 3. `mcp_dock.gd` — UI changes and client-config gating

- **Self-update removed** (~line 782): `_update_manager = null`. Plugin
  updates ship with the godot-ai-cli release instead of an in-editor
  self-updater; the update banner stays hidden and every other
  `_update_manager` reference is already null-guarded upstream.
- **MCP client-configuration UI hidden** (~lines 846, 855, 3614): the
  clients refresh button, the "Clients & Tools" button and the
  drift-reconfigure banner never show — client configuration is a CLI/skill
  concern in the fork.
- **Remaining MCP client-config paths gated off** (~lines 591, 2189, 2518,
  3117, 3293): `_dispatch_client_action` early-returns before any
  `ClientConfigurator.configure`/`remove` worker can spawn (defense in
  depth); `_on_open_clients_window` is a no-op; the "Configure an AI
  client ->" CTA is force-hidden; and both background probe paths —
  `_perform_initial_client_status_refresh` and the focus-in refresh via
  `_should_refresh_client_statuses_on_focus_in` — skip their per-client
  CLI probes. With `plugin.gd` not registering the wire commands (§4),
  every in-editor route into client configuration is closed.
- **Dev-mode toggle and Setup section hidden** (~lines 948, 1634): both only
  expose Python dev-server / uv controls, which are meaningless without a
  Python backend (`ForkConfig.external_daemon_mode()`).

## 4. `plugin.gd` — two guards

- **Client-config wire commands guarded off** (~lines 347, 419):
  `configure_client` / `remove_client` / `check_client_status` are not
  registered, and the `client` lazy handler is not declared either — the
  dispatcher's lazy-surface audit requires every declared handler to back at
  least one command. Gated on `ForkConfig.mcp_client_config_disabled()`.
- **Dev-server spawn disabled** (~lines 1956, 1980):
  `force_restart_or_start_dev_server()` and `start_dev_server()`
  early-return — the Go daemon owns the ports and must never be killed or
  replaced by the plugin. Gated on `ForkConfig.external_daemon_mode()`.

## 5. `utils/fork_config.gd` — new file

Centralizes every "disabled in the fork" decision behind two switches,
`external_daemon_mode()` and `mcp_client_config_disabled()` (both always
true). They are functions, not constants, so GDScript's analyzer does not
flag patched call sites as `UNREACHABLE_CODE`. Adding a fork deviation =
add a switch here + a marked guard at the call site.

## 6. Comment/copy touch-ups (no behavior change)

- `plugin/godot_ai/README.md`: a fork banner is prepended — the upstream
  usage flow it describes (MCP client configuration, auto-started Python
  server, uv requirement) cannot work in the fork. The upstream body is
  kept verbatim below the banner so sync diffs stay readable.
- `connection.gd`: four comments on the ACTIVE transport path said "the
  Python server" where the peer is now the Go daemon (file header, the
  `ws_port` reconnect note, the `server_version` doc, the fresh-peer
  reconnect note). Comment-only rewording; dormant spawn-path and
  upstream-attribution comments elsewhere keep upstream wording deliberately.

## 7. Output filtering, eval print echo, and SpriteFrames ops (beta.8)

Added for the CLI's output-shaping gaps; all spots carry `godot-ai-cli fork
patch` markers. Wire-compatible on both sides: new params/fields are
optional, older peers ignore/absent them.

- `handlers/editor_handler.gd` — `get_logs` accepts `level` (`warning`
  normalized to `warn`), `grep` (case-sensitive substring) and `tail`
  (last-N, wins over offset/count). Filtered reads add `matched_count`
  (post-filter, pre-window); unfiltered reads are byte-identical to before.
  `game_eval` passes the new `echo_prints` param through.
- `runtime/game_logger.gd` — a 256-entry ring of print()/printerr() texts
  with a monotonic seq (`message_seq`/`messages_since`), mirroring the
  script-error ring, so an eval can collect exactly its own output.
- `runtime/game_helper.gd` — `_handle_eval` reads the optional third
  `mcp:eval` payload element (echo flag), snapshots the print baseline, and
  `_reply_eval_response` sends captured lines as a third `mcp:eval_response`
  element. `_game_get_scene_tree`/`_game_get_node_info` accept `name`
  (glob) and `fields` (property whitelist + `unknown_fields`).
- `debugger/mcp_debugger_plugin.gd` — `echo_prints` is threaded
  request_game_eval → wait/probe → `_send_eval` (third `mcp:eval` element);
  `_on_eval_response` maps an optional third payload element to `prints`.
- `handlers/input_handler.gd` — `list_actions` accepts an `action` glob.
- `handlers/spriteframes_handler.gd` — NEW handler (not upstream):
  `spriteframes_add_animation`, `spriteframes_add_frame` (optional atlas
  region), `spriteframes_from_sheet` (idempotent row→animation batch build).
  Registered in `plugin.gd` as the lazy "spriteframes" handler; exposed by
  the CLI as `resource spriteframes-*` (see `internal/ops`).

## 8. Frame-aligned record, debug draw, and break recovery (beta.9)

- `runtime/game_helper.gd` — new game ops: `record_frames` (one viewport
  readback per process frame, 600-frame / 5 MiB payload caps, fail-fast when
  the main loop is stalled) and `debug_draw` (tri-state on/off toggles for
  the engine's debug_*_hint rendering flags).
- `handlers/editor_handler.gd` — `game_command` widens the deferred budget
  to 60s for `record_frames` (same pattern as `input_sequence`).
- `debugger/mcp_debugger_plugin.gd` — `continue_game()` resumes a
  debugger-broken game via the plain "continue" debugger message
  (remote_debugger.cpp); `_auto_continue_after_eval_error` auto-resumes the
  game after an eval-attributed compile/runtime error so a bad eval no
  longer parks the loop and silently kills later evals.
- `handlers/project_handler.gd` — `continue_run` delegates to the debugger
  plugin; registered in `plugin.gd` as `project_continue` (CLI
  `project continue`).
- CLI-only (no plugin change): `editor record` (WrapOp record_frames; local
  PNG/GIF post-processing), `editor screenshot --region` (local crop),
  `image grid-detect` (local sprite-sheet grid inference).

## 10. Tilemap file parsing, tile physics, and SpriteFrames/focus ops (beta.11)

- `plugin.gd` + handlers — five new registered commands: `project_focus`
  (CLI `project focus`; DisplayServer.window_move_to_foreground for a
  focus-stalled game loop), `tileset_add_physics_layer` /
  `tileset_set_tile_collision` (CLI `tileset add-physics-layer` /
  `set-tile-collision`; TileSet physics layer + per-tile collision polygon),
  `resource_spriteframes_list_frames` / `resource_spriteframes_swap_frames`
  (CLI `resource spriteframes-list-frames` / `spriteframes-swap-frames`).
- Plugin fix: `game_eval --echo-prints` now captures the lines of plain
  `print(expr)` statements into `prints` (previously omitted), so the eval
  DocNote example reflects real behavior. `plugin.cfg` version 3.2.5 → 3.2.6.
- CLI-only (no plugin change): `tilemap dump` / `tilemap render`
  (`internal/tilemapfile` — decodes TileMapLayer `tile_map_data`
  PackedByteArray straight from the .tscn text and composites a map PNG from
  the TileSet atlas, no editor needed), `image view`
  (`internal/image.UpscaleNearest` — nearest-neighbor zoom for inspecting
  saved frames).

## 9. Machine-regenerable docs surface (beta.10)

CLI-only (no plugin change). Makes the skill's `references/commands.md` a
generated artifact so a CLI update can never silently drift from the docs:

- `internal/ops` — `OpSpec` gains `CLIFlags` (CLI-side-only flags declared
  in the op table instead of hardcoded `cmd.Flags()` blocks in
  `internal/cli/ops.go`) and `DocNote` (catalog-only prose: worked examples,
  usage constraints; not part of `-h`). Cobra registration, `commands
  --json`, and `commands --format md` all read the same table.
- `internal/cli/commands.go` — `commands --json` adds `cli_flags` (flag,
  kind, default, usage — always an array) and `response` per op; new
  `--format text|json|md` flag (`--json` kept as a shorthand; contradictory
  combos are a `USAGE_ERROR`).
- `internal/cli/commands_md.go` — NEW: `--format md` emits the complete
  op-catalog markdown (preamble constants + mechanically derived long-ops /
  CLI-extras bullets + per-op entries with CLI-side flag lines, Response
  notes, and DocNotes). The committed `references/commands.md` is a
  byte-identical copy of this output; the workspace doc-coverage gate diffs
  them, so prose edits go into the generator constants / op table, never
  into the generated file.
- `internal/update` — a successful `update` diffs the old binary's op table
  against the new binary's `commands --json` and adds a `docs_hint` field
  (`ops_added` / `ops_removed` + regeneration instructions) to the result
  when the surface changed; an unqueryable new binary still yields the hint
  with `ops_diff_error`. Version bumps without op changes stay silent.

## 11. Session origin tracking and stop protection (beta.13)

`plugin.cfg` version 3.2.6 → 3.2.7. Lets the CLI tell editors it spawned
apart from ones the user opened manually, so a full `stop` no longer quits
the user's own editor out from under them.

- Plugin side: the handshake gains `launched_by` — `"cli"` when the editor
  process carries the `GODOT_AI_CLI_LAUNCHED=1` environment marker (the
  CLI's `launch` exports it on the editor spawn), `"user"` otherwise.
- Daemon side (`internal/bridge`, `internal/daemon`): the handshake
  envelope accepts the field; the session registry normalizes a missing
  (pre-3.2.7 plugin) or unrecognized value to `"user"` — conservative:
  unknown provenance is never treated as CLI-spawned — and
  `/godot-ai/cli/sessions` exposes `origin` per session.
- CLI side (`internal/cli/status_stop.go`, `internal/godot/launch.go`):
  `launch` injects `GODOT_AI_CLI_LAUNCHED=1` into the editor child env;
  `status` annotates user-origin sessions with a human-readable note; a
  full `stop` now sends `quit_editor` ONLY to `origin == "cli"` sessions,
  keeps the rest, and reports both groups as `quit_sessions` /
  `kept_sessions` in the payload. `stop --all` restores the old
  quit-everything behavior; `stop --session <id>` is unchanged (an explicit
  kill regardless of origin). The daemon still shuts down either way — the
  plugin's 60s reconnect adopts the next daemon, as before.
- Plugin spawn path (`utils/cli_daemon.gd` — NEW file, plus the §2 patch in
  `utils/server_lifecycle.gd`): when the startup walk finds the backend port
  free, the fork now spawns the Go daemon itself instead of just waiting for
  one. Binary lookup is two-tier, in order — `GODOT_AI_CLI_BIN` (explicit
  override, honored only when it names an existing file so a stale value can
  never shadow a working PATH), then a PATH scan for the platform exe name
  (`godot-ai-cli.exe` on Windows, `godot-ai-cli` elsewhere; plain
  `FileAccess` checks, no subprocess). The spawn is
  `godot-ai-cli serve --http-port <port> --ws-port <ws_port>` via
  `OS.create_process`, and only when no binary is found does the walk fall
  back to the upstream Python spawn. `_spawn_cli_daemon` intentionally
  diverges from the Python spawn in three places, all because the daemon is
  not the Python server:
  - **No env staging** — `GODOT_AI_OWNER_PID` / `GODOT_AI_PLUGIN_SPAWNED` /
    `GODOT_AI_NO_IDLE_EXIT` / `GODOT_AI_WS_TOKEN` are Python-server envs the
    daemon does not read, so the whole #691 mutation window is skipped.
  - **The WS auth token is scrubbed, not regenerated** — the daemon accepts
    token-less handshakes, and a stale staged token would 4003-loop (the
    same reason external adoption drops it). Scrubbed BEFORE the
    managed-server record write, which persists the token.
  - **The plugin writes the pid-file itself** with the spawned PID — the
    daemon has no `--pid-file` flag, and without a file the watch would end
    its cold window with the "never published a pid-file" warning on every
    healthy spawn (and a fast-exited daemon could misroute into the
    `uvx --refresh` retry, which keys on an absent pid-file).

## 12. Relaxed handshake version gate: major.minor compatibility + `plugin_stale` (beta.14)

`plugin.cfg` version 3.2.7 → 3.2.8. Replaces the plugin↔daemon **strict
equality** version check with a **major.minor compatibility** rule on both
sides, after a production incident: every patch-level bump (3.2.6 → 3.2.7)
instantly made every already-installed project incompatible — the plugin
dock hard-failed with "Incompatible server" and wedged.

- Compatibility rule (single Go source: `internal/pluginmeta.ParseSemver` /
  `Compatible`; mirrored in the plugin's
  `utils/server_lifecycle.gd::_server_version_compatibility`):
  `major == major && minor == minor`. Patch drifts freely in BOTH
  directions (3.2.6 plugin ↔ 3.2.7 daemon); a minor or major mismatch is
  still refused, and a malformed `plugin_version` is refused outright.
- Daemon side (`internal/bridge`): accepted-but-unequal handshakes set
  `Session.PluginStale = true`; the `handshake_ack` then carries
  `plugin_stale: true` + `bundled_plugin_version` (both omitted when
  aligned). Rejections close the socket with a policy-violation reason —
  kept under the 123-byte WebSocket close-payload limit, so the detailed
  guidance lives in docs/log lines, not the close frame. A server whose
  own version is not semver (tests/dev placeholders) skips the gate
  instead of rejecting everything.
- Surfacing (`internal/daemon`, `internal/cli`): `/godot-ai/cli/sessions`
  publishes `plugin_version` per session plus `plugin_stale: true` when
  drifted; `status` adds the per-session note "plugin vX < bundled vY —
  run `godot-ai-cli plugin install --project <dir>` and restart the editor
  to pick up new ops" (sign computed, so a NEWER plugin never reads as
  `<`); `launch` reusing a stale session emits the same warning.
- Why the beta.11 stale-plugin protection survives the relaxation: the
  drift window is now patch-only, and the two old failure modes each keep
  a net — ops the running plugin lacks still answer `UNKNOWN_COMMAND`
  (with a stale-plugin hint in troubleshooting), and `plugin_stale` makes
  the drift visible in status/launch instead of silent. A minor bump (new
  ops surface) still refuses the handshake, so a genuinely outdated plugin
  can never run unnoticed.
- Test matrix (`internal/bridge/bridge_test.go`,
  `internal/daemon/daemon_test.go`, `internal/cli/status_test.go`,
  `internal/pluginmeta`): equal / patch-older / patch-newer / minor /
  major / malformed handshakes, sessions+status `plugin_stale` exposure,
  and the note wording. Cross-version handshake coverage is now mandatory
  — the incident was precisely the untested case.

## v3.2.5 sync notes

The vendored base was a post-v3.2.4 upstream snapshot that already carried
several fixes that shipped in v3.2.5: the batch rollback rewrite
(`handlers/batch_handler.gd`), the write-path normalization
(`utils/path_validator.gd`), the `blocks_client_health` doc update
(`utils/mcp_server_state.gd`), the #916 "skip interpretation" client-health
hunks (`mcp_dock.gd`) and the stale-server repair-message rewording
(`utils/server_lifecycle.gd`). The v3.2.5 sync therefore only merged the
remaining 9 files: 5 clean copies (`clients/_json_strategy.gd`,
`handlers/node_handler.gd`, `handlers/project_handler.gd`,
`handlers/script_handler.gd`, `plugin.cfg`) and 4 three-way merges around
the marked patches (`connection.gd`, `plugin.gd`, `mcp_dock.gd`,
`utils/server_lifecycle.gd`).

Upstream-side follow-ups landed outside the vendored tree:

- The new `set_main_scene` wire command is exposed by the CLI as
  `project set-main-scene` (see `internal/ops`).
- The Go daemon's status probe publishes `"telemetry_enabled": false` so the
  v3.2.5 dock tooltip reflects reality — the fork never phones home.

## Syncing with upstream

1. Diff `plugin/godot_ai/` against the upstream tag; every hunk that is not
   behind a `godot-ai-cli fork patch` marker is upstream drift to review.
2. Re-apply upstream changes **around** the marked patches; never remove a
   `ForkConfig` gate — extend `fork_config.gd` instead.
3. Keep dormant upstream code compiling (guards early-return, never delete),
   keep `plugin.cfg` `version` as the single version source (see
   `docs/architecture.md` → Handshake and version compatibility), and re-run
   the smoke suite (`script/smoke-e2e.sh`, needs the workspace-only `../demo`).

## 13. Multi-daemon coexistence: per-project port pinning, `--upgrade-daemon`, double-open guard (beta.16)

`plugin.cfg` version 3.2.8 → 3.2.9. Fixes the upgrade deadlock reported from
the field: with several daemons coexisting (different versions/ports), a
user-opened editor connected to an OLD daemon left `launch` with exactly one
documented way out — a full `stop`, which quits the user's editors — and a
custom-port relaunch instead DOUBLE-OPENED the same project because nothing
detected the already-running editor.

- Per-project port pinning (`internal/godot/project_ports.go` — NEW,
  `internal/cli/launch.go`): launch no longer writes `godot_ai/http_port` /
  `godot_ai/ws_port` / the managed-server record into the GLOBAL
  EditorSettings. Before spawning the editor it writes
  `<project>/.godot/godot_ai_ports.json` (`{"http_port":N,"ws_port":M}` —
  default ports too, for determinism); the plugin resolves ports as
  **project file > EditorSettings > default** (plugin-side patch in
  `utils/server_lifecycle.gd`). The global settings are never mutated, so
  parallel daemons on different ports no longer cross-wire projects, and
  `SETTINGS_OVERRIDE_ACTIVE` stops blocking new launches (the gate was the
  old "one shared override set" rule; two concurrent launches are still
  serialized by the global launch lock, and the same-project race is now
  caught by the double-open guard). The backup/restore machinery stays for
  LEGACY pre-3.2.9 leftovers: `stop` restores a pending backup exactly as
  before, and a launch that finds one warns instead of stacking.
- `--upgrade-daemon` (`internal/cli/launch.go`,
  `internal/cli/known_daemons.go` — NEW): on `DAEMON_MISMATCH`, the CLI POSTs
  the old daemon's `/godot-ai/cli/shutdown` DIRECTLY — deliberately skipping
  the `quit_editor` round a full `stop` performs — waits for the port to
  free, spawns the new daemon on the same ports, and continues the launch.
  Every editor process survives; the launch warning reports how many were
  kept and reminds that major.minor-compatible plugins reconnect by
  themselves while incompatible ones need `plugin install` + a restart.
- `DAEMON_MISMATCH` probe hint: before erroring, launch scans every known
  daemon (per-port pid files `daemon-<port>.json` + `last-daemon.json`,
  ~300ms probes) for a HEALTHY major.minor-compatible one and names it in
  `error.data.same_version_daemon` (`http_port`/`ws_port`/`version`) with a
  `use --http-port <p>` message hint.
- Double-open guard (`EDITOR_ALREADY_OPEN`): before spawning an editor —
  when the current daemon has no session for the project — launch queries
  every OTHER known daemon's `/godot-ai/cli/sessions` and refuses the spawn
  when another daemon already hosts this project, naming `editor_pid` /
  `daemon_http_port` / `daemon_version` / `session_id` and the join/migrate
  paths. `--force-spawn` is the explicit escape (help text names the
  scene-lock/save-overwrite risk). Probe failures never block the launch
  (best-effort, warning).
- Adoption gate relaxation (`internal/daemonctl`): `EnsureRunning` now
  adopts a daemon whose version is major.minor-COMPATIBLE (reusing
  `pluginmeta.ParseSemver`/`Compatible`) instead of demanding exact
  equality — a CLI patch update no longer hard-fails every launch while an
  old daemon runs. A patch drift is adopted with a stale launch warning;
  ws_port drift and minor/major drift still produce `DAEMON_MISMATCH`.
- `status` fleet view: the payload gains `known_daemons` — every recorded
  daemon probed live (running: version/pid/ports/session projects, marked
  `current` on the resolved one; dead record: `running:false` with the
  recorded identity). `ports_override_active` is now true when a per-project
  port file OR a legacy global override exists. `stop` removes each session
  project's port pin (only while it still points at the stopped daemon's
  port; reported as `project_ports_cleared`).
- **D2 fix (3.2.10 → 3.2.11)**: the `--upgrade-daemon` swap used to query
  `/godot-ai/cli/sessions` IMMEDIATELY after the new daemon came up — the
  kept editors' plugins reconnect asynchronously (backoff), so the query
  saw an empty list and launch SPAWNED A DUPLICATE editor for a project
  whose editor was alive (field-reproduced: the reconnected editor pid
  36816 and the freshly spawned pid 14748 coexisted — exactly the
  double-open `EDITOR_ALREADY_OPEN` exists to prevent). Both upgrade
  branches (the `DAEMON_MISMATCH` branch and the adopted-daemon patch-drift
  branch) now record the kept-editor count, and step 5 goes through
  `sessionsForSpawnDecision` (`internal/cli/launch.go`): with kept editors
  it re-polls the session list every 500ms until this project's session
  reappears or a 75s grace expires (both injectable package vars for
  tests). A hit within the window is the normal reuse path (no spawn);
  an expiry adds a "kept editors did not reconnect within Ns" warning and
  continues into the unchanged spawn / `EDITOR_ALREADY_OPEN` flow. Plain
  launches (no kept editors) pay no wait — one query, as before.
  Follow-up (same release line): the swap now also inherits the old
  daemon's ACTUAL WS port unless `--ws-port` was passed explicitly —
  previously the replacement daemon fell back to the flag default 9500,
  so kept editors pinned to a non-default port could never reconnect
  within the grace (double-spawning their editor) and the new daemon
  could hijack another project's daemon on 9500.
  Third-round follow-up (plugin 3.2.11 → 3.2.12): cold-start replays
  STILL intermittently double-spawned — instrumented replays (plugin
  timing probes + netstat/session samplers) showed the kept editor's
  redials hanging ~10-15s "before OPEN" while concurrent curl upgrades
  to the same port answered instantly, and the session reappearing only
  after the grace expired. Two compounding defects, both fixed:
  - plugin (`connection.gd`): `_reconnect_timer` was never reset once a
    connection reached OPEN, so the first redial after a drop waited out
    the leftover backoff of the last successful dial (up to the 60s
    cap). A drop from OPEN now zeroes the timer and redials on the same
    tick; deliberate `disconnect_from_server` closes keep the old
    scheduled behavior.
  - daemon (`internal/bridge/server.go`): the WS listener shared the 10s
    first-frame deadline as its `ReadHeaderTimeout`. The plugin's
    WebSocketPeer writes the upgrade request only from `_process` ticks,
    so an editor frame-loop stall past 10s got its TCP connection dropped
    pre-upgrade — every stall became a FAILED dial and burned a backoff
    slot. The pre-upgrade header wait is now a separate, generous 60s
    (`upgradeHeaderTimeout`); the 10s post-upgrade first-frame deadline
    is unchanged, and the listener stays loopback-only so idle-connection
    exposure is bounded. (The frame stalls themselves — 9-15s inter-frame
    gaps with a fully idle process — were also observed outside the bug
    path on the affected host and are environmental; the fixes make the
    reconnect path robust to them instead of racing them.)
  Regression coverage: `connection` suite
  (`test_post_open_drop_redials_immediately`,
  `test_deliberate_disconnect_keeps_scheduled_redial`), bridge
  `TestSlowUpgradeHeaderTolerated`, and an end-to-end replay that
  froze the editor process for 20s mid-upgrade and still got PID0 reuse
  with no grace-expiry warning.

## 14. Node-reference assignment in `set_property`, `open_scene` stale-disk warning (beta.18)

`plugin.cfg` version 3.2.9 → 3.2.10. Two editor-iteration gaps closed:
node-typed properties could not be assigned through `set_property` at all
(the editor-inspector "drag a node onto the slot" case), and `open_scene`
silently picked up on-disk content newer than the editor's in-memory state.

- `set_property` node-reference encoding (plugin-side,
  `handlers/node_handler.gd`): a value of `{"$node":"<path>"}` assigns a
  NODE reference instead of plain JSON. The path resolves RELATIVE TO THE
  TARGET NODE (the `path` param), so `{"$node":"../Sprite2D"}` on
  `/Root/Player` points at `/Root/Sprite2D`; scene-root-absolute
  `/Root/Sprite2D` works too. After `save_scene` the reference serializes
  exactly like a hand-dragged inspector assignment: NodePath-typed
  properties become a `NodePath("../Sprite2D")` plus a `node_paths` entry
  in the .tscn. An unresolvable reference fails with `NODE_NOT_FOUND`, a
  non-string/malformed `$node` payload with `INVALID_PARAMS`.
- CLI side (`internal/cli/ops.go`, `internal/ops/scene_node.go`): the
  `--value` JSON passthrough needed no change (a `{"$node":...}` object
  travels as-is). On top, `node set-property` gained the CLI-side flag
  `--node-ref <path>` (declared via the existing CLIFlagSpec mechanism,
  consumed in `collectParams`) which expands to the `{"$node":...}` wire
  value; an explicit `--value` still wins when both are given, matching the
  documented "--params base, flags override" semantics, and an explicitly
  empty `--node-ref` is an `INVALID_PARAMS` input error.
- `open_scene` stale-disk warning (plugin-side): the response data may now
  carry a `warning` string when the scene file on disk was NEWER than what
  the editor had loaded (external edit / another tool wrote it) —
  informational, not an error. Documented on the `scene open` DocNote.
- Docs: op DocNotes in `internal/ops/scene_node.go`, regenerated
  `references/commands.md`, SKILL.md procedure step 4, and
  `references/troubleshooting.md` cover the new surface; pinned Go-side
  version tests bumped to 3.2.10.
