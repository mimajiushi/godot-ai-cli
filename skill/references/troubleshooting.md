# godot-ai-cli troubleshooting

Every failure is one JSON object on stdout with exit 1:

```json
{"status":"error","error":{"code":"EDITOR_NOT_READY","message":"...","data":{"sub_code":"...","retryable":true,"hint":"..."}}}
```

Route on `error.code`, then `error.data.sub_code` when present. `error.data.retryable` tells you whether a blind retry can work. `status` (exit 1 + `{"status":"daemon_not_running","hint":"Run: godot-ai-cli launch --project <path>","ports_tried":[...]}`) means no daemon answers — launch first; `ports_tried` names every port probed (explicit flag > recorded last-daemon port > 8000 default). A `stale_pid_file` field there means a daemon died without cleanup; it is informational, not blocking.

## Launch-phase codes

Emitted by `launch` itself, before any editor op is possible.

| Code | Meaning | Recovery |
|---|---|---|
| `INVALID_PROJECT` | `--project` dir has no `project.godot` | Point at the real project root. |
| `GODOT_NOT_FOUND` | No binary via `--godot` / `GODOT_BIN` / `godot use` default / PATH / conventional locations | Install Godot, pass `--godot <path>`, or save it once with `godot use <path>`; `godot detect` lists what would be found. |
| `GODOT_VERSION_UNKNOWN` | `--version` on the resolved binary failed/unparseable | Check the binary actually runs. |
| `GODOT_UNSUPPORTED` | Godot < 4.5 (`data.detected` / `data.supported` / `data.recommended`) | Upgrade Godot; 4.7+ recommended. (4.5–4.6 and 5.x are warnings in `warnings[]`, not failures.) |
| `PLUGIN_INSTALL_FAILED` | Could not extract/enable the bundled `godot_ai` plugin | Check project dir writability; retry `plugin install --project <dir>` for the bare error. |
| `FOREIGN_SERVER` | HTTP port held by a foreign process — typically the upstream Python godot-ai server (`data.http_port`) | Do NOT kill it. Relaunch with free `--http-port N --ws-port M`; later commands pick up the recorded ports automatically (an explicit `--http-port N` still works and always wins). |
| `DAEMON_MISMATCH` | The daemon on the requested port runs a different WS port or an INCOMPATIBLE plugin version (major.minor differs; a patch-level drift is adopted with a stale warning instead). `data` carries `running_ws_port` / `requested_ws_port` / versions, and `same_version_daemon` when a compatible daemon runs on another port | Three ways out: ① `same_version_daemon` present → re-launch with `--http-port <that port>` to join it. ② Re-run `launch --upgrade-daemon` — the old daemon shuts down WITHOUT quitting any connected editor; major.minor-compatible plugins reconnect to the new daemon by themselves, incompatible ones need `plugin install --project <dir>` + an editor restart. ③ `stop --http-port <N>` (this quits CLI-launched editors) and launch again. |
| `EDITOR_ALREADY_OPEN` | An editor for THIS project is already connected to ANOTHER daemon (`data.editor_pid`, `data.daemon_http_port`, `data.daemon_version`, `data.session_id`) — launch refuses to double-open the project | Re-run with `--http-port <daemon_http_port>` to join that daemon and reuse the session. Versions incompatible → `plugin install --project <dir>` + restart that editor, or migrate with `--upgrade-daemon`. Last resort: `--force-spawn` opens a second editor anyway (RISK: scene file locks / saves overwriting each other — unsaved changes in one editor are invisible to the other). |
| `SETTINGS_OVERRIDE_ACTIVE` | Legacy (pre-3.2.9 launches): another custom-port session's global EditorSettings overrides are still live (`data.active_http_port`, retryable). Current launches pin ports PER PROJECT (`.godot/godot_ai_ports.json`) and never touch the global settings, so new launches no longer emit this | `stop --http-port <active_http_port>` restores the legacy overrides, then relaunch. |
| `PROJECT_PORTS_FAILED` | Writing `<project>/.godot/godot_ai_ports.json` failed | Check the project dir is writable; `.godot/` is created when missing. |
| `EDITOR_SETTINGS_FAILED` | Reading/writing global EditorSettings or the backup failed | Check the settings file is not locked by a running editor. |
| `EDITOR_LAUNCH_FAILED` | The editor process failed to spawn | Try launching the same binary manually to see the OS error. |
| `LAUNCH_TIMEOUT` | No plugin session within `--wait` (default 60s, retryable) | Usually still starting: check `status` for a session before retrying with `--wait 90`+. |
| `DAEMON_UNREACHABLE` / `DAEMON_START_FAILED` | Daemon could not start or be probed | Check port availability, stale PID file, firewall on localhost. |
| `LAUNCH_LOCK_FAILED` | OS-level failure to open/lock the global launch-lock file — contention never produces this error: a concurrent launch/stop silently QUEUES until the holder finishes (which can look like a hang during a long `--wait`) | If a command seems stuck, another launch/stop is likely still running — wait for it. A real `LAUNCH_LOCK_FAILED` means the lock file itself (`<user cache dir>/godot-ai-cli/launch.lock`) is unusable: check permissions. |

