package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
	"github.com/mimajiushi/godot-ai-cli/internal/daemonctl"
	"github.com/mimajiushi/godot-ai-cli/internal/godot"
	"github.com/mimajiushi/godot-ai-cli/internal/pluginmeta"
	"github.com/mimajiushi/godot-ai-cli/plugin"
)

// jsonError prints a protocol-shaped error to stdout and returns a
// reportedError so execute still exits 1 without double-printing the
// envelope or cobra usage spam.
func jsonError(cmd *cobra.Command, code, message string, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	_ = printJSON(cmd.OutOrStdout(), map[string]any{
		"status": "error",
		"error":  map[string]any{"code": code, "message": message, "data": data},
	}, false)
	return &reportedError{err: exitError(code)}
}

// newLaunchCommand implements the zero-manual-step startup:
// find Godot → check version → install/enable the plugin → ensure the
// daemon → launch the editor detached → wait for the plugin handshake.
func newLaunchCommand() *cobra.Command {
	var (
		project    string
		headless   bool
		godotBin   string
		httpPort   int
		wsPort     int
		waitSec    int
		foreground bool
	)
	cmd := &cobra.Command{
		Use:   "launch --project PATH",
		Short: "Install the plugin, start the daemon, and launch the Godot editor",
		Long: `launch performs the full editor startup with zero manual steps:

  1. Resolve the Godot binary (--godot > GODOT_BIN > "godot use" default >
     PATH > common locations)
  2. Check the version (4.5+ required, 4.7+ recommended)
  3. Install/upgrade and enable the embedded godot_ai plugin
  4. Ensure the backend daemon runs (spawns "serve" detached if absent)
  5. Launch the Godot editor detached (skipped when THIS project's editor
     already has a connected session)
  6. Wait for the plugin session handshake and print a ready JSON line

Multiple projects can share one daemon: launching another project opens its
editor as an additional session and pins it active. Ops target the active
session by default — use "session list", "session activate <id>" or an op's
--session flag to drive a different project.

The daemon's ports are recorded in <user cache dir>/godot-ai-cli/
last-daemon.json, so later one-shot commands (status, stop, every op, call)
find the daemon WITHOUT repeating --http-port. Those commands resolve the
HTTP port as: explicit --http-port flag > recorded port > default 8000
(the default is retried when the recorded port is unreachable). stop
removes the record when it stops that daemon.

Examples:
  godot-ai-cli launch --project C:/games/rpg
  godot-ai-cli launch --project . --headless --wait 90
  godot-ai-cli launch --project . --foreground   # keep daemon in this process`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLaunch(cmd, launchOptions{
				project:    project,
				headless:   headless,
				godotBin:   godotBin,
				httpPort:   httpPort,
				wsPort:     wsPort,
				wait:       time.Duration(waitSec) * time.Second,
				foreground: foreground,
			})
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Godot project directory containing project.godot (required)")
	cmd.Flags().BoolVar(&headless, "headless", false, "launch the editor with --headless")
	cmd.Flags().StringVar(&godotBin, "godot", "", "explicit Godot binary path (overrides GODOT_BIN and PATH)")
	cmd.Flags().IntVar(&httpPort, "http-port", daemon.DefaultHTTPPort, "daemon HTTP port")
	cmd.Flags().IntVar(&wsPort, "ws-port", daemon.DefaultWSPort, "daemon plugin WebSocket port")
	cmd.Flags().IntVar(&waitSec, "wait", 60, "seconds to wait for the plugin session handshake")
	cmd.Flags().BoolVar(&foreground, "foreground", false, "run the daemon in-process instead of spawning it detached")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}

// launchOptions collects the launch flags.
type launchOptions struct {
	project    string
	headless   bool
	godotBin   string
	httpPort   int
	wsPort     int
	wait       time.Duration
	foreground bool
}

// runLaunch executes the launch pipeline and prints the result JSON.
func runLaunch(cmd *cobra.Command, opts launchOptions) error {
	out := cmd.OutOrStdout()
	var warnings []string

	// Hold the launch lock for the whole run (including the foreground
	// wait): `stop` restores the same global settings and must never run
	// concurrently with a launch.
	unlock, err := godot.AcquireLaunchLock()
	if err != nil {
		return jsonError(cmd, "LAUNCH_LOCK_FAILED", err.Error(), nil)
	}
	defer unlock()

	// Step 1: the project must be a Godot project.
	projectDir, err := filepath.Abs(opts.project)
	if err != nil {
		return jsonError(cmd, "INVALID_PROJECT", err.Error(), nil)
	}
	if info, err := os.Stat(filepath.Join(projectDir, "project.godot")); err != nil || info.IsDir() {
		return jsonError(cmd, "INVALID_PROJECT",
			fmt.Sprintf("%s does not contain a project.godot file", projectDir), nil)
	}

	// Step 2: resolve and version-check the Godot binary.
	binary, err := godot.Find(opts.godotBin)
	if err != nil {
		return jsonError(cmd, "GODOT_NOT_FOUND", err.Error(), nil)
	}
	gv, err := godot.VersionFromBinary(binary)
	if err != nil {
		return jsonError(cmd, "GODOT_VERSION_UNKNOWN", err.Error(), nil)
	}
	warn, err := godot.CheckCompatibility(gv)
	if err != nil {
		return jsonError(cmd, "GODOT_UNSUPPORTED", err.Error(),
			map[string]any{"detected": gv.Raw, "supported": "4.5+", "recommended": "4.7+"})
	}
	if warn != "" {
		warnings = append(warnings, warn)
	}

	// Step 3: install/upgrade + enable the embedded plugin.
	install, err := plugin.EnsureInstalled(projectDir)
	if err != nil {
		return jsonError(cmd, "PLUGIN_INSTALL_FAILED", err.Error(), nil)
	}
	if install.Upgraded {
		warnings = append(warnings,
			fmt.Sprintf("plugin upgraded from %s to %s", install.PreviousVersion, install.Version))
	}

	// Step 4: ensure the daemon answers on the configured ports.
	cfg := daemon.Config{
		HTTPPort: opts.httpPort,
		WSPort:   opts.wsPort,
		Version:  pluginmeta.PluginVersion(),
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var inProcess *daemon.Daemon
	if opts.foreground {
		inProcess, err = daemon.Start(ctx, cfg)
		if err != nil {
			return jsonError(cmd, "DAEMON_START_FAILED", err.Error(), nil)
		}
		defer inProcess.RequestShutdown()
	} else if _, err := daemonctl.EnsureRunning(ctx, cfg); err != nil {
		var foreignErr *daemonctl.ForeignServerError
		var mismatchErr *daemonctl.DaemonMismatchError
		switch {
		case errors.As(err, &foreignErr):
			return jsonError(cmd, "FOREIGN_SERVER", err.Error(),
				map[string]any{"http_port": opts.httpPort, "retryable": false})
		case errors.As(err, &mismatchErr):
			return jsonError(cmd, "DAEMON_MISMATCH", err.Error(), map[string]any{
				"http_port":         mismatchErr.HTTPPort,
				"running_ws_port":   mismatchErr.RunningWSPort,
				"requested_ws_port": mismatchErr.RequestedWSPort,
				"running_version":   mismatchErr.RunningVersion,
				"requested_version": mismatchErr.RequestedVersion,
				"retryable":         false,
			})
		default:
			return jsonError(cmd, "DAEMON_START_FAILED", err.Error(), nil)
		}
	}

	// Remember the daemon's ports so one-shot commands (status, ops, call,
	// ...) find it without a repeated --http-port flag. Best-effort: a
	// missing record just means resolution falls back to the default port.
	if err := writeLastDaemon(lastDaemonRecord{
		HTTPPort: opts.httpPort,
		WSPort:   opts.wsPort,
		Project:  projectDir,
	}); err != nil {
		warnings = append(warnings, fmt.Sprintf("record daemon ports: %v", err))
	}

	// Step 5: launch the editor unless a session for THIS project is already
	// connected. Other projects' sessions may share this daemon — they must
	// neither suppress our editor launch nor be mistaken for our session.
	sessions, err := getDaemonJSON(opts.httpPort, "/godot-ai/cli/sessions")
	if err != nil {
		return jsonError(cmd, "DAEMON_UNREACHABLE", err.Error(), nil)
	}
	sessionList, ok := sessions["sessions"].([]any)
	if !ok {
		return jsonError(cmd, "DAEMON_UNREACHABLE",
			"unexpected /godot-ai/cli/sessions payload shape (missing sessions array)", nil)
	}
	editorPID := 0
	settingsMutated := false
	if findProjectSession(sessionList, projectDir) == nil {
		// The plugin refuses to adopt a server whose ports differ from its
		// EditorSettings overrides (or from a stale managed-server record
		// for this plugin version). Only then do we touch the user's
		// global EditorSettings — always after capturing a backup.
		record, recErr := godot.ReadManagedRecord(gv)
		if recErr != nil {
			record = godot.ManagedRecord{} // unreadable settings: treat as absent
		}
		if settingsMutationNeeded(opts, record, godot.ReadPluginPorts(gv)) {
			// Refuse to stack a second override session: a backup for a
			// different port proves that session's overrides are still
			// active in the same shared global EditorSettings file.
			if otherPort, found := godot.FindOtherLaunchBackup(opts.httpPort); found {
				return jsonError(cmd, "SETTINGS_OVERRIDE_ACTIVE",
					fmt.Sprintf("global EditorSettings overrides from the session on http port %d are still active — run `godot-ai-cli stop --http-port %d` first", otherPort, otherPort),
					map[string]any{"active_http_port": otherPort, "retryable": true})
			}
			if _, err := godot.CaptureLaunchBackup(gv, opts.httpPort, projectDir); err != nil {
				return jsonError(cmd, "EDITOR_SETTINGS_FAILED",
					fmt.Sprintf("capture EditorSettings backup: %v", err), nil)
			}
			changed, err := godot.SetPluginPorts(gv, opts.httpPort, opts.wsPort)
			if err != nil {
				return jsonError(cmd, "EDITOR_SETTINGS_FAILED",
					fmt.Sprintf("write EditorSettings port overrides: %v", err), nil)
			}
			if changed {
				warnings = append(warnings, fmt.Sprintf(
					"wrote godot_ai/http_port=%d and godot_ai/ws_port=%d into the Godot %d.%d EditorSettings",
					opts.httpPort, opts.wsPort, gv.Major, gv.Minor))
			}
			status, err := getDaemonJSON(opts.httpPort, "/godot-ai/status")
			if err != nil {
				return jsonError(cmd, "DAEMON_UNREACHABLE", err.Error(), nil)
			}
			daemonPID, ok := status["pid"].(float64)
			if !ok {
				return jsonError(cmd, "DAEMON_UNREACHABLE",
					"unexpected /godot-ai/status payload shape (missing numeric pid)", nil)
			}
			changed, err = godot.SetPluginManagedServer(gv,
				int(daemonPID), opts.wsPort, pluginmeta.PluginVersion())
			if err != nil {
				return jsonError(cmd, "EDITOR_SETTINGS_FAILED",
					fmt.Sprintf("pin managed-server record: %v", err), nil)
			}
			if changed {
				warnings = append(warnings,
					"repinned the godot_ai managed-server record to this daemon")
			}
			settingsMutated = true
		}
		editorPID, err = godot.LaunchEditor(godot.LaunchOptions{
			Binary:     binary,
			ProjectDir: projectDir,
			Headless:   opts.headless,
		})
		if err != nil {
			return jsonError(cmd, "EDITOR_LAUNCH_FAILED", err.Error(), nil)
		}
		if settingsMutated {
			// Record the editor pid in the backup so `stop` can wait for
			// its exit (and skip the restore while it survives).
			if err := godot.RecordBackupEditorPID(opts.httpPort, editorPID); err != nil {
				warnings = append(warnings,
					fmt.Sprintf("record editor pid in the settings backup: %v", err))
			}
		}
	}

	// Step 6: wait for THIS project's plugin handshake — a session belonging
	// to another project must never satisfy the wait, or launch would report
	// ready while its own editor never connected.
	session, err := waitForSession(ctx, opts.httpPort, projectDir, opts.wait)
	if err != nil {
		return jsonError(cmd, "LAUNCH_TIMEOUT",
			fmt.Sprintf("no plugin session for this project connected within %s — the editor may still be starting; retry or raise --wait", opts.wait),
			map[string]any{"retryable": true})
	}

	// Pin the session active so the ops following a launch target the
	// project just launched — the bridge otherwise keeps the FIRST connected
	// session active, which may belong to another project.
	if sid, ok := session["session_id"].(string); ok && sid != "" && session["active"] != true {
		resp, err := postDaemonJSON(opts.httpPort, "/godot-ai/cli/activate",
			map[string]any{"session_id": sid}, 5*time.Second)
		switch {
		case err != nil:
			warnings = append(warnings, fmt.Sprintf("activate session %s: %v", sid, err))
		default:
			// postDaemonJSON only surfaces transport failures — a
			// daemon-side refusal (e.g. the session died between the poll
			// and this POST) arrives as an error envelope with a nil error.
			if st, _ := resp["status"].(string); st != "ok" {
				warnings = append(warnings, fmt.Sprintf("activate session %s refused: %v", sid, resp["error"]))
			}
		}
	}

	// The connected editor's version can differ from the binary probed in
	// step 2 (an already-connected session skips the launch step). The
	// plugin already loaded, so an unsupported editor is a warning here,
	// never a failure — but it must not pass silently.
	if raw, ok := session["godot_version"].(string); ok {
		if sv, perr := godot.ParseVersion(raw); perr == nil {
			if _, cerr := godot.CheckCompatibility(sv); cerr != nil {
				warnings = append(warnings, cerr.Error())
			}
		}
	}

	if warnings == nil {
		warnings = []string{}
	}
	result := map[string]any{
		"status":         "ready",
		"session_id":     session["session_id"],
		"godot_version":  session["godot_version"],
		"project":        projectDir,
		"editor_pid":     session["editor_pid"],
		"headless":       opts.headless,
		"daemon":         map[string]any{"http_port": opts.httpPort, "ws_port": opts.wsPort},
		"plugin_version": pluginmeta.PluginVersion(),
		"warnings":       warnings,
	}
	if editorPID != 0 {
		result["launched_editor_pid"] = editorPID
	}
	if err := printJSON(out, result, false); err != nil {
		return err
	}

	// Foreground mode keeps the daemon (and this command) alive until the
	// user interrupts, then waits for the graceful shutdown to complete;
	// detached mode exits immediately.
	if opts.foreground {
		<-ctx.Done()
		inProcess.RequestShutdown()
		<-inProcess.Done()
	}
	return nil
}

// settingsMutationNeeded decides whether launch must touch the user's
// global EditorSettings before starting the editor. With default ports and
// no conflicting managed-server record or live port override, the plugin's
// own adoption handles everything and no mutation happens at all.
func settingsMutationNeeded(opts launchOptions, record godot.ManagedRecord, cur godot.PluginPorts) bool {
	if opts.httpPort != daemon.DefaultHTTPPort || opts.wsPort != daemon.DefaultWSPort {
		return true // custom ports always need overrides written
	}
	// Live port overrides pointing at OTHER ports would send our editor's
	// plugin to another project's daemon even when the managed record looks
	// clean — editor save races can blank the record while the port keys
	// stay (one global EditorSettings file is shared by every running
	// editor). Without this check a default-port launch during another
	// session's custom-port override cross-wires the two projects onto one
	// daemon.
	if (cur.HTTPPresent && cur.HTTPPort != opts.httpPort) || (cur.WSPresent && cur.WSPort != opts.wsPort) {
		return true
	}
	// Default ports: a managed record is only trusted by the plugin when
	// its version matches the installed plugin — and then it pins the
	// expected WS port. A record for our version pointing at a different
	// port would make the plugin reject our daemon as ws_port_mismatch.
	return record.Present &&
		record.Version == pluginmeta.PluginVersion() &&
		record.WSPort != opts.wsPort
}

// waitForSession polls the daemon's session list until a session for
// projectDir connects or the deadline expires. Sessions belonging to other
// projects never satisfy the wait. Among matching sessions the active one
// is preferred. A malformed payload is an immediate error, never a panic
// and never a misleading timeout.
func waitForSession(ctx context.Context, httpPort int, projectDir string, timeout time.Duration) (map[string]any, error) {
	deadline := time.Now().Add(timeout)
	for {
		sessions, err := getDaemonJSON(httpPort, "/godot-ai/cli/sessions")
		if err == nil {
			list, ok := sessions["sessions"].([]any)
			if !ok {
				return nil, errors.New("unexpected /godot-ai/cli/sessions payload shape (missing sessions array)")
			}
			var first map[string]any
			for _, entry := range list {
				sess, ok := entry.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("unexpected session entry shape: %T", entry)
				}
				pp, _ := sess["project_path"].(string)
				if !sameProjectPath(pp, projectDir) {
					continue
				}
				if first == nil {
					first = sess
				}
				if sess["active"] == true {
					return sess, nil
				}
			}
			if first != nil {
				return first, nil
			}
		}
		if time.Now().After(deadline) {
			return nil, errors.New("wait expired")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// sameProjectPath reports whether a session's project_path (Godot-style:
// forward slashes, trailing slash) and a --project directory name the same
// project. Separator and trailing-slash differences must not split a match,
// and on Windows neither must letter case; elsewhere the comparison is
// exact. An empty sessionPath never matches.
func sameProjectPath(sessionPath, projectDir string) bool {
	norm := func(p string) string {
		p = strings.ReplaceAll(p, "\\", "/")
		return strings.TrimRight(p, "/")
	}
	a, b := norm(sessionPath), norm(projectDir)
	if a == "" || b == "" {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// findProjectSession returns the first session entry whose project_path
// matches projectDir, or nil. Entries without a string project_path never
// match.
func findProjectSession(list []any, projectDir string) map[string]any {
	for _, entry := range list {
		sess, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if pp, ok := sess["project_path"].(string); ok && sameProjectPath(pp, projectDir) {
			return sess
		}
	}
	return nil
}
