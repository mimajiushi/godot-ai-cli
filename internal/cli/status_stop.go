package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
	"github.com/mimajiushi/godot-ai-cli/internal/godot"
	"github.com/mimajiushi/godot-ai-cli/internal/version"
)

// fileExists reports whether path names an existing regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// newStatusCommand reports the daemon and its connected editor sessions.
func newStatusCommand() *cobra.Command {
	var httpPort int
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the daemon status and connected Godot editor sessions",
		Long: `status probes the local godot-ai daemon and prints one JSON object
describing the daemon and every connected Godot editor session.

Port resolution: an explicit --http-port flag wins; otherwise the port the
last launch/serve recorded (last-daemon.json in the user cache dir) is
tried first, falling back to the default 8000 when the recorded port is
unreachable.

When no daemon answers, it prints {"status":"daemon_not_running", ...,
"ports_tried":[...]} and exits 1. (stop reports the same condition as
{"status":"not_running"} with exit 0 — the different spelling is
intentional: status is a probe, stop an idempotent teardown.)

Examples:
  godot-ai-cli status
  godot-ai-cli status --http-port 9000`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			port, tried, ok := resolveDaemonPort(cmd)
			if !ok {
				payload := map[string]any{
					"status":      "daemon_not_running",
					"hint":        "Run: godot-ai-cli launch --project <path>",
					"ports_tried": tried,
				}
				// A leftover PID file means a daemon ran here once and died
				// without cleanup — worth one extra diagnostic field.
				for _, p := range tried {
					if _, err := os.Stat(daemon.PIDFilePath(p)); err == nil {
						payload["stale_pid_file"] = daemon.PIDFilePath(p)
						break
					}
				}
				_ = printJSON(out, payload, false)
				return errExit("daemon_not_running")
			}

			statusBody, err := getDaemonJSON(port, "/godot-ai/status")
			if err != nil {
				return jsonError(cmd, "DAEMON_UNREACHABLE", err.Error(), nil)
			}
			sessionsBody, err := getDaemonJSON(port, "/godot-ai/cli/sessions")
			if err != nil {
				return jsonError(cmd, "DAEMON_UNREACHABLE", err.Error(), nil)
			}
			sessions, compatWarnings := enrichSessionsCompatibility(sessionsBody["sessions"])
			payload := map[string]any{
				"status": "ok",
				"daemon": map[string]any{
					"version":   statusBody["version"],
					"http_port": port,
					"ws_port":   statusBody["ws_port"],
					"pid":       statusBody["pid"],
				},
				"sessions": sessions,
				// A launch backup for this port means the global
				// EditorSettings are temporarily overridden; `stop`
				// restores them.
				"ports_override_active": fileExists(godot.LaunchBackupPath(port)),
			}
			if len(compatWarnings) > 0 {
				payload["warnings"] = compatWarnings
			}
			return printJSON(out, payload, false)
		},
	}
	cmd.Flags().IntVar(&httpPort, "http-port", daemon.DefaultHTTPPort, "daemon HTTP port")
	return cmd
}