## Image / screenshot codes

Emitted by the `image` command group (local, no daemon) and the CLI-side
extras of `editor screenshot`.

| Code | Meaning | Recovery |
|---|---|---|
| `GAME_NOT_RUNNING` | `editor screenshot --source game` while no game is running (retryable) | Start it with `project run`, then retry; use `--source viewport`/`viewport_2d` for the editor viewport. |
| `PIXEL_ASSERT_FAILED` | One or more `editor screenshot --assert '#RRGGBB@x,y'` checks mismatched (`data.samples` carries expected vs actual) | Inspect `data.samples`; widen `--tolerance` only if the delta is rendering noise, not a real regression. |
| `IMAGE_LOAD_FAILED` | `image palette`/`image probe` could not decode the file | PNG/JPEG only (no WebP); check the path — `res://` needs `--project` or a remembered launch project. |
| `SCREENSHOT_DECODE_FAILED` / `SCREENSHOT_SAVE_FAILED` | The capture's base64 payload was undecodable, or `--out` could not be written | Check disk writability; report undecodable payloads as a bug. |

## Update-phase codes

Emitted by `update`. None of them modify the install — checksum/download failures always abort before any file is touched.

| Code | Meaning | Recovery |
|---|---|---|
| `UPDATE_CHECK_FAILED` | Releases query failed: no release yet, rate limit (60 req/h/IP unauthenticated), or network down (`data.url`) | Retry later; the updater tracks both stable and prerelease tags. |
| `UPDATE_ASSET_NOT_FOUND` | No asset for this OS/arch in the latest release (`data.goos`/`data.goarch`) | Download manually from the release page or build from source. |
| `UPDATE_DOWNLOAD_FAILED` | Asset or checksums download failed | Retry on a stable network. |
| `UPDATE_CHECKSUM_INVALID` / `UPDATE_CHECKSUM_MISMATCH` | Checksums file unusable, or the asset hash differs — install provably untouched | Treat as a supply-chain warning; re-download; report if persistent. |
| `UPDATE_ARCHIVE_INVALID` | The zip holds no matching binary | Report the broken release asset. |
| `UPDATE_REPLACE_FAILED` | Could not swap the executable (message names the `.old` fallback path when rollback also failed) | Check file permissions; recover the binary from `<exe>.old` if present. |

## Runtime op codes

### EDITOR_NOT_READY — always read `data.sub_code`

| sub_code | State | retryable | Recovery |
|---|---|---|---|
| `EDITOR_IMPORTING` | Asset import in flight | true | The daemon already held the write up to ~8s re-probing; retry once after a short wait. |
| `EDITOR_PLAYING` | Game is running in the editor | false | `project stop`, then retry the write. |
| `EDITOR_NO_SCENE` | No scene is being edited | — | `scene open --path res://...` first. |
| `EDITOR_GAME_NOT_RUNNING` | Game-domain op with no running game | — | `project run` first. |
| `EDITOR_VIEWPORT_EMPTY` | Capture came back empty — headless or not-yet-drawn viewport | false | Headless renders nothing; relaunch windowed. Do not retry-loop. |
| `EDITOR_VIEWPORT_NOT_3D` | 3D screenshot against a 2D-rooted scene | false | Use `--source viewport_2d` or a 3D scene. |
| `EDITOR_VIEWPORT_UNAVAILABLE` / `EDITOR_UNAVAILABLE` | Viewport/editor interface not available | per `data` | Usually transient editor startup; check `editor state`. |
| `EDITOR_TEST_RUNNING` | A synchronous test run holds the main thread | — | Wait for `test run` to finish (300s budget) or poll `test results-get`. |
| *(absent)* | Unobservable state (script compile, modal dialog, …) | — | Bare `EDITOR_NOT_READY` is the honest fallback; check `editor state` and `logs read`. |

