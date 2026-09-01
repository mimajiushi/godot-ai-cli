# Fork patches vs upstream v3.2.4

`plugin/godot_ai/` is a fork of [hi-godot/godot-ai](https://github.com/hi-godot/godot-ai)
**v3.2.4** (MIT, "Godot AI contributors" — see `UPSTREAM-LICENSE.txt`). This
is the complete list of divergences. Every one is marked in the GDScript
source with a `godot-ai-cli fork patch` comment — `grep -rn "godot-ai-cli
fork patch" plugin/godot_ai` audits them — and gated behind
`utils/fork_config.gd` so dormant upstream code keeps compiling and upstream
diffs stay reviewable.

## 1. `telemetry.gd` — rewritten as no-ops

Upstream relays plugin events (dock startup, self-update outcome, plugin
reload, dev-server toggle) to the Python server via
`send_event("plugin_event", …)`. The fork removes reporting entirely: no
event is buffered, sent or persisted, and no data ever leaves the editor.
The original public interface is kept as no-ops so callers (`plugin.gd`,
`mcp_dock.gd`, handlers, `update_reload_runner.gd`) need no changes;
`_drain_editor_setting_dict` is unchanged because it only touches local
EditorSettings. Rationale: the fork's hard rule is *no telemetry, ever*.

## 2. `utils/server_lifecycle.gd` — spawn early-return (~line 962)

Upstream spawns its own Python MCP server when the port is free. The fork
returns early, gated on `ForkConfig.external_daemon_mode()`: the Go daemon
owns the WS/HTTP endpoints, and the plugin's connection node already retries
the WebSocket with capped backoff, so a daemon that appears later is picked
up without any spawn. The adoption branch above the patch stays intact — a
running godot-ai-cli daemon is adopted exactly like an upstream compatible
server. The gate is a function call so the analyzer does not flag the kept,
dormant spawn sequence below as unreachable.

## 3. `mcp_dock.gd` — UI changes and client-config gating

- **Self-update removed** (~line 777): `_update_manager = null`. Plugin
  updates ship with the godot-ai-cli release instead of an in-editor
  self-updater; the update banner stays hidden and every other
  `_update_manager` reference is already null-guarded upstream.
- **MCP client-configuration UI hidden** (~lines 846, 855, 3593): the
  clients refresh button, the "Clients & Tools" button and the
  drift-reconfigure banner never show — client configuration is a CLI/skill
  concern in the fork.
- **Remaining MCP client-config paths gated off** (~lines 591, 2168, 2497,
  3096, 3272): `_dispatch_client_action` early-returns before any
  `ClientConfigurator.configure`/`remove` worker can spawn (defense in
  depth); `_on_open_clients_window` is a no-op; the "Configure an AI
  client ->" CTA is force-hidden; and both background probe paths —
  `_perform_initial_client_status_refresh` and the focus-in refresh via
  `_should_refresh_client_statuses_on_focus_in` — skip their per-client
  CLI probes. With `plugin.gd` not registering the wire commands (§4),
  every in-editor route into client configuration is closed.
- **Dev-mode toggle and Setup section hidden** (~lines 948, 1613): both only
  expose Python dev-server / uv controls, which are meaningless without a
  Python backend (`ForkConfig.external_daemon_mode()`).

## 4. `plugin.gd` — two guards

- **Client-config wire commands guarded off** (~lines 347, 418):
  `configure_client` / `remove_client` / `check_client_status` are not
  registered, and the `client` lazy handler is not declared either — the
  dispatcher's lazy-surface audit requires every declared handler to back at
  least one command. Gated on `ForkConfig.mcp_client_config_disabled()`.
- **Dev-server spawn disabled** (~lines 1940, 1964):
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

## Syncing with upstream

1. Diff `plugin/godot_ai/` against the upstream tag; every hunk that is not
   behind a `godot-ai-cli fork patch` marker is upstream drift to review.
2. Re-apply upstream changes **around** the marked patches; never remove a
   `ForkConfig` gate — extend `fork_config.gd` instead.
3. Keep dormant upstream code compiling (guards early-return, never delete),
   keep `plugin.cfg` `version` as the single version source (see
   `docs/architecture.md` → Handshake and version strictness), and re-run
   the smoke suite (`script/smoke-e2e.sh`, needs the workspace-only `../demo`).
