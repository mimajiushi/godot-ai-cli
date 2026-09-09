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
		project       string
		headless      bool
		godotBin      string
		httpPort      int
		wsPort        int
		waitSec       int
		foreground    bool
		upgradeDaemon bool
		forceSpawn    bool
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

Before spawning the editor, launch writes the daemon ports into
<project>/.godot/godot_ai_ports.json (the plugin resolves ports as: project
file > EditorSettings > default) — the global EditorSettings is NOT
touched, so parallel daemons on different ports no longer cross-wire
projects. stop deletes the file again.

When the port is held by an OLD daemon (DAEMON_MISMATCH):
  - re-run with --upgrade-daemon to shut the old daemon down WITHOUT
    quitting any editor (compatible plugins reconnect to the new daemon
    automatically); after the swap launch waits (up to 15s) for the kept
    editors to reconnect and reuses their sessions instead of spawning a
    duplicate editor, or
  - point launch at a compatible already-running daemon with --http-port
    (the error's data.same_version_daemon names one when found).
When another daemon already hosts an editor for THIS project, launch fails
with EDITOR_ALREADY_OPEN instead of double-opening; --force-spawn overrides
(at your own risk: scene file locks / saves can overwrite each other).

Examples:
  godot-ai-cli launch --project C:/games/rpg
  godot-ai-cli launch --project . --headless --wait 90
  godot-ai-cli launch --project . --foreground   # keep daemon in this process`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLaunch(cmd, launchOptions{
				project:       project,
				headless:      headless,
				godotBin:      godotBin,
				httpPort:      httpPort,
				wsPort:        wsPort,
				wait:          time.Duration(waitSec) * time.Second,
				foreground:    foreground,
				upgradeDaemon: upgradeDaemon,
				forceSpawn:    forceSpawn,
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
	cmd.Flags().BoolVar(&upgradeDaemon, "upgrade-daemon", false, "on DAEMON_MISMATCH, shut the old daemon down WITHOUT quitting editors (compatible plugins reconnect), then start the new one")
	cmd.Flags().BoolVar(&forceSpawn, "force-spawn", false, "open a second editor even when another daemon hosts one for this project (RISK: scene file locks / saves can overwrite each other)")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}

// keptEditorReconnectGrace / keptEditorReconnectPoll bound the wait after
// an --upgrade-daemon swap: the kept editors' plugins reconnect to the new
// daemon asynchronously (with backoff), so the session list can be empty
// for a few seconds even though this project's editor is alive. Package
// vars so tests can shrink the wait.
var (
	keptEditorReconnectGrace = 15 * time.Second
	keptEditorReconnectPoll  = 500 * time.Millisecond
)

