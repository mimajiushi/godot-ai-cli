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

## 2. `utils/server_lifecycle.gd` — spawn early-return (~line 968)

Upstream spawns its own Python MCP server when the port is free. The fork
returns early, gated on `ForkConfig.external_daemon_mode()`: the Go daemon
owns the WS/HTTP endpoints, and the plugin's connection node already retries
the WebSocket with capped backoff, so a daemon that appears later is picked
up without any spawn. The adoption branch above the patch stays intact — a
running godot-ai-cli daemon is adopted exactly like an upstream compatible
server. The gate is a function call so the analyzer does not flag the kept,
dormant spawn sequence below as unreachable.

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
   `docs/architecture.md` → Handshake and version strictness), and re-run
   the smoke suite (`script/smoke-e2e.sh`, needs the workspace-only `../demo`).