### Other runtime codes

| Code | Meaning | Recovery |
|---|---|---|
| `PLUGIN_DISCONNECTED` | Editor session dropped mid-command (`data.retryable: true`) | Retry once; then `status` / relaunch. |
| `TRANSPORT_TIMEOUT` | No plugin reply inside the op budget | Retry once; if persistent, `editor reload-plugin`, then check `logs read --source plugin`. |
| `DEFERRED_TIMEOUT` | A long plugin-side operation exceeded its deferred budget | Same handling as TRANSPORT_TIMEOUT. |
| `INVALID_PARAMS` and family: `MISSING_REQUIRED_PARAM`, `WRONG_TYPE`, `VALUE_OUT_OF_RANGE`, `NODE_NOT_FOUND`, `RESOURCE_NOT_FOUND`, `PROPERTY_NOT_ON_CLASS` | Input problems — the first three are fixable input errors, the last three are structural lookups | Fix the flags; `api get-class --class-name X` lists real properties; `node find` / `filesystem search` locate real paths. |
| `EDITED_SCENE_MISMATCH` | `--scene-file` guard tripped: another scene is being edited | `scene open` the intended scene or drop the guard. |
| `UNKNOWN_COMMAND` | Plugin command name not registered — when the op IS in `commands --json`, the running editor's plugin is usually stale (see the plugin-version section below) | Check spelling against `commands --json` (`plugin_command` field); on a `plugin_stale` session, `plugin install --project <dir>` and restart the editor. |
| `TEST_RUN_TIMEOUT` | Test run hit its abort ceiling; partial summary in `data` | `test results-get` returns the partial results. |
| `EVAL_COMPILE_ERROR` / `EVAL_RUNTIME_ERROR` | `editor eval` code failed to compile / threw | Fix the eval snippet. |
| `EVAL_GAME_NOT_READY` | Game helper not servicing evals though play mode is up | Wait for the game to finish booting; retry. |
| `EVAL_HUNG` | Eval never finished (game CPU-bound or frozen loop) | Simplify/shorten the eval. |
| `EVAL_RESULT_TOO_LARGE` | Serialized eval result too big for the pipeline | Return a smaller slice. |
| `GAME_HELPER_TIMEOUT` | Live game process failed to answer a game-side request | The game main loop is blocked/frozen; `project stop` and re-run. |
| `INTERNAL_ERROR` | Unclassified plugin fault | `logs read --source plugin --include-details` for the stack. |
| `SHUTDOWN_FAILED` | `stop` could not complete daemon shutdown | Teardown continued best-effort; check remaining processes manually. |

## Plugin version compatibility (`plugin_stale`)

The plugin↔daemon handshake is **major.minor compatible**, not exact-match: a 3.2.6 plugin talks to a 3.2.7 daemon (both directions). What happens per case:

- **Patch drift (accepted)**: the session connects and carries `plugin_stale: true` in `status` / `session list`, plus a per-session note `plugin vX < bundled vY — run 'godot-ai-cli plugin install --project <dir>' and restart the editor to pick up new ops`; `launch` reusing such a session adds the same warning. The running editor keeps its OLD plugin code until restarted — ops added in the newer plugin answer `UNKNOWN_COMMAND` until then.
- **Minor/major mismatch (refused)**: the daemon rejects the handshake (`incompatible plugin version …: major.minor must match`) and the plugin dock reports the server incompatible. Fix: align the project with `godot-ai-cli plugin install --project <dir>` and restart the editor, or update the CLI (`godot-ai-cli update`) so both sides share a major.minor.
- **Malformed plugin version (refused)**: same rejection path; only real released plugin builds should ever hit it.

