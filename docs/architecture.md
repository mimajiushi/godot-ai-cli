# Architecture

One page on how the pieces fit. Code references are the source of truth.

## Topology

```
agent ──▶ godot-ai-cli <subcommand> ──HTTP 127.0.0.1:8000──▶ daemon
                                                              │ WebSocket 127.0.0.1:9500
                                                              ▼
                                        godot_ai editor plugin (WS client) ──▶ live Godot editor
```

- **CLI** (`cmd/godot-ai-cli`, `internal/cli`): every subcommand is a thin
  HTTP client call into the daemon and prints one JSON object on stdout.
- **Daemon** (`internal/daemon`): the combined backend `launch` spawns and
  `serve` runs — the agent-facing HTTP API on port 8000 plus the
  plugin-facing WebSocket **bridge** (`internal/bridge`) on port 9500. Both
  bind loopback only; that is the entire trust boundary.
- **Plugin** (`plugin/godot_ai`, vendored, embedded via `plugin/embed.go`):
  a GDScript editor plugin that dials the bridge as a WebSocket *client* and
  executes commands inside the editor.

HTTP endpoints: `GET /godot-ai/status` (the upstream plugin-adoption probe —
field-compatible so the GDScript plugin adopts the Go daemon exactly like an
upstream Python server) and the CLI surface `GET /godot-ai/cli/health`,
`GET /godot-ai/cli/sessions`, `GET /godot-ai/cli/custom-tools`,
`POST /godot-ai/cli/activate`, `POST /godot-ai/cli/execute`,
`POST /godot-ai/cli/shutdown` (`internal/daemon/daemon.go`).

## Wire envelope

JSON frames (`internal/bridge/envelope.go`), mirrored from the upstream
Python backend:

- Plugin → server: `Handshake` (`type`, `session_id`, `godot_version`,
  `project_path`, `plugin_version`, `protocol_version`, `readiness`,
  `editor_pid`) as the first frame; unsolicited `event` frames
  (`scene_changed`, `play_state_changed`, `readiness_changed`, …) at any time.
- Server → plugin: `handshake_ack` (`server_version`), then command requests
  `{request_id, command, params}`.
- Plugin → server: `CommandResponse` `{request_id, status: "ok"|"error",
  data, error: {code, message, data}, readiness}`. Responses may arrive out
  of order and are correlated purely by `request_id`; the `readiness` stamp
  on every response heals the server's cached session readiness.

## Handshake and version strictness

The plugin enforces **strict equality** between its own `plugin.cfg` version
and the `server_version` in `handshake_ack` — anything else is rejected as
`version_mismatch`. The check lives plugin-side in
`plugin/godot_ai/utils/server_lifecycle.gd::_server_version_compatibility`
(exact match or incompatible, no ranges). The Go side therefore advertises
the version parsed from the embedded `plugin.cfg`:
`plugin.PluginVersion()` → `internal/pluginmeta` → `daemon.Config.Version`
default → `bridge.NewServer(version)` → `handshake_ack.server_version`.
`plugin.cfg` is the single source of truth; bumping it is the only version
act needed.

## Readiness gating

Write operations pass through `bridge.RequireWritable`
(`internal/bridge/readiness.go`), mirroring the upstream `_readiness.py`
semantics:

- Cached `ready` / `no_scene` passes without a probe.
- Cached `importing` / `playing` is re-probed live (`get_editor_state`, 2s
  probe timeout) because the cache may be stale from a lost event.
- Only a live-confirmed `importing` holds the write for a bounded window
  (probe every 500 ms, cap 8 s) before failing with a retryable
  `EDITOR_NOT_READY` (`sub_code: EDITOR_IMPORTING`); a live `playing` fails
  non-retryable (`sub_code: EDITOR_PLAYING`) with a hint to stop the game.

## EditorSettings backup/restore

`launch` with custom ports rewrites the user's **global** EditorSettings
(keys `godot_ai/http_port`, `godot_ai/ws_port`, `godot_ai/managed_server_*`)
so the plugin finds the daemon. Every mutation is preceded by a backup at
`<user cache dir>/godot-ai-cli/launch-backup-<httpPort>.json`
(`internal/godot/launch_backup.go`), and `stop` restores **byte-identically**:
pre-existing keys get their original `key = value` line written back in
place, added keys are removed, and a settings file created from scratch is
deleted again. Restore first checks the recorded editor PIDs — a surviving
editor would re-save the overridden settings on exit and resurrect them.

## Port memory

`launch` / `serve` record the daemon they brought up in
`<user cache dir>/godot-ai-cli/last-daemon.json`
(`internal/cli/daemon_state.go`). Daemon-facing one-shot commands (`status`,
`stop`, ops, `call`, …) resolve the HTTP port in order: explicit
`--http-port` flag → recorded port → default 8000. The record is a hint
only: stale or corrupt files are silently tolerated, and the daemon
additionally writes a per-port PID file `daemon-<httpPort>.json` (same
directory) so `status` can distinguish "no daemon" from "stale PID file".
