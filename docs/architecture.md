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
  executes commands inside the editor. Since the v4.1.0 rebase it is upstream
  v4's signed add-on tree plus the fork patches (docs/fork-patches.md §16).

HTTP endpoints: `GET /godot-ai/status` (the upstream v4 plugin-adoption
probe — field-compatible so the GDScript plugin adopts the Go daemon exactly
like an upstream Python server; **Bearer-only**, keyed by the capability
record's auth token) and the CLI surface `GET /godot-ai/cli/health`,
`GET /godot-ai/cli/sessions`, `GET /godot-ai/cli/custom-tools`,
`POST /godot-ai/cli/activate`, `POST /godot-ai/cli/execute`,
`POST /godot-ai/cli/shutdown` (`internal/daemon/daemon.go`) — the `/cli/*`
surface is deliberately unauthenticated so agents never touch the capability
secret.

Browser CSRF hardening: everything binds loopback only, and on top of that
the POST mutation endpoints require `Content-Type: application/json`
(rejected with 415 otherwise) and cap bodies at 8 MiB — cross-origin
"simple requests" from a browser cannot satisfy that content type without a
preflight the daemon never answers. The WebSocket bridge rejects upgrade
requests whose `Origin` header names a non-loopback host with 403; the
plugin sends no `Origin` header at all, which stays accepted.

## Capability record and authenticated transport (v4)

The v4 trust anchor is a per-instance **capability record** written by
whoever spawned the daemon (`internal/capability`) at
`%LOCALAPPDATA%\godot-ai\capabilities\http-<port>.json`
(`{version:1, http, websocket, instance_nonce}` — POSIX: XDG config dir).
The record carries the bearer token; directory and file are owner-only, and
the plugin refuses linked/permissive paths (upstream `transport_capability`
rules, mirrored by the Go side). A CLI/`launch` spawn injects the record
into the editor process environment; a user-opened editor discovers it from
the well-known directory.

## Wire envelope

JSON frames (`internal/bridge/envelope.go`). The **handshake is upstream v4
protocol version 2** (`internal/bridge/auth.go`), a four-frame authenticated
dance — v3 peers (first frame `type:"handshake"`, protocol 1) are refused
with close code 4002:

1. Plugin → server: `auth_hello` (`type`, `protocol_version`,
   `client_nonce`).
2. Server → plugin: `auth_challenge` (`client_nonce`, `server_nonce`,
   `server_version`, `server_proof`).
3. Plugin → server: `auth_response` (`client_proof`, `session_id`,
   `godot_version`, `project_path`, `plugin_version`, `readiness`,
   `editor_pid`, `server_launch_mode` — plus the fork-only `launched_by`,
   §16.2). Proofs are HMAC-SHA256 keyed by the 64-hex capability over the
   per-field `\n<utf8-byte-len>:<value>` transcript, domains
   `godot-ai-ws-v2/server-proof` / `…/client-proof`.
4. Server → plugin: `handshake_ack` (`server_version` + fork keys
   `plugin_stale` / `bundled_plugin_version`), then command requests
   `{request_id, command, params}`.

Failures close with 4001 (duplicate session), 4002 (protocol mismatch — also
the Godot-version gate), 4003 (auth failed), 1008 (handshake
policy/timeout), 1009 (frame too large). Frames are capped at 8 KB pre-auth
and 4 MB post-auth; the handshake times out after 5 s.

After authentication the command envelope is unchanged: unsolicited `event`
frames (`scene_changed`, `play_state_changed`, `readiness_changed`, …) flow
plugin → server at any time, and `CommandResponse` `{request_id, status:
"ok"|"error", data, error: {code, message, data}, readiness}` answers may
arrive out of order, correlated purely by `request_id`; the `readiness`
stamp on every response heals the server's cached session readiness.

## Handshake and version compatibility

Both sides of the handshake enforce **major.minor compatibility** between
the plugin's `plugin.cfg` version and the server's version — exact equality
is NOT required (since 3.2.8; see `docs/fork-patches.md` §12 for the
incident that ended strict equality). A patch-level drift (4.1.0 ↔ 4.1.1)
is accepted and flagged: the daemon marks the session `PluginStale`, sends
`plugin_stale: true` + `bundled_plugin_version` in `handshake_ack`, and
surfaces the drift via `/godot-ai/cli/sessions`, `status`, and `launch`
warnings; the plugin side shows it as a soft amber `version_note` on the
dock (never a block). A minor/major mismatch or a malformed version is
rejected — by the plugin even earlier, at the HTTP probe stage (it refuses
to dial the WS at all). The plugin-side rule lives in
`plugin/godot_ai/utils/version_compat.gd` (`McpVersionCompat`;
`server_version_check.gd` delegates to it); the Go-side rule lives in
`internal/pluginmeta` (`ParseSemver` / `Compatible`), the single source both
the bridge gate and the CLI notes use. The advertised version flows
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

## Port pinning (per project) and the legacy EditorSettings backup/restore

Since 3.2.9 `launch` pins the daemon ports **per project**: before spawning
the editor it writes `<project>/.godot/godot_ai_ports.json`
(`{"http_port":N,"ws_port":M}`, default ports included —
`internal/godot/project_ports.go`), and the plugin resolves ports as
*project file → EditorSettings → default*. The global EditorSettings is
never mutated, so parallel daemons on different ports no longer cross-wire
projects. `stop` removes the pin (only while it still points at the stopped
daemon's port).

Launches from before 3.2.9 instead rewrote the user's **global**
EditorSettings (keys `godot_ai/http_port`, `godot_ai/ws_port`,
`godot_ai/managed_server_*`). Every such mutation was preceded by a backup
at `<user cache dir>/godot-ai-cli/launch-backup-<httpPort>.json`
(`internal/godot/launch_backup.go`), and `stop` still restores those
leftovers **byte-identically**: pre-existing keys get their original
`key = value` line written back in place, added keys are removed, and a
settings file created from scratch is deleted again. Restore first checks
the recorded editor PIDs — a surviving editor would re-save the overridden
settings on exit and resurrect them. A current `launch` that finds a
pending backup warns (the project file wins for its own editor) instead of
refusing with `SETTINGS_OVERRIDE_ACTIVE`.

## Port memory

`launch` / `serve` record the daemon they brought up in
`<user cache dir>/godot-ai-cli/last-daemon.json`
(`internal/cli/daemon_state.go`). Daemon-facing one-shot commands (`status`,
`stop`, ops, `call`, …) resolve the HTTP port in order: explicit
`--http-port` flag → recorded port → default 8000. The record is a hint
only: stale or corrupt files are silently tolerated, and the daemon
additionally writes a per-port PID file `daemon-<httpPort>.json` (same
directory) so `status` can distinguish "no daemon" from "stale PID file".
The pid files plus the last-daemon record double as the KNOWN-DAEMON
registry (`internal/cli/known_daemons.go`): `status` probes all of them
into `known_daemons`, and `launch` scans them for the `DAEMON_MISMATCH`
compatible-daemon hint and the `EDITOR_ALREADY_OPEN` double-open guard.