The strict pre-3.2.8 behavior (exact equality both ways) is gone on purpose: every patch bump used to break every installed project at once.

## Port conflicts and the upstream Python godot-ai

Defaults (HTTP 8000, plugin WS 9500) collide with a running upstream godot-ai Python server. Symptoms: `FOREIGN_SERVER` at launch, or the plugin refusing to adopt the daemon. The CLI never kills foreign processes. Standard fix:

```bash
godot-ai-cli launch --project . --http-port 18000 --ws-port 19500
# launch recorded the ports in last-daemon.json — later commands, stop
# included, find this daemon without the flag:
godot-ai-cli scene get-hierarchy
godot-ai-cli stop
```

Port resolution on every daemon-facing command: explicit `--http-port` > recorded last-daemon port > default 8000 (the default is retried when the recorded port is unreachable, and the not-running error names the ports tried). `stop` removes the record when it stops that daemon. One caveat: the record is a single per-user file — when driving several daemons at once (parallel CI), keep passing `--http-port` explicitly.

Ops only take `--http-port` — the WS port is fixed at launch and carried by the daemon.

## Per-project port pinning (3.2.9+) and the legacy EditorSettings backup/restore

Since plugin/CLI 3.2.9, launch pins the daemon ports PER PROJECT: before spawning the editor it writes `<project>/.godot/godot_ai_ports.json` (`{"http_port":N,"ws_port":M}` — default ports too, for determinism), and the plugin resolves ports as **project file > EditorSettings > default**. The global EditorSettings is never touched, so several daemons on different ports coexist without cross-wiring projects, and `stop` deletes the pin again (only while it still points at the stopped daemon's port; the stop payload lists `project_ports_cleared`). `status` reports `ports_override_active: true` when any connected session's project (or the recorded launch project) carries the file — or a legacy global override is still pending.

Launches from BEFORE 3.2.9 instead overrode the user's GLOBAL Godot EditorSettings (`godot_ai/http_port`, `godot_ai/ws_port`, managed-server record). Those leftovers are still cleaned up by the current `stop`:

1. The old launch captured a backup at `<user cache dir>/godot-ai-cli/launch-backup-<httpPort>.json` (`%LOCALAPPDATA%\godot-ai-cli\` on Windows, `~/.cache/godot-ai-cli/` on Linux, `~/Library/Caches/godot-ai-cli/` on macOS). An existing backup is never overwritten — it holds the original, not the mutated state.
2. `status` shows `ports_override_active: true` while a backup for that port exists (even with no project file).
3. `stop` restores byte-identically, but only AFTER the editor process exits — the editor rewrites EditorSettings on exit and would resurrect the overrides. A still-alive editor vetoes the restore: the stop payload warns `editor still running (pid N); settings backup kept at ... — quit the editor and re-run stop`.
4. A crashed daemon is covered: `stop` with no daemon running still restores a pending backup (same editor-alive veto).
5. Manual recovery when all else fails: the backup JSON names the exact `editor_settings_path`, the keys, and their original values (or that they were absent) — edit the settings file by hand from it.

The launch warning `"legacy global EditorSettings overrides from the session on http port N are still active"` means such a pre-3.2.9 leftover exists — it no longer BLOCKS the launch (the project file wins for your editor), but other projects' editors without a port file may still read the stale global pins; `stop --http-port <N>` restores them.

## Multiple daemons at once (versions, ports)

Several godot-ai-cli daemons can coexist on different ports — e.g. an old CLI's daemon still serving a manually opened editor, and the new CLI's daemon for everything else.

- `status` shows them all: `known_daemons` lists every recorded daemon (per-port pid files + the last-daemon record), probed live — running ones with `version` / `http_port` / `ws_port` / session `projects`, dead records with `running:false`. The resolved daemon carries `current: true`.
- A launch whose requested port is held by an old daemon used to be a dead end (`stop` would quit the user's editors). Now: `--upgrade-daemon` shuts the old daemon down WITHOUT the quit_editor round — every editor process stays up, major.minor-compatible plugins reconnect to the new daemon on the same ports automatically, and the launch payload's `warnings` say how many editors were kept. Incompatible plugins must be aligned first (`plugin install` + editor restart).
- Launch also refuses to DOUBLE-OPEN a project: before spawning an editor it scans every known daemon for a session with the same project path and fails with `EDITOR_ALREADY_OPEN` (naming the editor pid, daemon port/version, and session id) when one exists. Join that daemon with `--http-port <port>` instead; `--force-spawn` is the explicit escape (scene file locks / saves can overwrite each other).
- A patch-level version drift between CLI and a running daemon is no longer a hard failure: the daemon is ADOPTED (same major.minor rule as the plugin handshake) and launch adds a stale warning — relaunch with `--upgrade-daemon` to switch the daemon to the bundled build.

## Multiple projects at once

The recommended model is ONE daemon (default ports) hosting one editor session per project — no custom ports needed:

1. `launch --project A` then `launch --project B` — each launch opens that project's editor as an additional session and pins it active. A launch only reuses an existing session when that session belongs to the SAME project; another project's session never suppresses or satisfies it.
2. Ops target the ACTIVE session. Route to another project with `session activate <id>` or an op's `--session <id>` flag; `session list` (or `status`) shows every connected session with its `project_path` and `active` flag.
3. Teardown granularity: `stop --session <id>` quits exactly one editor (daemon and other sessions keep running); plain `stop` quits EVERY connected editor, shuts the daemon down, and restores any settings overrides.
4. Editors reconnect: an editor whose daemon died keeps retrying and joins the NEXT daemon on its ports. A full `stop` prevents such orphans by quitting every session — prefer it over killing the daemon process by hand. If an orphaned editor reconnects mid-launch of the SAME project, launch may still open a second editor for it — `session list` shows both twins; `stop --session` retires the stale one.
5. Session-to-project matching compares normalized paths (case-insensitive on Windows). Exotic spellings of the same directory (junctions, symlinks, 8.3 short names) can fail to match; that failure is loud (a duplicate editor or `LAUNCH_TIMEOUT`), never silent cross-project writes.

Custom ports remain for ISOLATED daemons (e.g. defaults occupied by the upstream Python server). Since 3.2.9 each launch pins its ports in the project's own `.godot/godot_ai_ports.json`, so several custom-port daemons run side by side without interfering — the old single-shared-override constraint (one global EditorSettings, `SETTINGS_OVERRIDE_ACTIVE` when two override sets would stack) only remains for LEGACY pre-3.2.9 leftovers, which a current `stop --http-port <port>` restores. Two launches of the SAME project on different daemons are caught by the double-open guard (`EDITOR_ALREADY_OPEN`) rather than cross-wiring. A second project launched on the SAME custom ports simply joins that daemon as another session, exactly like the default-port model above. While a custom-port daemon is live, other projects' ALREADY-CONNECTED editors are unaffected.

## Headless caveats

- The headless viewport renders nothing: every `editor screenshot` source fails with `EDITOR_NOT_READY` / `EDITOR_VIEWPORT_EMPTY`, `retryable: false`. Relaunch without `--headless` for anything visual.
- Headless has no window focus events: after writing scripts via `script create`/`filesystem write-text`, run `filesystem scan` so the editor file system settles (the ops' responses say so in their diagnostics).
- Editor startup plus first import is slow headless on CI — use `--wait 90` or more.
- Game domain ops and `test run` work headless; viewport/screenshot-dependent suites typically self-skip without a real viewport (they show up as `skipped`, not failures).

## GDScript test suites

- Suites are discovered from `res://tests/` (McpTestSuite subclasses); `test run` has a 300s budget — set your shell timeout above that.
- Scene-dependent suites assume the project's MAIN scene is the edited scene. Run `scene open --path <main scene>` before `test run`; otherwise expect mass phantom failures. The results payload carries `edited_scene` and a `scene_warning` naming both paths when they differ — check it before trusting a red run.
- A `--suite` filter matching nothing is an explicit error naming the available suites, not `{"total":0}`.
