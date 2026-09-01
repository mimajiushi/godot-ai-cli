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
| `GODOT_NOT_FOUND` | No binary via `--godot` / `GODOT_BIN` / PATH / conventional locations | Install Godot or pass `--godot <path>`; `godot detect` lists what would be found. |
| `GODOT_VERSION_UNKNOWN` | `--version` on the resolved binary failed/unparseable | Check the binary actually runs. |
| `GODOT_UNSUPPORTED` | Godot < 4.5 (`data.detected` / `data.supported` / `data.recommended`) | Upgrade Godot; 4.7+ recommended. (4.5–4.6 and 5.x are warnings in `warnings[]`, not failures.) |
| `PLUGIN_INSTALL_FAILED` | Could not extract/enable the bundled `godot_ai` plugin | Check project dir writability; retry `plugin install --project <dir>` for the bare error. |
| `FOREIGN_SERVER` | HTTP port held by a foreign process — typically the upstream Python godot-ai server (`data.http_port`) | Do NOT kill it. Relaunch with free `--http-port N --ws-port M`; later commands pick up the recorded ports automatically (an explicit `--http-port N` still works and always wins). |
| `DAEMON_MISMATCH` | Running daemon's WS port/plugin version ≠ requested (`data.running_ws_port`, `data.requested_ws_port`, versions) | `stop --http-port <N>` the old daemon, or match its ports. |
| `SETTINGS_OVERRIDE_ACTIVE` | Another custom-port session's EditorSettings overrides are still live (`data.active_http_port`, retryable) | `stop --http-port <active_http_port>` first, then relaunch. Launch refuses to stack two override sets because the EditorSettings file is global and shared. |
| `EDITOR_SETTINGS_FAILED` | Reading/writing global EditorSettings or the backup failed | Check the settings file is not locked by a running editor. |
| `EDITOR_LAUNCH_FAILED` | The editor process failed to spawn | Try launching the same binary manually to see the OS error. |
| `LAUNCH_TIMEOUT` | No plugin session within `--wait` (default 60s, retryable) | Usually still starting: check `status` for a session before retrying with `--wait 90`+. |
| `DAEMON_UNREACHABLE` / `DAEMON_START_FAILED` | Daemon could not start or be probed | Check port availability, stale PID file, firewall on localhost. |
| `LAUNCH_LOCK_FAILED` | OS-level failure to open/lock the global launch-lock file — contention never produces this error: a concurrent launch/stop silently QUEUES until the holder finishes (which can look like a hang during a long `--wait`) | If a command seems stuck, another launch/stop is likely still running — wait for it. A real `LAUNCH_LOCK_FAILED` means the lock file itself (`<user cache dir>/godot-ai-cli/launch.lock`) is unusable: check permissions. |

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
| `UNKNOWN_COMMAND` | Plugin command name not registered | Check spelling against `commands --json` (`plugin_command` field). |
| `TEST_RUN_TIMEOUT` | Test run hit its abort ceiling; partial summary in `data` | `test results-get` returns the partial results. |
| `EVAL_COMPILE_ERROR` / `EVAL_RUNTIME_ERROR` | `editor eval` code failed to compile / threw | Fix the eval snippet. |
| `EVAL_GAME_NOT_READY` | Game helper not servicing evals though play mode is up | Wait for the game to finish booting; retry. |
| `EVAL_HUNG` | Eval never finished (game CPU-bound or frozen loop) | Simplify/shorten the eval. |
| `EVAL_RESULT_TOO_LARGE` | Serialized eval result too big for the pipeline | Return a smaller slice. |
| `GAME_HELPER_TIMEOUT` | Live game process failed to answer a game-side request | The game main loop is blocked/frozen; `project stop` and re-run. |
| `INTERNAL_ERROR` | Unclassified plugin fault | `logs read --source plugin --include-details` for the stack. |
| `SHUTDOWN_FAILED` | `stop` could not complete daemon shutdown | Teardown continued best-effort; check remaining processes manually. |

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

## EditorSettings backup/restore (custom ports)

Custom ports temporarily override the user's GLOBAL Godot EditorSettings (`godot_ai/http_port`, `godot_ai/ws_port`, managed-server record), because the plugin reads its ports from there:

1. Before touching anything, launch captures a backup at `<user cache dir>/godot-ai-cli/launch-backup-<httpPort>.json` (`%LOCALAPPDATA%\godot-ai-cli\` on Windows, `~/.cache/godot-ai-cli/` on Linux, `~/Library/Caches/godot-ai-cli/` on macOS). An existing backup is never overwritten — it holds the original, not the mutated state.
2. `status` shows `ports_override_active: true` while a backup for that port exists.
3. `stop` restores byte-identically, but only AFTER the editor process exits — the editor rewrites EditorSettings on exit and would resurrect the overrides. A still-alive editor vetoes the restore: the stop payload warns `editor still running (pid N); settings backup kept at ... — quit the editor and re-run stop`.
4. A crashed daemon is covered: `stop` with no daemon running still restores a pending backup (same editor-alive veto).
5. Manual recovery when all else fails: the backup JSON names the exact `editor_settings_path`, the keys, and their original values (or that they were absent) — edit the settings file by hand from it.

The launch warning `"repinned the godot_ai managed-server record to this daemon"` is part of this mechanism: the plugin pins its expected WS port from that record, so custom-port launches must point it at this daemon. It is informational and rolled back with the rest on `stop`.

## Multiple projects at once

The recommended model is ONE daemon (default ports) hosting one editor session per project — no custom ports needed:

1. `launch --project A` then `launch --project B` — each launch opens that project's editor as an additional session and pins it active. A launch only reuses an existing session when that session belongs to the SAME project; another project's session never suppresses or satisfies it.
2. Ops target the ACTIVE session. Route to another project with `session activate <id>` or an op's `--session <id>` flag; `session list` (or `status`) shows every connected session with its `project_path` and `active` flag.
3. Teardown granularity: `stop --session <id>` quits exactly one editor (daemon and other sessions keep running); plain `stop` quits EVERY connected editor, shuts the daemon down, and restores any settings overrides.
4. Editors reconnect: an editor whose daemon died keeps retrying and joins the NEXT daemon on its ports. A full `stop` prevents such orphans by quitting every session — prefer it over killing the daemon process by hand. If an orphaned editor reconnects mid-launch of the SAME project, launch may still open a second editor for it — `session list` shows both twins; `stop --session` retires the stale one.
5. Session-to-project matching compares normalized paths (case-insensitive on Windows). Exotic spellings of the same directory (junctions, symlinks, 8.3 short names) can fail to match; that failure is loud (a duplicate editor or `LAUNCH_TIMEOUT`), never silent cross-project writes.

Custom ports remain for ISOLATED daemons (e.g. defaults occupied by the upstream Python server). Because the port override lives in the single shared global EditorSettings file, only one custom-port OVERRIDE SET may be live at a time: a launch that needs DIFFERENT ports while one is live (including a default-port launch during a custom-port override) fails fast with `SETTINGS_OVERRIDE_ACTIVE` naming the blocking port — `stop --http-port <that port>` first. A second project launched on the SAME custom ports simply joins that daemon as another session, exactly like the default-port model above. (When leftover overrides have no surviving backup file, launch instead normalizes the keys and proceeds — either way the projects never cross-wire.) While a custom-port daemon is live, other projects' ALREADY-CONNECTED editors are unaffected, but a running editor that saves its EditorSettings may overwrite the port keys — the restore on `stop` puts the user's original values back regardless.

## Headless caveats

- The headless viewport renders nothing: every `editor screenshot` source fails with `EDITOR_NOT_READY` / `EDITOR_VIEWPORT_EMPTY`, `retryable: false`. Relaunch without `--headless` for anything visual.
- Headless has no window focus events: after writing scripts via `script create`/`filesystem write-text`, run `filesystem scan` so the editor file system settles (the ops' responses say so in their diagnostics).
- Editor startup plus first import is slow headless on CI — use `--wait 90` or more.
- Game domain ops and `test run` work headless; viewport/screenshot-dependent suites typically self-skip without a real viewport (they show up as `skipped`, not failures).

## GDScript test suites

- Suites are discovered from `res://tests/` (McpTestSuite subclasses); `test run` has a 300s budget — set your shell timeout above that.
- Scene-dependent suites assume the project's MAIN scene is the edited scene. Run `scene open --path <main scene>` before `test run`; otherwise expect mass phantom failures. The results payload carries `edited_scene` and a `scene_warning` naming both paths when they differ — check it before trusting a red run.
- A `--suite` filter matching nothing is an explicit error naming the available suites, not `{"total":0}`.