// launchOptions collects the launch flags.
type launchOptions struct {
	project       string
	headless      bool
	godotBin      string
	httpPort      int
	wsPort        int
	wait          time.Duration
	foreground    bool
	upgradeDaemon bool
	forceSpawn    bool
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
	// An explicit --godot that no saved default covers is worth one pointer:
	// `godot use` makes it permanent instead of repeating the flag every run.
	// Never auto-persist here — a per-run flag must not silently become
	// global state.
	if opts.godotBin != "" {
		if _, saved := godot.LoadDefaultBinary(); !saved {
			warnings = append(warnings, fmt.Sprintf(
				"Godot resolved via --godot; save it as the default with `godot-ai-cli godot use %s`", binary))
		}
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

	// keptEditors counts the editors an --upgrade-daemon swap preserved;
	// step 5 waits for their sessions to reconnect before deciding to spawn.
	keptEditors := 0
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
			if !opts.upgradeDaemon {
				return daemonMismatchError(cmd, mismatchErr, cfg.Version)
			}
			// --upgrade-daemon: shut the OLD daemon down without quitting
			// any editor, then bring the new daemon up on the same ports
			// and continue the launch normally.
			editors, uerr := shutdownDaemonKeepEditors(opts.httpPort)
			if uerr != nil {
				return jsonError(cmd, "DAEMON_UPGRADE_FAILED", uerr.Error(),
					map[string]any{"http_port": opts.httpPort, "retryable": true})
			}
			warnings = append(warnings, fmt.Sprintf(
				"old daemon (version %s) on http port %d shut down; %d editor(s) kept running — major.minor-compatible plugins reconnect to the new daemon automatically, incompatible ones need `godot-ai-cli plugin install --project <dir>` plus an editor restart",
				mismatchErr.RunningVersion, opts.httpPort, editors))
			if _, err := daemonctl.EnsureRunning(ctx, cfg); err != nil {
				return jsonError(cmd, "DAEMON_START_FAILED",
					fmt.Sprintf("old daemon stopped, but the new daemon did not come up: %v", err), nil)
			}
			keptEditors = editors
		default:
			return jsonError(cmd, "DAEMON_START_FAILED", err.Error(), nil)
		}
	} else {
		// Adopted or freshly spawned: an adopted daemon may carry a
		// patch-level version drift (accepted by the major.minor adoption
		// gate). Surface the stale hint instead of letting the version skew
		// pass silently — or, with --upgrade-daemon, actually swap the drifted
		// daemon for the bundled build (same keep-editors flow as the
		// DAEMON_MISMATCH branch above).
		if running, ok := probeDaemonHealth(opts.httpPort); ok && running != "" &&
			running != pluginmeta.PluginVersion() && semverCompatible(running, pluginmeta.PluginVersion()) {
			if opts.upgradeDaemon {
				editors, uerr := shutdownDaemonKeepEditors(opts.httpPort)
				if uerr != nil {
					return jsonError(cmd, "DAEMON_UPGRADE_FAILED", uerr.Error(),
						map[string]any{"http_port": opts.httpPort, "retryable": true})
				}
				warnings = append(warnings, fmt.Sprintf(
					"old daemon (version %s) on http port %d shut down; %d editor(s) kept running — major.minor-compatible plugins reconnect to the new daemon automatically, incompatible ones need `godot-ai-cli plugin install --project <dir>` plus an editor restart",
					running, opts.httpPort, editors))
				if _, err := daemonctl.EnsureRunning(ctx, cfg); err != nil {
					return jsonError(cmd, "DAEMON_START_FAILED",
						fmt.Sprintf("old daemon stopped, but the new daemon did not come up: %v", err), nil)
				}
				keptEditors = editors
			} else {
				warnings = append(warnings, fmt.Sprintf(
					"adopted daemon runs version %s (this CLI bundles %s) — major.minor compatible, but the daemon keeps its OLD code; relaunch with --upgrade-daemon to switch it to the bundled build",
					running, pluginmeta.PluginVersion()))
			}
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
	// After an --upgrade-daemon swap the kept editors reconnect
	// asynchronously (the plugin retries with backoff), so an immediate
	// query can come back empty even though this project's editor is alive;
	// awaitKept waits out that reconnect window instead of double-opening
	// the editor the swap deliberately preserved (defect D2).
	sessionList, waitWarning, err := sessionsForSpawnDecision(ctx, opts.httpPort, projectDir, keptEditors > 0)
	if err != nil {
		return jsonError(cmd, "DAEMON_UNREACHABLE", err.Error(), nil)
	}
	if waitWarning != "" {
		warnings = append(warnings, waitWarning)
	}
	editorPID := 0
	if findProjectSession(sessionList, projectDir) == nil {
		// Double-open guard: an editor for THIS project may already be
		// connected to ANOTHER daemon (different version/port). Spawning a
		// second editor for one project risks scene file locks and saves
		// overwriting each other, so refuse unless --force-spawn is given.
		// Probe failures never block the launch (best-effort, warnings).
		if !opts.forceSpawn {
			hit, probeWarnings := findProjectOnOtherDaemons(opts.httpPort, projectDir)
			warnings = append(warnings, probeWarnings...)
			if hit != nil {
				return jsonError(cmd, "EDITOR_ALREADY_OPEN",
					fmt.Sprintf("an editor for this project is already open and connected to the daemon on http port %v (version %v) — re-run with --http-port %v to join that daemon and reuse the session; when the versions are incompatible, run `godot-ai-cli plugin install --project <dir>` and restart that editor first, or migrate with --upgrade-daemon. --force-spawn opens a second editor anyway (RISK: scene file locks / saves overwriting each other)",
						hit["daemon_http_port"], hit["daemon_version"], hit["daemon_http_port"]),
					hit)
			}
		}

		// Pin the daemon ports PER PROJECT: the plugin resolves ports as
		// project file > EditorSettings > default, so this file (written for
		// default ports too, for determinism) replaces the old global
		// EditorSettings overrides entirely.
		if err := godot.WriteProjectPorts(projectDir, opts.httpPort, opts.wsPort); err != nil {
			return jsonError(cmd, "PROJECT_PORTS_FAILED",
				fmt.Sprintf("write %s: %v", godot.ProjectPortsPath(projectDir), err), nil)
		}

		// A legacy launch-backup (pre-3.2.9 global EditorSettings overrides)
		// still pending means another project's editors may read stale
		// global port pins. New launches never touch the global settings —
		// warn so the user can clean the leftovers up with `stop`.
		if otherPort, found := godot.FindOtherLaunchBackup(-1); found {
			warnings = append(warnings, fmt.Sprintf(
				"legacy global EditorSettings overrides from the session on http port %d are still active — run `godot-ai-cli stop --http-port %d` to restore them (this launch no longer touches the global settings)",
				otherPort, otherPort))
		}

		editorPID, err = godot.LaunchEditor(godot.LaunchOptions{
			Binary:     binary,
			ProjectDir: projectDir,
			Headless:   opts.headless,
		})
		if err != nil {
			return jsonError(cmd, "EDITOR_LAUNCH_FAILED", err.Error(), nil)
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

	// Reusing a session whose plugin drifted at patch level (accepted by
	// the major.minor handshake gate) must surface the same stale hint
	// status shows — the reused editor keeps running its OLD plugin code
	// even after `plugin install` aligned the files on disk. The stale flag
	// is relative to the DAEMON's bundled version, so name that version as
	// the align target (falling back to this CLI's when the probe fails) —
	// passing the CLI's own bundled version renders nonsense like
	// "plugin v3.2.9 ≠ bundled v3.2.9" when the daemon is an older build.
	if session["plugin_stale"] == true {
		daemonVersion := pluginmeta.PluginVersion()
		if running, ok := probeDaemonHealth(opts.httpPort); ok && running != "" {
			daemonVersion = running
		}
		warnings = append(warnings,
			pluginStaleNote(fmt.Sprint(session["plugin_version"]), daemonVersion))
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

// daemonMismatchError renders the DAEMON_MISMATCH envelope. Before giving
// up it scans every known daemon for a HEALTHY major.minor-compatible one:
// when found, data.same_version_daemon names it and the message points at
// --http-port, so a CLI update with an old daemon still running has a
// one-command fix that does not touch any editor. The three documented
// ways out: join a compatible daemon (--http-port), migrate the port's
// daemon without quitting editors (--upgrade-daemon), or a full stop.
func daemonMismatchError(cmd *cobra.Command, mismatchErr *daemonctl.DaemonMismatchError, requestedVersion string) error {
	data := map[string]any{
		"http_port":         mismatchErr.HTTPPort,
		"running_ws_port":   mismatchErr.RunningWSPort,
		"requested_ws_port": mismatchErr.RequestedWSPort,
		"running_version":   mismatchErr.RunningVersion,
		"requested_version": mismatchErr.RequestedVersion,
		"retryable":         false,
	}
	message := mismatchErr.Error() +
		"; or re-run launch with --upgrade-daemon to shut the old daemon down WITHOUT quitting its editors (compatible plugins reconnect automatically)"
	if alt, ok := findCompatibleDaemon(mismatchErr.HTTPPort, requestedVersion); ok {
		data["same_version_daemon"] = alt
		message += fmt.Sprintf(
			"; a compatible daemon (version %v) already runs on http port %v — use --http-port %v to join it",
			alt["version"], alt["http_port"], alt["http_port"])
	}
	return jsonError(cmd, "DAEMON_MISMATCH", message, data)
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

// sessionsForSpawnDecision fetches the daemon's session list for the
// step-5 spawn decision. With awaitKept set (an --upgrade-daemon swap just
// kept editors running) it re-polls every keptEditorReconnectPoll until a
// session for projectDir reappears or keptEditorReconnectGrace expires —
// the kept editors' plugins reconnect asynchronously, and acting on the
// transient empty list would spawn a duplicate editor (defect D2). A grace
// expiry or an interrupt is NOT an error: the last list comes back with a
// warning and the caller continues into the normal spawn /
// EDITOR_ALREADY_OPEN flow.
func sessionsForSpawnDecision(ctx context.Context, httpPort int, projectDir string, awaitKept bool) ([]any, string, error) {
	deadline := time.Now().Add(keptEditorReconnectGrace)
	for {
		sessions, err := getDaemonJSON(httpPort, "/godot-ai/cli/sessions")
		if err != nil {
			return nil, "", err
		}
		list, ok := sessions["sessions"].([]any)
		if !ok {
			return nil, "", errors.New("unexpected /godot-ai/cli/sessions payload shape (missing sessions array)")
		}
		if !awaitKept || findProjectSession(list, projectDir) != nil {
			return list, "", nil
		}
		reconnectWarning := fmt.Sprintf(
			"kept editors did not reconnect within %s — continuing without them; if this project's editor is still alive it keeps retrying, and a later launch reuses its session once connected",
			keptEditorReconnectGrace)
		if time.Now().After(deadline) {
			return list, reconnectWarning, nil
		}
		select {
		case <-ctx.Done():
			return list, reconnectWarning, nil
		case <-time.After(keptEditorReconnectPoll):
		}
	}
}