// newStopCommand asks connected editors to quit, then shuts the daemon
// down. It never hard-kills anything.
func newStopCommand() *cobra.Command {
	var httpPort int
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop connected Godot editors and shut down the daemon",
		Long: `stop performs a best-effort graceful teardown:

  1. POST quit_editor to every session's daemon (failures ignored)
  2. POST the daemon's shutdown endpoint
  3. Wait briefly for the daemon to exit

Port resolution matches status: --http-port flag > port recorded by the
last launch/serve > default 8000 (the default is retried when the recorded
port is dead). When the daemon stopped here IS the recorded one, the record
(last-daemon.json) is removed.

When no daemon is running it prints {"status":"not_running", ...,
"ports_tried":[...]} and exits 0 — stop is idempotent teardown, so
"nothing to stop" is a success. (status reports the same condition as
{"status":"daemon_not_running"} with exit 1; the different spelling is
intentional.)

Examples:
  godot-ai-cli stop
  godot-ai-cli stop --http-port 9000`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			// Hold the launch lock for the whole teardown so a concurrent
			// launch cannot re-mutate the global settings while we restore.
			unlock, err := godot.AcquireLaunchLock()
			if err != nil {
				return jsonError(cmd, "LAUNCH_LOCK_FAILED", err.Error(), nil)
			}
			defer unlock()

			port, tried, ok := resolveDaemonPort(cmd)
			if !ok {
				// No daemon: a backup may still be pending (crashed
				// daemon). Restore it here too so settings never stay
				// overridden — unless a recorded editor is still alive and
				// would re-save the overridden settings on exit, undoing
				// the restore. In that case keep the backup and warn. The
				// backup is per-port: restore the primary candidate's (the
				// explicit flag port, else the recorded one).
				primary := tried[0]
				payload := map[string]any{"status": "not_running", "ports_tried": tried}
				if alive := aliveEditorPIDs(godot.BackupEditorPIDs(primary)); len(alive) > 0 {
					payload["warnings"] = editorAliveWarnings(primary, alive)
					return printJSON(out, payload, false)
				}
				if restored, err := godot.RestoreLaunchBackup(primary); err != nil {
					payload["warnings"] = []string{fmt.Sprintf("EditorSettings restore failed: %v (backup kept at %s)", err, godot.LaunchBackupPath(primary))}
				} else if restored {
					payload["settings_restored"] = true
				}
				return printJSON(out, payload, false)
			}

			// Capture editor pids BEFORE quitting so we can sequence the
			// settings restore after the editor's exit-time settings write.
			var editorPIDs []int
			if sessionsBody, err := getDaemonJSON(port, "/godot-ai/cli/sessions"); err == nil {
				if list, ok := sessionsBody["sessions"].([]any); ok {
					for _, entry := range list {
						if sess, ok := entry.(map[string]any); ok {
							if pid, ok := sess["editor_pid"].(float64); ok && pid > 0 {
								editorPIDs = append(editorPIDs, int(pid))
							}
						}
					}
				}
			}

			// Best-effort editor quit; every failure is ignored by design.
			_, _ = postDaemonJSON(port, "/godot-ai/cli/execute", map[string]any{
				"command":     "quit_editor",
				"params":      map[string]any{},
				"timeout_sec": 5,
			}, 10*time.Second)

			// A failed shutdown must not abort the teardown: keep going so
			// the daemon-down poll and the settings restore still run, then
			// report the failure at the end.
			var shutdownErr error
			if _, err := postDaemonJSON(port, "/godot-ai/cli/shutdown", map[string]any{}, 5*time.Second); err != nil {
				shutdownErr = err
			}

			// Give the daemon a moment to release its ports.
			deadline := time.Now().Add(3 * time.Second)
			for daemonReachable(port) && time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
			}

			// Forget the recorded last-daemon identity when this stop
			// actually shut THAT daemon down — an explicit --http-port for
			// another daemon, or a daemon that survived, keeps the record.
			if rec, ok := readLastDaemon(); ok && rec.HTTPPort == port && !daemonReachable(port) {
				_ = removeLastDaemon()
			}

			var warnings []string
			payload := map[string]any{"status": "stopped"}

			// Restore the global EditorSettings launch overrode. The editor
			// rewrites EditorSettings on exit, so wait for the process to
			// actually die before writing the original values back —
			// restoring earlier races the exit-time write and loses.
			if fileExists(godot.LaunchBackupPath(port)) {
				// Persist the session pids into the backup first: if this
				// stop cannot finish the restore, a later `stop` run still
				// knows which editors to wait for.
				_ = godot.AddBackupEditorPIDs(port, editorPIDs)

				for _, pid := range editorPIDs {
					godot.WaitProcessExit(pid, 10*time.Second)
				}
				// Any recorded editor still alive (from this or an earlier
				// launch) vetoes the restore: its exit-time write would
				// resurrect the overrides afterwards. Keep the backup.
				if alive := aliveEditorPIDs(godot.BackupEditorPIDs(port)); len(alive) > 0 {
					warnings = append(warnings, editorAliveWarnings(port, alive)...)
				} else {
					restored, err := godot.RestoreLaunchBackup(port)
					switch {
					case err != nil:
						// Best-effort: warn in the payload, keep the backup
						// for manual recovery, still report stop as done.
						warnings = append(warnings, fmt.Sprintf(
							"EditorSettings restore failed: %v (backup kept at %s)",
							err, godot.LaunchBackupPath(port)))
					case restored:
						payload["settings_restored"] = true
					}
				}
			}

			if len(warnings) > 0 {
				payload["warnings"] = warnings
			}
			if shutdownErr != nil {
				return jsonError(cmd, "SHUTDOWN_FAILED", shutdownErr.Error(),
					map[string]any{"warnings": warnings})
			}
			return printJSON(out, payload, false)
		},
	}
	cmd.Flags().IntVar(&httpPort, "http-port", daemon.DefaultHTTPPort, "daemon HTTP port")
	return cmd
}

