# Fork patches vs upstream v4.2.3

`plugin/godot_ai/` is a fork of [hi-godot/godot-ai](https://github.com/hi-godot/godot-ai)
**v4.2.3** (MIT, "Godot AI contributors" — see `UPSTREAM-LICENSE.txt`). This
is the complete list of divergences. The behavioral patches are
marked in the GDScript source with a `godot-ai-cli fork patch` comment —
`grep -rn "godot-ai-cli fork patch" plugin/godot_ai` audits them — and gated
behind `utils/fork_config.gd` so dormant upstream code keeps compiling and
upstream diffs stay reviewable. §1 is a whole-file rewrite (marked
"STRIPPED in the godot-ai-cli fork" instead) and §6 is comment-only
rewording, so neither shows up in that grep.

> §1–§15 are the running history written against the v3.2.x base (each entry
> names the fork release that introduced it). **§16 documents the v4.1.0
> rebase**: which patches were re-applied onto the new upstream architecture,
> which upstream deletions made v3-era patches obsolete, and the upgrade
> consequence for projects still on a 3.2.x plugin.

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

## v3.2.5 sync notes (historical — superseded by the §16 v4.1.0 rebase)

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

1. Diff `plugin/godot_ai/` against the upstream tag (current base:
   **v4.1.0**); every hunk that is not behind a `godot-ai-cli fork patch`
   marker is upstream drift to review.
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

## 15. Eval compile-error attribution, prints on the error path, shell-quote-free code input (beta.23)

`plugin.cfg` version 3.2.12 → 3.2.13. Two field reports (`EVAL_COMPILE_ERROR`
carried no diagnostic; PowerShell 5.1 silently strips `"` from `--code`) plus
the launch/plugin control asks from the same project.

- **`EVAL_COMPILE_ERROR` now answers with the diagnostics** (`debugger/
  mcp_debugger_plugin.gd`): the pending entry records the code and two
  cursors (`editor_cursor`, `debugger_cursor`) at `_send_eval`; `_on_eval_grace`
  captures them BEFORE `_clear_pending` and replies
  `error.data.{code_echo, parse_errors, hint, game_status}` (+ `truncated`
  past `EVAL_PARSE_ERROR_MAX` = 5). `code_echo` is the code the plugin
  literally compiled — the fastest way to see a quote the shell stripped.
  `parse_errors` comes from `McpSurfacedErrorTracker.editor_entries_since`
  filtered to error-level rows containing "Parse Error".
- **The reply is deferred ~1s when no Parse Error row exists yet**
  (`EVAL_PARSE_ERROR_RESCAN_SEC`): measured live (RS-022), the engine row is
  promoted to the editor only AFTER the eval-attributed break is continued,
  so the first scrape at grace time is always empty. The deferred path sends
  the auto-continue first, then re-scrapes once and replies — the only way
  the new field is non-empty in practice. A synchronous
  `_compile_error_rescan_waiter` test seam mirrors `_eval_ready_frame_waiter`.
- **`prints` now rides in `error.data` too** (`_on_eval_error`,
  `_on_eval_runtime_error`): the CLI's bridge maps only `error.data` into
  `CommandError`, so the fork's `error.prints` never reached any CLI caller.
  The old key is kept (the GDScript assertions pin it).
- CLI-only (no further plugin change): `editor eval` gained `--code-file`
  (UTF-8, BOM tolerated), `--code-stdin` (terminal stdin refused instead of
  hanging) and `--code-b64`, exactly one source per call
  (`EVAL_CODE_SOURCE_CONFLICT` otherwise, `--params` code included), plus a
  `-h`-only `OpSpec.HelpNote` carrying the PowerShell 5.1 warning.
- CLI-only (launch/plugin control): `plugin.Preview`/`Plan`/`GitImpact` are a
  read-only preview shared by `launch --dry-run`,
  `launch --no-plugin-upgrade` (refuses with `PLUGIN_VERSION_MISMATCH` + the
  plan before any write), `plugin status` and `plugin install --dry-run`
  (`--version` is a guard: one plugin build is embedded,
  `PLUGIN_VERSION_UNSUPPORTED` otherwise). The launch preview/gate runs
  BEFORE the Godot probe, so both work fully offline; the ready payload adds a
  structured `plugin` object (`files_changed` / `files_created` /
  `git_dirty`). `Enable` was split into `enableContent` + `EnableDecision` so
  the preview and the write cannot diverge.

## 16. Full alignment to upstream v4.1.0 (beta.24)

`plugin.cfg` version 3.2.13 → **4.1.0**. Upstream v4 is a breaking line:
signed add-on tree, authenticated transport (capability record +
HMAC-proofed WS handshake, protocol version 2), a lifecycle-manager state
machine replacing `McpServerState`'s presentation logic, a new in-editor
updater, and the attach bridge. The fork re-based by mirroring the upstream
tree wholesale and re-applying every still-meaningful fork patch onto the
new architecture, then teaching the Go daemon the v4 transport.

**Upgrade consequence (accepted):** every project on a 3.2.x plugin must run
`godot-ai-cli plugin install` and restart the editor; v3 and v4 peers fail
closed against each other (close codes 4002 protocol mismatch / 4003 auth
failed). `plugin install` also clears a stale `addons/.godot_ai_update/`
directory left by the upstream self-updater so its restart barrier cannot
misfire.

### 16.1 Go daemon implements the v4 authenticated transport

`internal/bridge/auth.go` + `internal/capability/`: WS protocol version 2 —
`auth_hello` → `auth_challenge` → `auth_response` → `handshake_ack`, with
`server_proof`/`client_proof` = HMAC-SHA256 keyed by the 64-hex capability
over per-field `\n<utf8-byte-len>:<value>` transcripts (domains
`godot-ai-ws-v2/server-proof` / `…/client-proof`). Capability records live at
`%LOCALAPPDATA%\godot-ai\capabilities\http-<port>.json`
(`{version:1,http,websocket,instance_nonce}`); `/godot-ai/status` is
Bearer-only, CLI probes stay on unauthenticated `/godot-ai/cli/*`. The
handshake enforces the 4.7+/4.x Godot gate (mirror of upstream
`_supports_v4_editor`), close codes 4001/4002/4003/1008/1009, an 8 KB
pre-auth / 4 MB post-auth frame cap, and a 5 s handshake timeout.
`mockplugin` (`internal/testutil`) speaks the same v4 dance for tests.

### 16.2 Fork patches re-applied onto the v4 plugin

- **`launched_by` origin tracking** (`connection.gd`, §11): the fork appends
  its `launched_by` key to the v4 `auth_response` AND to the client-proof
  transcript; the daemon tries the fork transcript first, then the upstream
  one (origin `user`). `_launched_by()` returns `cli` only for
  `GODOT_AI_CLI_LAUNCHED=1`.
- **Relaxed version gate** (§12, now `utils/version_compat.gd` —
  `McpVersionCompat`): `utils/server_version_check.gd` delegates to it.
  Patch drift is a SOFT warning — `server_lifecycle.gd` carries
  `version_note` into `get_status_dict()` and the dock shows an amber label
  (`mcp_dock.gd::_refresh_server_version_label`); minor/major mismatch is a
  hard block with `McpVersionCompat.incompatible_align_hint()` in the copy.
- **`handshake_ack` fork keys** (`connection.gd`): `plugin_stale` /
  `bundled_plugin_version` are accepted via a new optional-keys parameter on
  `_has_exact_keys` (upstream demanded an exact key set and rejected the fork
  daemon's ack) and surfaced as `conn.server_plugin_stale` /
  `conn.bundled_plugin_version`.
- **D1 BLOCKED self-heal, redesigned for v4** (`utils/server_lifecycle.gd`):
  the v3 connection-level "keep redialing while blocked" loop is incoherent
  in v4 (blocked connections carry no auth token — redialing loops into
  4003). The v4 form is lifecycle-level: a BLOCKED episode whose reason is
  `incompatible`/`occupied` (and only with `automatic_effects` on) arms a
  5s/15s/30s backoff re-probe, then a 60 s steady-state re-probe
  (`BLOCKED_RECHECK_DELAYS_SECONDS`), each recheck starting a fresh tagged
  episode through `recover_blocked_episode` (stale-episode and state guards
  refuse superseded timers). When the occupant was meanwhile replaced by a
  compatible daemon the editor heals to READY without a restart. RS-021's
  old connection-suite assertions are superseded — see
  `tools/regression-scenarios.md`.
- **Port pinning** (§13): `PortPins` + `ClientConfigurator._resolved_ports()`
  keep 项目钉值 > EditorSettings (v4 endpoint override included) > 默认.
- **Go-daemon-first spawn** (§2): `_effect_launch` prefers
  `godot-ai-cli serve` via `utils/cli_daemon.gd`, with capability injection
  (`spawn_capability_process`), falling back to the upstream Python spawn
  only when no CLI binary exists.
- **No telemetry** (§1): `telemetry.gd` stays a whole-file no-op stub on the
  v4 interface (`OPT_OUT_EVENT` kept for parity; `assert_opt_out()` returns
  false — nothing is ever sent).
- **No in-editor self-update** (`ForkConfig.v4_self_update_disabled()`):
  `_update_manager` is never constructed, the update banner is forced hidden,
  `present_update_check` early-returns; plugin updates ride the CLI release
  and land via `plugin install`.
- **MCP client-config stays disabled** (§3):
  `ForkConfig.mcp_client_config_disabled()` gates the `client` lazy handler
  (3 wire commands), the dock rows/CTA/drift banner, and
  `client_job_owner.request_status_refresh` (defense in depth).
- **Fork-only commands**: `spriteframes` handler (+5 ops, now
  `extends command_handler.gd` like every v4 handler), tileset physics ops,
  `project continue`/`project focus`, input-map `list_actions` glob, the
  `$node` reference encoding in `set_property`, the open-scene stale-disk
  warning, the dispatcher's UNKNOWN_COMMAND stale-plugin hint
  (`unknown_command_error`), and the beta.8/9/23 game-side features
  (record_frames, debug_draw, eval print echo, break recovery,
  compile-error attribution) — all re-applied at their v4 locations.
- **Lazy-handler arithmetic**: upstream declares 30 handlers including
  `client`; the fork gates `client` off (−1) and adds `spriteframes` (+1) —
  the count stays 30 (`demo/tests/test_dispatcher.gd` pins it).

### 16.3 Test-suite rebase (`demo/tests`)

All 74 upstream v4.1.0 suites plus the `fixtures/` cross-language samples
were mirrored (CRLF→LF normalized); the five fork-only suites
(`cli_daemon`, `port_pins`, `spriteframes`, `tileset_physics`,
`version_compat`) are kept. Suites pinning fork-removed v3 mechanisms were
adapted rather than deleted so upstream refreshes stay diff-able:
`test_plugin_telemetry` was rewritten against the stub contract; `clients` /
`plugin_lifecycle` stash and clear the project port-pin file around their
upstream port assertions; `dock` / `plugin_lifecycle` fork-gate the four
tests that pin hidden UI; `server_lifecycle` gained the D1 re-probe block
(driven through a canned-effect subclass — `automatic_effects=true` would
otherwise run a REAL probe against the configured port). The old fork-only
`test_plugin_lifecycle` walk tests (resolved-port reuse, managed-record
pins, `McpStartupPath`, `handle_server_version_verified`) were NOT ported —
they pin v3 mechanisms the lifecycle manager deleted, and the mirrored v4
suite covers the same ground. Green baseline (Godot 4.7.2 headless):
79 suites / 2519 tests / 0 failed / 0 load errors (see
`../demo/tests/README.md`).

## 17. beta.24 requirement batch (8 user-requested features)

Landed after the v4.1.0 rebase, each with unit tests + a
`tools/regression-scenarios.md` entry (RS-024…RS-029):

- **`image cells`** (`internal/image/cells.go`, `internal/cli/image.go`):
  per-cell alpha occupancy + bbox for sprite sheets (partial edge cells
  counted, `--region` windowing, falls back to the best `grid-detect`
  candidate); `grid-detect` gained a forced-grid verification mode
  (`--cell WxH` / `--cols N --rows M` → `forced:true`, `GRID_MISMATCH` on
  inconsistency) and empty-candidate `reason`/`hints`/`factor_candidates`.
  (RS-024)
- **`script run`** (`internal/cli/script_run.go`): standalone engine
  `--script` probe with no editor session or daemon; resolves the Godot
  binary like `launch`, prefers the `<name>_console.exe` variant on Windows
  so stdout is capturable, returns exit_code + stdout/stderr + duration_ms.
  (RS-025)
- **`test run` play-state visibility** (`testing/test_runner.gd`,
  `handlers/test_handler.gd`): the result aggregates `assertions`;
  `game_status{active,status,readiness}` rides every response; failures
  during play carry `play_state_warning`; new `--require-idle-session` flag
  refuses with `EDITOR_NOT_READY`/`EDITOR_PLAYING` (retryable:false) instead
  of producing misleading results. (RS-026)
- **`editor eval --syntax-only`** (`handlers/editor_handler.gd`): an
  editor-process compile check (same wrapper template as the game side) that
  never executes, never breaks, and needs no running game — answers
  `{ok:true, source:"editor", checked_bytes}` or `EVAL_COMPILE_ERROR` with
  `code_echo` (+ PowerShell quote hint / headless no-Parse-Error fallback
  hint). (RS-027)
- **Eval break-snapshot + F2/F3** (`debugger/mcp_debugger_plugin.gd`,
  `runtime/game_helper.gd`): the rescan path of `EVAL_COMPILE_ERROR` keeps
  the pre-continue game state as `game_status_before_continue`
  (status `break` + `break.reason`); the game side now sends
  `EVAL_COMPILE_ERROR` as its `mcp:eval_error` code (whitelisted editor-side)
  so an external `project continue` inside the grace window no longer
  degrades the reply to a bare INTERNAL_ERROR; the stale head comment was
  corrected. (RS-028)
- **Mouse-aim input** (`runtime/game_helper.gd`): `game input-warp`
  (`--position` + `--space window|canvas|world`, real `Input.warp_mouse`,
  three-space echo, `clamped:true` + actual landing when the OS clamps),
  `game get-mouse` (window/canvas/world readback), and `input-mouse` now
  admits `affects_mouse_position:false` in its payload and help. (RS-029)

Also in beta.24: `game debug-control` (suspend/resume/next_frame/debug_status
through the debugger channel) and `resource physics-shape-generate`
(batch collision-shape generation for 3D scenes).

Regression fixes caught by the beta.24 cold-start replay (Agent Teams,
4 lanes, RS-001…RS-029):

- **Go-daemon brand equivalence** (`utils/port_resolver.gd`, RS-016 step 5):
  upstream's `commandline_is_godot_ai_server` demands `--pid-file` /
  `--transport` in the command line (the Python server's shape), so the
  fork-spawned Go daemon (`godot-ai-cli serve --http-port N --ws-port M`)
  failed the brand check and never got a kill grant (unbranded →
  launch_unproven — the daemon ran but the plugin could not authenticate).
  The brand check now also accepts the Go shape (brand + ` serve ` +
  `--http-port`); pinned by `test_brand_accepts_go_daemon_serve_shape`.
- **Plugin-written pid file for the Go daemon** (`utils/server_lifecycle.gd`,
  same RS-016 step 5 replay): `_effect_prove`'s pid_file gate waits on a
  file only the Python backend ever wrote, so the prove stage timed out
  even after the brand fix. `_effect_launch_cli_daemon` now pre-clears the
  pid file and publishes it itself after a successful spawn
  (`_publish_launch_pid_file`), keeping every downstream reader (prove
  gate, stop/replace `server_pid`) consistent.
- **Lane-artifact self-skip** (`demo/tests/test_clients.gd`):
  `test_addons_dir_is_symlink_detects_canonical_layout` now skips itself on
  replay-lane materialized addon copies (real directory, not junction)
  instead of failing there — it still runs and passes in the canonical
  checkout layout.
- **RS-018 adjudication**: the v3-era cross-version soft-warning +
  eval-roundtrip expectation is marked historical in the registry — v4
  peers fail closed (4002/4003) by design.



## 18. Full alignment to upstream v4.2.3 + CLI coverage of the v4.2.x tool surface (beta.25)

`plugin.cfg` version 4.1.0 → **4.2.3** (upstream v4.2.0…v4.2.3: navigation
baking/path queries, compile-validated shader authoring, VisualShader graph
authoring/editing, filesystem move/rename/remove, physics-collider refresh,
live native-resource inspection, theme font/icon slots + per-node stylebox
override + richtext, animation typed-property coercion, `.cs` text-only
script contract, omp/zcode clients, plus lifecycle/transport hardening the
fork already covered structurally). Re-based the same way as §16: mirror the
upstream tree wholesale, re-apply every fork patch via three-way merge
(`git merge-file` with upstream v4.1.0 as base — 26 files clean, the
server_lifecycle occupied-probe hunk and the connection buffer refactor
resolved by hand), then re-verify with the sentinel strip. The Go daemon's
protocol is unchanged (auth handshake, capability record, error surface all
identical), so this sync is plugin-side plus version pins.

- **Protocol verification**: `connection.gd`'s v4.2.x change only refactors
  peer-buffer setup into `_configure_peer_buffers` (#1050-class 8 KiB→4 MiB
  fix); the auth transcript, close codes, and capability record format are
  byte-identical in behaviour. `pluginmeta`'s pinned tests move to 4.2.3.
- **19 new CLI ops** (`internal/ops/navigation_shader.go`,
  `script_project_test_fs_batch.go`, `theme_ui_resource_api.go`):
  `navigation bake|path-get`, `shader create|get|patch|validate`,
  `visual-shader create-graph|get|edit|node-catalog`,
  `filesystem move|rename|remove`, `resource inspect`,
  `theme set-font|set-icon|set-stylebox-texture|stylebox-override`,
  `ui set-richtext`, plus `resource physics-shape-generate --overwrite`
  (undoable refresh). `TestPluginCommandParity` keeps the CLI surface equal
  to the plugin's registered command set (182 ops).
- **INFERRED_DECLARATION hardening**: the demo project's warning config
  treats inferred-Variant declarations as errors; 37 upstream lines across
  `client_configurator.gd`, `node_handler.gd`, `shader_handler.gd`,
  `visual_shader_handler.gd`, `spriteframes_handler.gd` gained explicit
  `: Variant` annotations (fork-marked).
- **Upstream test absorption**: five new upstream suites copied verbatim
  into `demo/tests/` (`navigation`, `shader`, `visual_shader`,
  `physics_refresh`, `resource_inspect`) plus refreshed `netstat_parser` and
  `physics_shape` (the beta.24 copies targeted the v4.1.0 internal APIs —
  `PortResolver.windows_listener_*` and `PhysicsShapeHandler._plan_generate_mesh`
  signatures moved in v4.2.x, so both failed to load with Parse errors and
  silently skipped entire suites under a green `failed:0`) — all pass
  unmodified; baseline moves to 84 suites / 2689 tests / 0 failed /
  0 load_errors. Adaptations elsewhere:
  `test_dock.gd` seeds the port picker's occupancy snapshot explicitly
  (Windows now consults the real listener snapshot instead of the mock
  probe), `test_client_attach_config.gd` gains #838 shape expectations for
  the new `zcode`/`omp` clients, `test_dispatcher.gd` re-pins the lazy
  handler count at 33.
- **Install-path test** (`plugin/install_test.go`):
  `TestInstallUpgrades41xTo42x` pins the minor-crossing upgrade detection
  (4.1.0 → 4.2.3 reports `Upgraded` and rewrites the addon).
- **D1 self-heal widened to launch-failure BLOCKED reasons**
  (`utils/server_lifecycle.gd`, RS-035): runner-3's slow-replacement replay
  caught the plugin parking at `BLOCKED(no_command)` forever — the fork's
  default environment cannot spawn a Go daemon from the editor (the
  fallback finds no server command), and the recheck whitelist only covered
  `incompatible`/`occupied`. `BLOCKED_RECHECK_REASONS` now also arms
  `no_command` / `launch_failed` / `launch_unproven` /
  `capability_dir_unwritable`, so a CLI/manually started compatible daemon
  arriving later is adopted on the next recheck (5/15/30 s, then 60 s)
  instead of the editor wedging until Restart. Pinned by two new
  `server_lifecycle` suite tests.
- **Known upstream limitation (not forked)**: v4.2.x `filesystem_mutation`
  refuses any move/rename/remove when a single owner-extension file under
  `res://` exceeds 256 KiB (`MAX_FILE_BYTES` — the reference scan reads
  every `.gd/.cs/.gdshader/.tres/...` to find uid/path references, and the
  bounded-read guard fails the whole order). Upstream's own
  `test_project/tests/test_clients.gd` is 329 KiB, so live mutations are
  unusable there too; the RS-030/032/033 scenarios park the oversized file
  out of `res://` for the duration of the mutation steps.

## 19. beta.25 requirement batch (6 user requirement docs)

`plugin.cfg` 4.2.3 → **4.2.4**. Six requirement docs landed, each with unit
tests + a `tools/regression-scenarios.md` entry (RS-036…RS-041):

- **`resource create --properties-file`** (`internal/ops/theme_ui_resource_api.go`,
  `internal/cli/ops.go`, RS-037): the `properties` JSON object can come from a
  UTF-8 file (BOM tolerated) — PowerShell 5.1 strips embedded quotes from
  native-program arguments, so the `--properties '{...}'` form was unusable on
  Windows; an explicit `--properties` flag still wins over the file.
- **Non-@tool script Resources instantiate via the placeholder path**
  (`handlers/resource_handler.gd`, same RS): `_instantiate_resource` no longer
  rejects a concrete non-@tool `class_name` Resource with WRONG_TYPE — it
  instantiates the script's native base via ClassDB and `set_script()`s it
  (the editor "create resource" dialog's own path; the PlaceHolderScriptInstance
  holds exported vars and survives ResourceSaver.save). Live-verified in a real
  editor: write → save → reload roundtrip preserves properties and the script
  reference. The required-arg `_init` static guard stays on the real
  (`scr.new()`) path only — placeholders never run `_init`.
- **Editor discovery / attach** (`internal/godot/editors_scan*.go`,
  `internal/cli/status_stop.go`, `internal/cli/launch.go`, RS-036/038/039):
  `status` prints `known_daemons`/`live_daemons` on the failure path too, and
  the hint only suggests `launch` when NO daemon on the machine is alive;
  every known-daemon entry carries `version_relation` (same/newer/older vs the
  bundled plugin); `status --project <dir>` detects an editor that is open but
  connected to no daemon and fails with `EDITOR_OPEN_UNCONNECTED`
  (editor pid + running game + live daemons + a `--attach` suggestion);
  `launch --attach` pins the daemon ports for the project and waits for the
  already-open editor's plugin to connect — spawning nothing, timing out
  loudly with `EDITOR_NOT_CONNECTED`; plain `launch` refuses to double-open
  against such an editor (scan: `--editor` + absolute `--path`, or a
  `--remote-debug`/`--editor-pid` game process backfilling the project for
  `--path ./` forms); `status --prune` deletes dead `daemon-*.json` records.
- **Handshake-rejection visibility** (`internal/bridge/server.go`,
  `internal/daemon/daemon.go`, `internal/cli/rejections.go`, `internal/cli/plugin.go`,
  RS-040): the bridge records every refused v4 handshake (version gate,
  malformed frames, nonce/proof failures, godot-version gate, duplicate
  session) into an 8-entry ring — with peer version, expected version, editor
  pid and project path — and exposes it as `GET /godot-ai/cli/rejections`;
  a v3-era first frame (`type:"handshake"`, the original incident's shape) is
  refused with the peer identity best-effort extracted into the record
  (`legacy_v3_handshake`, `legacyRejectionFrom`) — a beta.18 live replay showed
  peer 3.2.10/pid/project in all three CLI surfaces;
  `PLUGIN_DISCONNECTED(no_active_session)` carries `recent_rejections` +
  a full-editor-restart hint; `status` merges rejections from every live known
  daemon (newest 5) with a hint; `launch`'s timeout envelopes
  (`LAUNCH_TIMEOUT` / `EDITOR_NOT_CONNECTED`) include them; `plugin install`
  probes the daemon and, when a connected session or rejected handshake for
  the project reports an in-memory plugin version different from the freshly
  installed disk version, answers with a `PROJECT_PLUGIN_MISMATCH` warning and
  `next_steps` — installing files never replaces code an already-open editor
  loaded.
- **`update` proxy/diagnostics** (`internal/update/update.go`,
  `internal/cli/update.go`, RS-041): `--proxy <url>` and `--proxy auto`
  (HTTPS_PROXY/HTTP_PROXY env, then the Windows registry system proxy);
  downloads retry 3× with exponential backoff (not on 4xx) and failures carry
  `url`/`http_status`/`content_length`/`bytes_read`/`redirect_host`/
  `proxy_used`/`attempts` plus actionable guidance (explicit-proxy retry or
  manual install); `unexpected EOF` is rendered as an interrupted-at-X/Y-bytes
  message naming the TUN/proxy suspect; `--check` answers availability only;
  a failed download never performs the `.old` rename.
- **Eval single-line loop semantics** (`internal/ops/editor.go`, SKILL.md,
  RS-036): the docs now state the verified GDScript inline-suite behaviour —
  `for i in range(5): k += 1; return k` COMPILES but returns after the first
  iteration (the return is inside the loop body) — and point loops at
  `--code-file`; the previous "fails to parse" claim was wrong. A GDScript
  wrap test pins multi-line loop indentation through
  `_build_eval_script_source`.

- **Attach port re-resolution** (`utils/server_lifecycle.gd`, `plugin.gd`,
  same RS-039): the lifecycle plan captures its http/ws ports once at editor
  startup (`configure()` is one-shot), so a pin written later by
  `launch --attach` was invisible to BLOCKED rechecks — the recheck re-probed
  the stale port forever. A fork patch adds `endpoint_policy_refresher`: before
  `_begin_start_episode` (fresh starts AND rechecks) the plugin re-reads the
  project pin (project file > EditorSettings > default) and merges the
  endpoint fields. Live-verified: a blocked windowed editor adopted the daemon
  minutes after startup (`adopted external server` in the editor log).
  Companion lessons pinned in RS-039: the plugin self-disables in headless
  editors, and WMI quotes argument values which the CLI scan must strip.

Test-hermeticity fixes that landed in the same batch: `resolveDaemonPort`
gained a `daemonReachableFn` seam (the recorded-then-dead-port test used to
fail whenever ANY daemon occupied the real default port — e.g. the user's own
session), and the CLI's record path plumbing (`daemonRecordPath`) goes through
the same `userCacheDir` seam as enumeration (`daemon.PIDFilePath` reads the
real `os.UserCacheDir` directly, so `--prune` testing and the stale-pid
diagnostic were unstubbable).

## 20. beta.26 replay-findings batch (3 requirement docs from the beta.25 replay)

`plugin.cfg` 4.2.4 → **4.2.5**. Three requirement docs produced by the
beta.25 full-registry replay landed, each with unit tests + a
`tools/regression-scenarios.md` entry:

- **Probe-rejection self-report** (`utils/server_lifecycle.gd`,
  `internal/daemon/daemon.go`, `internal/bridge/server.go`, RS-042): a v4
  plugin that judges the daemon major/minor-incompatible at the HTTP probe
  stage self-blocks and NEVER dials the WebSocket, so the daemon's rejection
  ring stayed empty on exactly the live path it was built for ("editor alive,
  daemon alive, `sessions: []` forever"). The plugin now POSTs once per
  (port, daemon instance+version fingerprint) to the new Bearer-authenticated
  `POST /godot-ai/probe-rejection`; the daemon re-judges the version with the
  handshake gate's semantics and records a `probe_version_mismatch` rejection
  (consecutive identical reports deduped so BLOCKED rechecks cannot flush the
  8-entry ring). `recent_rejections`, `status`, `plugin install` and the
  `PLUGIN_DISCONNECTED` error consume it unchanged; the latter two's
  `in_memory_plugin` distinguishes `source:"probe_rejection"` from
  `rejected_handshake`. Only a NEW plugin self-reports — daemons cannot learn
  a peer's version from the bare probe, which is why the report is
  plugin-side and why the beta.24/25 plugin still has to be restarted blind.
- **`--upgrade-daemon` pre/post-upgrade guard split**
  (`internal/cli/launch.go`, `internal/cli/known_daemons.go`, RS-043):
  `shutdownDaemonKeepEditors` now returns the kept sessions' identities, not
  just a count. When THIS project's editor was connected before the swap but
  cannot re-attach within the 75s grace (incompatible in-memory plugin),
  launch no longer fails with EDITOR_OPEN_UNCONNECTED after the fact — the
  daemon replacement already happened, so it reports `status:ok` with
  `daemon_upgraded:true`, `editor_reconnected:false`, the new daemon's
  identity, `kept_editors` and editor-restart `next_steps` (plus
  `recent_rejections` when present). Only a never-connected third-party
  editor still fails closed, and that error now carries `daemon_upgraded` /
  `daemon` / `kept_editors` so "command errored but the side effect landed"
  is self-explanatory.
- **`PLUGIN_DISCONNECTED` carries `in_memory_plugin`**
  (`internal/bridge/server.go`, same RS-042): the no_active_session error
  derives the in-memory plugin versions from the rejection ring (sessions
  are empty by definition in that state), closing the last path where the
  scene was unexplainable from the CLI.

Test-suite hermeticity fix in the same batch (demo/tests, no product code):
the `get_logs` counting tests passed `debugger_errors_root=null`, so the
surfaced-error tracker fell through to the LIVE editor's Debugger Errors tab
— errors promoted during a live game session stayed there past
`project stop` and polluted every absolute-count assertion (+2). The
affected tests now inject an empty synthetic root; the two unguarded
`entry.code` / `entry.run_id` accesses use `.get()` so future residue fails
cleanly instead of aborting the case with a SCRIPT ERROR.

One REAL product bug surfaced by that replay (`utils/surfaced_error_tracker.gd`,
pinned by the strengthened
`test_surfaced_error_tracker_deferred_scan_survives_freed_root`): the #641
freed-root guard `root != null and not is_instance_valid(root)` could never
fire — under Godot 4.7 a freed instance reads as `== null` even through an
untyped Variant (and a typed member read yields null outright), so a tracker
whose injected debugger root was freed silently fell through to the LIVE
editor UI and promoted real errors into test-scoped state. The tracker now
remembers whether a root was ever injected (`_debugger_errors_root_set`) and
treats an injected-but-invalid root as "scan nothing".

## 21. Inline materials, screenshot coordinates and update fallbacks (v0.2.0-beta.1)

`plugin.cfg` 4.2.5 → **4.3.0** (minor). The 2026-09-26 shoot-2d submission
(R-1…R-6) landed as one batch; the plugin-side fork patches are:

- **Inline shader materials** (`handlers/material_handler.gd`,
  `handlers/resource_handler.gd`, `plugin.gd`, RS-046/047/048):
  `material apply-to-node` reads `shader_path` for `type=shader` (a missing
  parameter is `MISSING_REQUIRED_PARAM` with the same wording constant as
  `material create`, instead of silently attaching a `ShaderMaterial` whose
  shader is null), binds the shader BEFORE applying `--props` so
  `shader_parameter/*` and `resource_local_to_scene` resolve through
  `get_property_list()`, and defaults to an inline (never saved to disk)
  material; `material assign --from-node-path` shares the SAME instance with a
  second node (no duplicate; mutually exclusive with `--resource-path`);
  `material set-shader-param` / `material get` accept `--node-path` (an inline
  material has no `resource_path`), with `get` returning the full
  `shader_parameter_values` dict; and the new `resource_set_property` op
  (registered in `plugin.gd`) writes a field on the resource already sitting in
  a node slot, reporting `old_value`/`new_value`.
- **`resource create` snapshot re-query** (`handlers/resource_handler.gd`,
  RS-056): `_apply_resource_properties` re-reads `get_property_list()` once
  when a key is missing from the entry snapshot — applying the shader first is
  what puts `shader_parameter/*` there — and only then falls through to the
  original `PROPERTY_NOT_ON_CLASS` path. The error contract and its
  `valid_properties` list are unchanged; without the re-query the same call
  failed 5/5 while `valid_properties` listed the very key it rejected.
- **Reload diagnostic grading** (`handlers/script_handler.gd`, RS-050):
  editor-async reload jitter is no longer reported as `parse_error`; it is
  downgraded to `level:"info"` + `reload_jitter:true` with
  `reload_pending:true` / `reload_reason:"reload_pending"`, while a CONFIRMED
  parse failure keeps the error level and `parse_error`. The classification is
  a retestable predicate (`_gdscript_parse_failure_confirmed`), and the log
  capture path was split into a static method so the suite can substitute it.
- **`scene open --force-reload`** (`handlers/scene_handler.gd`, RS-051): the
  current scene goes through `EditorInterface.reload_scene_from_path()` (a
  hand-edited .tscn is really re-read on the first call, with no
  `filesystem scan` detour), and the two loading channels became injectable
  seams (`_editor_open_scene` / `_editor_reload_scene`); when the target is not
  the current scene the reply carries a `hint` naming `filesystem scan` and the
  retry instead of a silent `reloaded_from_disk:false`.
- **Screenshot coordinate metadata + node rects**
  (`handlers/editor_handler.gd`, `runtime/game_helper.gd`,
  `debugger/mcp_debugger_plugin.gd`, RS-052): screenshot replies carry
  `canvas_size`/`canvas_scale`/`note` (the game source computes the real window
  ratio inside the game process and sends it over the 9th debugger field;
  editor sources have no window stretch and truthfully report `[1,1]`), and the
  new `game node-screen-rect` read-only game command reports
  `canvas_rect`/`image_rect`/`scale` through a capability dispatch —
  `rect_kind:"bounds"` when the node implements `get_rect` (Control /
  Sprite2D), `origin_only` (transform origin + zero size) otherwise — never a
  fabricated rectangle and never a SCRIPT ERROR. One limit of the new update
  fallback channels is registered in `references/troubleshooting.md` rather
  than papered over: a GitHub secondary rate limit (403 + `Retry-After`, no
  `X-RateLimit-Remaining: 0` header) still reports `UPDATE_CHECK_FAILED`,
  `--tag` only installs a NEWER release (no rollback), and `--zip` verifies the
  file name + SHA256 but not the archive's OS/arch.