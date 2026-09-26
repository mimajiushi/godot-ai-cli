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

// pluginUpgradeRefusal renders PLUGIN_VERSION_MISMATCH for a plan that would
// rewrite a differently-versioned addons/godot_ai: the refusal happens BEFORE
// any write, and it names the file/git impact so "upgrade or not" stays an
// informed decision instead of a silent edit of version-controlled files.
func pluginUpgradeRefusal(cmd *cobra.Command, plan plugin.Plan) error {
	data := plan.JSON()
	data["hint"] = fmt.Sprintf(
		"project plugin %s ≠ bundled %s; re-run without --no-plugin-upgrade to update the addon tree, or install the CLI release that bundles %s (see `godot-ai-cli -v`)",
		plan.InstalledVersion, plan.BundledVersion, plan.InstalledVersion)
	return jsonError(cmd, "PLUGIN_VERSION_MISMATCH",
		"Project plugin version differs from the bundled plugin; refusing to modify the project without explicit consent", data)
}

// newLaunchCommand implements the zero-manual-step startup:
// check the project → preview/gate the plugin step (--dry-run /
// --no-plugin-upgrade) → find Godot → check its version → install/enable the
// plugin → ensure the daemon → launch the editor detached → wait for the
// plugin handshake.
func newLaunchCommand() *cobra.Command {
	var (
		project         string
		headless        bool
		godotBin        string
		httpPort        int
		wsPort          int
		waitSec         int
		foreground      bool
		upgradeDaemon   bool
		forceSpawn      bool
		noPluginUpgrade bool
		dryRun          bool
		attach          bool
	)
	cmd := &cobra.Command{
		Use:   "launch --project PATH",
		Short: "Install the plugin, start the daemon, and launch the Godot editor",
		Long: `launch performs the full editor startup with zero manual steps:

  1. Check that the project directory contains project.godot
  2. Preview the plugin install step and gate it: --dry-run prints the plan
     (files written, git impact) and exits; --no-plugin-upgrade fails with
     PLUGIN_VERSION_MISMATCH instead of rewriting a differently-versioned
     addons/godot_ai
  3. Resolve the Godot binary (--godot > GODOT_BIN > "godot use" default >
     PATH > common locations) and check the version (4.7+ required;
     4.5/4.6 and 5.x refused)
  4. Install/upgrade and enable the embedded godot_ai plugin
  5. Ensure the backend daemon runs (spawns "serve" detached if absent)
  6. Launch the Godot editor detached (skipped when THIS project's editor
     already has a connected session)
  7. Wait for the plugin session handshake and print a ready JSON line

With --attach, step 6 is REPLACED: launch only pins the daemon ports for the
project (<project>/.godot/godot_ai_ports.json — the plugin resolves project
file > EditorSettings > default, and its BLOCKED recheck adopts a compatible
daemon arriving later) and waits for the ALREADY-OPEN editor's plugin to
connect. Nothing is spawned, so the user's own editor keeps running as the
only instance. A timeout fails loudly with EDITOR_NOT_CONNECTED (never a
silent success). --attach also composes with --http-port to reuse an
existing daemon.

Before spawning (without --attach), launch scans for an already-open editor
for THIS project that is connected to no daemon; when found it fails with
EDITOR_OPEN_UNCONNECTED instead of double-opening (scene file locks / saves
overwriting each other). --force-spawn overrides, as with the existing
EDITOR_ALREADY_OPEN guard.

Steps 1-2 never touch the daemon, the editor or the Godot binary, so both
--dry-run and the --no-plugin-upgrade refusal work fully offline.

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

The ready payload reports the plugin step as a structured "plugin" object
(installed / upgraded / from / to / files_changed / files_created /
git_dirty), so a version bump that rewrites a tracked addon tree is visible
as data, not only as a warning line.

When the port is held by an OLD daemon (DAEMON_MISMATCH):
  - re-run with --upgrade-daemon to shut the old daemon down WITHOUT
    quitting any editor (compatible plugins reconnect to the new daemon
    automatically); after the swap launch waits (up to 75s) for the kept
    editors to reconnect and reuses their sessions instead of spawning a
    duplicate editor. A kept editor that WAS connected before the swap but
    cannot re-attach (e.g. its in-memory plugin is major.minor-incompatible
    with the new daemon) does NOT fail the command: launch reports
    status:ok with daemon_upgraded:true, editor_reconnected:false, the new
    daemon's identity and editor-restart next_steps — the upgrade itself
    already happened. An editor that was never connected still fails
    closed with EDITOR_OPEN_UNCONNECTED (data.daemon_upgraded tells the
    two situations apart), or
  - point launch at a compatible already-running daemon with --http-port
    (the error's data.same_version_daemon names one when found).
When another daemon already hosts an editor for THIS project, launch fails
with EDITOR_ALREADY_OPEN instead of double-opening; --force-spawn overrides
(at your own risk: scene file locks / saves can overwrite each other).

Examples:
  godot-ai-cli launch --project C:/games/rpg
  godot-ai-cli launch --project . --headless --wait 90
  godot-ai-cli launch --project . --dry-run      # preview what would be written
  godot-ai-cli launch --project . --no-plugin-upgrade
  godot-ai-cli launch --project . --foreground   # keep daemon in this process`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLaunch(cmd, launchOptions{
				project:         project,
				headless:        headless,
				godotBin:        godotBin,
				httpPort:        httpPort,
				wsPort:          wsPort,
				wsPortSet:       cmd.Flags().Changed("ws-port"),
				wait:            time.Duration(waitSec) * time.Second,
				foreground:      foreground,
				upgradeDaemon:   upgradeDaemon,
				forceSpawn:      forceSpawn,
				noPluginUpgrade: noPluginUpgrade,
				dryRun:          dryRun,
				attach:          attach,
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
	cmd.Flags().BoolVar(&noPluginUpgrade, "no-plugin-upgrade", false, "refuse to rewrite addons/godot_ai when the project plugin version differs from the bundled one (fails with PLUGIN_VERSION_MISMATCH instead of dirtying the project)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview the plugin install step (files written, git impact) and exit — no Godot probe, no daemon, no editor, nothing written")
	cmd.Flags().BoolVar(&attach, "attach", false, "do NOT spawn an editor: pin the daemon ports for the project and wait for the ALREADY-OPEN editor's plugin to connect (fails with EDITOR_NOT_CONNECTED on timeout)")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}

// keptEditorReconnectGrace / keptEditorReconnectPoll bound the wait after
// an --upgrade-daemon swap: the kept editors' plugins reconnect to the new
// daemon asynchronously (with backoff), so the session list can be empty
// for a while even though this project's editor is alive. The grace must
// cover the plugin's reconnect backoff cap (60s) plus margin: the editor's
// backoff schedule is anchored to when it noticed the OLD daemon die, so
// by the time the new daemon is healthy the editor may already be sitting
// in a long sleep slot (15s proved too short — RS-021 replay on beta.20).
// Package vars so tests can shrink the wait.
var (
	keptEditorReconnectGrace = 75 * time.Second
	keptEditorReconnectPoll  = 500 * time.Millisecond
)

// launchOptions collects the launch flags.
type launchOptions struct {
	project         string
	headless        bool
	godotBin        string
	httpPort        int
	wsPort          int
	wsPortSet       bool // true only when --ws-port was passed explicitly
	wait            time.Duration
	foreground      bool
	upgradeDaemon   bool
	forceSpawn      bool
	noPluginUpgrade bool // --no-plugin-upgrade: refuse a version-rewriting install
	dryRun          bool // --dry-run: print the plugin plan and exit before any mutation
	// attach：--attach 不 spawn 编辑器——只写工程端口钉 + 确保 daemon，
	// 等已打开编辑器的插件重连（需求 editor-attach-and-daemon-discovery §3.3）。
	attach bool
}

// 以下三个 seam 让 runLaunch 的编辑器相关副作用可在测试中注入：launch
// 全流程单测必须假设「本机没有 Godot、绝不真开编辑器」，否则在 CI（无
// Godot）与开发机（有用户的编辑器）上都不可复现。
var (
	launchEditorFn    = godot.LaunchEditor
	resolveGodotBinFn = godot.Find
	probeGodotVerFn   = godot.VersionFromBinary
)

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

	// Step 2 (new): preview the plugin install step and gate it. Deliberately
	// placed BEFORE the Godot probe: a preview and a refusal must not depend on
	// whether this machine has Godot installed, which also keeps --dry-run
	// fully offline and side-effect free.
	plan, err := plugin.Preview(projectDir)
	if err != nil {
		return jsonError(cmd, "PLUGIN_PLAN_FAILED", err.Error(), nil)
	}
	if opts.dryRun {
		return printJSON(out, map[string]any{
			"status":   "ok",
			"dry_run":  true,
			"project":  projectDir,
			"plugin":   plan.JSON(),
			"warnings": []string{},
			"note":     "dry run covers the plugin install step only — no Godot probe, no daemon, no editor, nothing written",
		}, false)
	}
	if opts.noPluginUpgrade && plan.VersionMismatch() {
		return pluginUpgradeRefusal(cmd, plan)
	}

	// Step 3: resolve and version-check the Godot binary.
	binary, err := resolveGodotBinFn(opts.godotBin)
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
	gv, err := probeGodotVerFn(binary)
	if err != nil {
		return jsonError(cmd, "GODOT_VERSION_UNKNOWN", err.Error(), nil)
	}
	warn, err := godot.CheckCompatibility(gv)
	if err != nil {
		return jsonError(cmd, "GODOT_UNSUPPORTED", err.Error(),
			map[string]any{"detected": gv.Raw, "supported": "4.7+"})
	}
	if warn != "" {
		warnings = append(warnings, warn)
	}

	// Step 4: install/upgrade + enable the embedded plugin.
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
	// 以 cmd.Context() 为父上下文：生产上 cobra 默认 Background（行为与
	// 原来一致，信号仍生效）；测试可 SetContext 一个可取消上下文，让
	// foreground 模式（打印 ready 后阻塞等中断）能够干净退出。
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// keptEditors counts the editors an --upgrade-daemon swap preserved;
	// step 5 waits for their sessions to reconnect before deciding to spawn.
	// keptSessions 保留这些会话的身份（editor_pid/project_path/plugin_version）：
	// 「本工程编辑器升级前已连接」是升级后守卫改判「升级成功+warning」的
	// 证据（需求 upgrade-daemon-unconnected-editor §3.2）。
	keptEditors := 0
	var keptSessions []map[string]any
	daemonUpgraded := false
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
			// and continue the launch normally. Without an explicit
			// --ws-port the replacement inherits the old daemon's ACTUAL
			// WS port: the kept editors' plugins (and this project's port
			// file) still point there, so falling back to the flag default
			// would leave them reconnecting forever (launch then
			// double-spawns the editor) and could hijack another project's
			// daemon on the default port.
			opts.wsPort = upgradeWSPort(opts.wsPortSet, opts.wsPort, mismatchErr.RunningWSPort)
			cfg.WSPort = opts.wsPort
			kept, uerr := shutdownDaemonKeepEditors(opts.httpPort)
			if uerr != nil {
				return jsonError(cmd, "DAEMON_UPGRADE_FAILED", uerr.Error(),
					map[string]any{"http_port": opts.httpPort, "retryable": true})
			}
			warnings = append(warnings, fmt.Sprintf(
				"old daemon (version %s) on http port %d shut down; %d editor(s) kept running — major.minor-compatible plugins reconnect to the new daemon automatically, incompatible ones need `godot-ai-cli plugin install --project <dir>` plus an editor restart",
				mismatchErr.RunningVersion, opts.httpPort, len(kept)))
			if _, err := daemonctl.EnsureRunning(ctx, cfg); err != nil {
				return jsonError(cmd, "DAEMON_START_FAILED",
					fmt.Sprintf("old daemon stopped, but the new daemon did not come up: %v", err), nil)
			}
			keptSessions = kept
			keptEditors = len(kept)
			daemonUpgraded = true
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
				// Same WS-port inheritance as the DAEMON_MISMATCH branch
				// above, sourced from the drifted daemon's live status
				// BEFORE it is shut down (a failed probe advertises no
				// port, so the requested value stands).
				liveWS, _ := liveDaemonWSPort(opts.httpPort)
				opts.wsPort = upgradeWSPort(opts.wsPortSet, opts.wsPort, liveWS)
				cfg.WSPort = opts.wsPort
				kept, uerr := shutdownDaemonKeepEditors(opts.httpPort)
				if uerr != nil {
					return jsonError(cmd, "DAEMON_UPGRADE_FAILED", uerr.Error(),
						map[string]any{"http_port": opts.httpPort, "retryable": true})
				}
				warnings = append(warnings, fmt.Sprintf(
					"old daemon (version %s) on http port %d shut down; %d editor(s) kept running — major.minor-compatible plugins reconnect to the new daemon automatically, incompatible ones need `godot-ai-cli plugin install --project <dir>` plus an editor restart",
					running, opts.httpPort, len(kept)))
				if _, err := daemonctl.EnsureRunning(ctx, cfg); err != nil {
					return jsonError(cmd, "DAEMON_START_FAILED",
						fmt.Sprintf("old daemon stopped, but the new daemon did not come up: %v", err), nil)
				}
				keptSessions = kept
				keptEditors = len(kept)
				daemonUpgraded = true
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
	// 「这个 daemon 是哪个 CLI 起的」只有记录能回答（需求 R-5 附带），但只能
	// 给我们**自己起的** daemon 写：foreground 的 in-process daemon 属于本
	// 进程；detached 路径 spawn 出去的 serve 由它自己补写（见 serve.go）。
	// 收养别人起的 daemon 时绝不能回写——那会把「旧版 CLI 起的 daemon」谎报
	// 成本版，恰恰破坏该字段的用途。
	if inProcess != nil {
		recordDaemonCLIVersion(inProcess.HTTPPort())
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

		// 未连接编辑器守卫（需求 editor-attach-and-daemon-discovery §3.2）：
		// 进程扫描发现该工程的编辑器活着、但没连任何 daemon 时，再 spawn
		// 就是双开（场景锁/保存互覆）——拒绝并指向 launch --attach。
		// --attach 本身就是这种现场的入口，跳过守卫与 spawn；
		// --force-spawn 保持既有「自担风险」语义。
		if !opts.attach && !opts.forceSpawn {
			if editors, scanErr := unconnectedEditorsReport(projectDir); len(editors) > 0 {
				// 升级后守卫（需求 upgrade-daemon-unconnected-editor §3.2）：
				// 本工程编辑器在升级前连着旧 daemon（keptSessions 里有它的
				// 会话），daemon 替换是本命令已完成的既定事实——整体 error
				// 会让调用方误判升级失败。改判「升级成功 + warning」。
				if kept := keptSessionForProject(keptSessions, projectDir); kept != nil {
					return printJSON(out, upgradeKeptEditorPayload(
						opts, projectDir, editors, kept, warnings, scanErr, install, plan), false)
				}
				live := liveDaemonEntries(knownDaemonsReport(opts.httpPort))
				data := map[string]any{
					"editors":      editors,
					"live_daemons": live,
					"suggest": []string{
						"godot-ai-cli launch --project " + projectDir + " --attach",
						fmt.Sprintf("godot-ai-cli status --http-port %v", opts.httpPort),
					},
					"retryable": false,
					"scan_note": scanErr,
					// 命令报错但副作用可能已生效（需求
					// upgrade-daemon-unconnected-editor §3.1）：升级过的
					// 运行必须把新 daemon 的身份放进错误数据。
					"daemon_upgraded": daemonUpgraded,
				}
				message := fmt.Sprintf(
					"检测到工程 %s 的编辑器进程已打开，但它没有连接任何 daemon；launch 会再开一个编辑器实例（场景锁/保存互相覆盖风险）", projectDir)
				if daemonUpgraded {
					data["daemon"] = map[string]any{
						"http_port": opts.httpPort, "ws_port": opts.wsPort,
						"version": pluginmeta.PluginVersion(),
					}
					data["kept_editors"] = editors
					message = fmt.Sprintf(
						"daemon 已换成本版 %s，但保留编辑器未能重连；", pluginmeta.PluginVersion()) + message
				}
				// 与被拒握手同源的现场（需求 §3.3）：「为什么保留编辑器
				// 连不上」在一条命令里自解释。
				if rj := recentRejectionsFrom(opts.httpPort); len(rj) > 0 {
					data["recent_rejections"] = rj
				}
				return jsonError(cmd, "EDITOR_OPEN_UNCONNECTED", message, data)
			} else if scanErr != "" {
				warnings = append(warnings, scanErr)
			}
		}

		// Pin the daemon ports PER PROJECT: the plugin resolves ports as
		// project file > EditorSettings > default, so this file (written for
		// default ports too, for determinism) replaces the old global
		// EditorSettings overrides entirely. --attach 依赖这个文件让已打开的
		// 编辑器在下一轮 recheck 时收养本 daemon。
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

		// --attach 不 spawn：等已打开编辑器的插件重连（步骤 6 的等待不变）。
		if !opts.attach {
			editorPID, err = launchEditorFn(godot.LaunchOptions{
				Binary:     binary,
				ProjectDir: projectDir,
				Headless:   opts.headless,
			})
			if err != nil {
				return jsonError(cmd, "EDITOR_LAUNCH_FAILED", err.Error(), nil)
			}
		}
	}

	// Step 6: wait for THIS project's plugin handshake — a session belonging
	// to another project must never satisfy the wait, or launch would report
	// ready while its own editor never connected.
	session, err := waitForSession(ctx, opts.httpPort, projectDir, opts.wait)
	if err != nil {
		// --attach 超时是显式失败（EDITOR_NOT_CONNECTED），绝不静默成功——
		// 调用方需要知道「等不到已打开的编辑器」而不是把超时的 daemon
		// 当作 ready 继续（需求 editor-attach-and-daemon-discovery §3.3）。
		if opts.attach {
			data := map[string]any{
				"http_port": opts.httpPort,
				"ws_port":   opts.wsPort,
				"wait_s":    opts.wait.Seconds(),
				"retryable": true,
				"hint": ("editor not connected within the wait window — confirm the editor is still open and its Godot AI panel shows no incompatible-server block; " +
					"when the panel shows a version mismatch, fully quit and reopen the editor (a plugin reload does not replace loaded plugin code)"),
			}
			if rj := recentRejectionsFrom(opts.httpPort); len(rj) > 0 {
				data["recent_rejections"] = rj
			}
			return jsonError(cmd, "EDITOR_NOT_CONNECTED",
				fmt.Sprintf("no plugin session for this project connected within %s", opts.wait), data)
		}
		return jsonError(cmd, "LAUNCH_TIMEOUT",
			fmt.Sprintf("no plugin session for this project connected within %s — the editor may still be starting; retry or raise --wait", opts.wait),
			map[string]any{
				"retryable": true,
				// 等待超时同样带拒绝线索（需求 handshake-rejection-visibility
				// §4.2）：spawn 出去的编辑器若因版本不匹配被拒握手，这里能
				// 直接看到原因与修法，而不是盲等。
				"recent_rejections": recentRejectionsFrom(opts.httpPort),
			})
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
		warnings = append(warnings, pluginStaleLaunchWarning(session, opts.httpPort))
	}

	if warnings == nil {
		warnings = []string{}
	}
	// The plugin step as structured data (requirement: a rewrite of a tracked
	// addon tree must be visible as fields, not only inside a warning string).
	// The counts come from the pre-flight plan, which lists exactly what
	// EnsureInstalled writes.
	result := map[string]any{
		"status":         "ready",
		"session_id":     session["session_id"],
		"godot_version":  session["godot_version"],
		"project":        projectDir,
		"editor_pid":     session["editor_pid"],
		"headless":       opts.headless,
		"spawned_editor": editorPID != 0,
		"daemon":         map[string]any{"http_port": opts.httpPort, "ws_port": opts.wsPort},
		"plugin_version": pluginmeta.PluginVersion(),
		"plugin": map[string]any{
			"installed":     install.Installed,
			"upgraded":      install.Upgraded,
			"from":          install.PreviousVersion,
			"to":            install.Version,
			"files_changed": len(plan.WouldUpdate),
			"files_created": len(plan.WouldCreate),
			"git_dirty":     plan.Git.DirtyAfter,
			"git_available": plan.Git.Available,
		},
		"warnings": warnings,
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

// pluginStaleLaunchWarning 渲染 launch 复用陈旧会话时的警告：文案与方向
// 全部来自 status 共用的 pluginStaleHint（禁止复制两份逻辑，需求 R-5）——
// 插件比 daemon 新时指向 `launch --upgrade-daemon`，插件更旧时才是
// `plugin install` + 重启编辑器。对齐目标版本取 daemon 实时上报值，探测
// 失败时回落到本 CLI 的内置插件版本（否则 daemon 是旧构建时会渲染出
// "plugin v3.2.9 ≠ bundled v3.2.9" 这种无意义文案）。
func pluginStaleLaunchWarning(session map[string]any, httpPort int) string {
	daemonVersion := pluginmeta.PluginVersion()
	if running, ok := probeDaemonHealth(httpPort); ok && running != "" {
		daemonVersion = running
	}
	return pluginStaleNote(fmt.Sprint(session["plugin_version"]), daemonVersion)
}

// keptSessionForProject 在 --upgrade-daemon 保留的会话清单里找本工程的
// 那条——它是「升级前本工程编辑器已连接」的证据（需求
// upgrade-daemon-unconnected-editor §3.2 的升级前/后守卫区分）。
func keptSessionForProject(kept []map[string]any, projectDir string) map[string]any {
	for _, sess := range kept {
		if pp, _ := sess["project_path"].(string); pp != "" && sameProjectPath(pp, projectDir) {
			return sess
		}
	}
	return nil
}

// upgradeKeptEditorPayload 渲染升级后守卫的 ok 载荷：daemon 已换成本版、
// 保留编辑器未在重连宽限内重连（多数是内存插件与新 daemon major.minor
// 错配）。主副作用（换 daemon）已完成，所以这是 status:ok + 结构化数据，
// 不是错误——调用方据此知道要重启的是编辑器，而不是重跑升级（需求
// upgrade-daemon-unconnected-editor §3.1/§3.2）。
func upgradeKeptEditorPayload(
	opts launchOptions, projectDir string, editors []map[string]any,
	kept map[string]any, warnings []string, scanErr string,
	install plugin.InstallResult, plan plugin.Plan,
) map[string]any {
	newVersion := pluginmeta.PluginVersion()
	inMemory := fmt.Sprint(kept["plugin_version"])
	reconnectWarning := fmt.Sprintf(
		"kept editor (pid %v) did not reconnect within %s — its in-memory plugin (%s) could not re-attach to the new daemon %s; the daemon upgrade itself succeeded",
		kept["editor_pid"], keptEditorReconnectGrace, inMemory, newVersion)
	warnings = append(warnings, reconnectWarning)
	payload := map[string]any{
		"status":             "ok",
		"daemon_upgraded":    true,
		"editor_reconnected": false,
		"project":            projectDir,
		"daemon": map[string]any{
			"http_port": opts.httpPort, "ws_port": opts.wsPort, "version": newVersion,
		},
		"kept_editors": editors,
		"next_steps": []string{
			"完全退出 Godot 编辑器（内存插件 " + inMemory + " 与新 daemon " + newVersion + " 不兼容时 reload-plugin 不会替换已加载的插件代码）",
			"重新打开（建议用 godot-ai-cli launch --project <dir> 以便复用同一 daemon）",
		},
		"warnings": warnings,
		// 插件步骤保持结构化可见（与 ready 载荷同一形状）：升级-daemon 往往
		// 伴随 addons 树重写，调用方不该因为走了守卫就丢失它。
		"plugin": map[string]any{
			"installed":     install.Installed,
			"upgraded":      install.Upgraded,
			"from":          install.PreviousVersion,
			"to":            install.Version,
			"files_changed": len(plan.WouldUpdate),
			"files_created": len(plan.WouldCreate),
			"git_dirty":     plan.Git.DirtyAfter,
			"git_available": plan.Git.Available,
		},
	}
	if scanErr != "" {
		payload["scan_note"] = scanErr
	}
	// 与被拒握手同源的现场（需求 §3.3）：探针自阻自报 / 握手拒绝都在这里。
	if rj := recentRejectionsFrom(opts.httpPort); len(rj) > 0 {
		payload["recent_rejections"] = rj
	}
	return payload
}

// upgradeWSPort decides the WS port of the daemon that REPLACES the old one
// in an --upgrade-daemon swap. An explicit --ws-port always wins; otherwise
// the new daemon inherits the old daemon's actual WS port (runningWSPort),
// because the kept editors' plugins — and this project's
// .godot/godot_ai_ports.json — still point there. A non-positive
// runningWSPort means the old daemon did not advertise one (dead probe),
// so the requested value stands.
func upgradeWSPort(explicit bool, requested, runningWSPort int) int {
	if !explicit && runningWSPort > 0 {
		return runningWSPort
	}
	return requested
}

// liveDaemonWSPort reads the daemon's actual WS port from its live status
// (a recorded identity file may predate a restart on other ports). ok=false
// when the daemon does not answer or advertises no ws_port.
func liveDaemonWSPort(httpPort int) (wsPort int, ok bool) {
	status, ok := probeKnownDaemonGET(httpPort, "/godot-ai/status")
	if !ok {
		return 0, false
	}
	n, ok := status["ws_port"].(float64)
	if !ok || n <= 0 {
		return 0, false
	}
	return int(n), true
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