// errExit is a bare error whose message is the machine-readable code;
// Execute prints it to stderr and exits 1 while stdout stays clean JSON.
type exitError string

func (e exitError) Error() string { return string(e) }

func errExit(code string) error { return exitError(code) }

// aliveEditorPIDs returns the subset of pids still running.
func aliveEditorPIDs(pids []int) []int {
	var alive []int
	for _, pid := range pids {
		if godot.IsProcessRunning(pid) {
			alive = append(alive, pid)
		}
	}
	return alive
}

// editorAliveWarnings explains why the settings restore was skipped: a live
// editor re-saves the overridden settings on exit and would undo it.
func editorAliveWarnings(httpPort int, alive []int) []string {
	warnings := make([]string, 0, len(alive))
	for _, pid := range alive {
		warnings = append(warnings, fmt.Sprintf(
			"editor still running (pid %d); settings backup kept at %s — quit the editor and re-run stop",
			pid, godot.LaunchBackupPath(httpPort)))
	}
	return warnings
}

// enrichSessionsCompatibility annotates every session entry with
// godot_compatible and — for unsupported or untestable editors — a
// warning, so an incompatible editor never fails silently (the daemon
// accepts sessions from any Godot version). It also rolls the warnings up
// for the top-level warnings field.
func enrichSessionsCompatibility(raw any) (any, []string) {
	list, ok := raw.([]any)
	if !ok {
		return raw, nil
	}
	var warnings []string
	out := make([]any, 0, len(list))
	for _, entry := range list {
		sess, ok := entry.(map[string]any)
		if !ok {
			out = append(out, entry)
			continue
		}
		warning, compatible := godotVersionCompatibility(fmt.Sprint(sess["godot_version"]))
		sess["godot_compatible"] = compatible
		if warning != "" {
			sess["warning"] = warning
			warnings = append(warnings, warning)
		}
		out = append(out, sess)
	}
	return out, warnings
}

// godotVersionCompatibility classifies the godot_version one session
// reported at handshake: <4.5 is incompatible (the warning carries the
// CheckCompatibility error wording), 5.x and unparseable versions stay
// compatible but carry the CheckCompatibility warning wording, and
// anything else is silently compatible.
func godotVersionCompatibility(raw string) (warning string, compatible bool) {
	v, err := godot.ParseVersion(raw)
	if err != nil {
		return fmt.Sprintf(
			"Godot version %q could not be parsed: godot-ai-cli is verified against Godot %s+ (%s+ recommended)",
			raw, version.SupportedGodotMin, version.SupportedGodotRecommended), true
	}
	warn, compatErr := godot.CheckCompatibility(v)
	if compatErr != nil {
		return compatErr.Error(), false
	}
	if v.Major >= 5 {
		return warn, true
	}
	return "", true
}
