package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
	"github.com/mimajiushi/godot-ai-cli/internal/pluginmeta"
	"github.com/mimajiushi/godot-ai-cli/plugin"
)

// newPluginCommand groups plugin-management subcommands.
func newPluginCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Manage the godot_ai editor plugin in a Godot project",
	}
	cmd.AddCommand(newPluginInstallCommand())
	cmd.AddCommand(newPluginStatusCommand())
	return cmd
}

// resolveProjectDir validates --project and returns its absolute path.
func resolveProjectDir(project string) (string, error) {
	projectDir, err := filepath.Abs(project)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(filepath.Join(projectDir, "project.godot")); err != nil || info.IsDir() {
		return "", fmt.Errorf("%s does not contain a project.godot file", projectDir)
	}
	return projectDir, nil
}

// newPluginInstallCommand installs/upgrades and enables the embedded
// plugin in one project.
func newPluginInstallCommand() *cobra.Command {
	var (
		project  string
		dryRun   bool
		version  string
		httpPort int
	)
	cmd := &cobra.Command{
		Use:   "install --project PATH",
		Short: "Install or upgrade the godot_ai plugin into a Godot project",
		Long: `plugin install extracts the plugin embedded in this binary into
<project>/addons/godot_ai (upgrading in place when versions differ) and
enables it in project.godot.

It never deletes the addon directory itself and never removes files that do
not belong to the plugin.

This CLI embeds exactly ONE plugin version, so --version is a guard rather
than a picker: asking for anything else fails with PLUGIN_VERSION_UNSUPPORTED.

After a successful install it ALSO probes the local daemon (best-effort;
--http-port defaults to the recorded/default port): when a connected session
or a rejected handshake for THIS project reports an in-memory plugin version
different from the freshly installed disk version, the payload carries a
PROJECT_PLUGIN_MISMATCH warning plus next_steps — installing files does not
replace the plugin code an already-open editor loaded, and only a full editor
restart does（需求 handshake-rejection-visibility §4.2）.

--dry-run prints the same write plan as ` + "`launch --dry-run`" + ` and touches
nothing.

Examples:
  godot-ai-cli plugin install --project C:/games/rpg
  godot-ai-cli plugin install --project C:/games/rpg --dry-run`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			projectDir, err := resolveProjectDir(project)
			if err != nil {
				return jsonError(cmd, "INVALID_PROJECT", err.Error(), nil)
			}
			if version != "" && version != plugin.PluginVersion() {
				return jsonError(cmd, "PLUGIN_VERSION_UNSUPPORTED",
					fmt.Sprintf("this CLI bundles only plugin %s — it cannot install %s; use the CLI release that bundles %s, or drop --version",
						plugin.PluginVersion(), version, version),
					map[string]any{"requested_version": version, "bundled_version": plugin.PluginVersion()})
			}
			if dryRun {
				plan, err := plugin.Preview(projectDir)
				if err != nil {
					return jsonError(cmd, "PLUGIN_PLAN_FAILED", err.Error(), nil)
				}
				return printJSON(cmd.OutOrStdout(), map[string]any{
					"status":  "ok",
					"dry_run": true,
					"plugin":  plan.JSON(),
				}, false)
			}
			result, err := plugin.EnsureInstalled(projectDir)
			if err != nil {
				return jsonError(cmd, "PLUGIN_INSTALL_FAILED", err.Error(), nil)
			}
			payload := map[string]any{
				"installed":        result.Installed,
				"upgraded":         result.Upgraded,
				"version":          result.Version,
				"previous_version": result.PreviousVersion,
				"path":             result.Path,
				"enabled":          result.Enabled,
			}
			// 磁盘已对齐但编辑器内存可能仍是旧代码：探测 daemon（best-effort，
			// 没有 daemon/探测失败都只是少一句 warning，绝不阻断安装结果）。
			if port, _, ok := resolveDaemonPort(cmd); ok {
				inMemory := inMemoryPluginVersions(port, projectDir)
				if warning, nextSteps := editorRestartWarning(result.Version, inMemory); warning != "" {
					payload["warning"] = warning
					payload["next_steps"] = nextSteps
					payload["in_memory_plugin"] = inMemory
				}
			}
			return printJSON(cmd.OutOrStdout(), payload, false)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Godot project directory containing project.godot (required)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be written (files + git impact) without touching the project")
	cmd.Flags().StringVar(&version, "version", "", "assert this exact plugin version (this CLI bundles only one — any other value fails)")
	cmd.Flags().IntVar(&httpPort, "http-port", daemon.DefaultHTTPPort, "daemon HTTP port for the post-install in-memory plugin check")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}

// newPluginStatusCommand reports the project plugin's version against the
// bundled one plus the write plan, WITHOUT modifying anything: the read-only
// companion to launch's --no-plugin-upgrade gate.
func newPluginStatusCommand() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "status --project PATH",
		Short: "Report the project plugin version, compatibility, and pending changes",
		Long: `plugin status inspects <project>/addons/godot_ai and
<project>/project.godot without writing anything, and reports:

  - installed / installed_version / enabled
  - bundled_version (the plugin this CLI would install) and version_match
  - compatible: major.minor equality (a patch drift still handshakes)
  - plan: would_update / would_create (+ git tracked/untracked counts)

The git block degrades to {"available": false, "reason": ...} when git is
missing or the project is not a work tree — the plugin answer itself never
depends on git.

Examples:
  godot-ai-cli plugin status --project C:/games/rpg`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			projectDir, err := resolveProjectDir(project)
			if err != nil {
				return jsonError(cmd, "INVALID_PROJECT", err.Error(), nil)
			}
			plan, err := plugin.Preview(projectDir)
			if err != nil {
				return jsonError(cmd, "PLUGIN_PLAN_FAILED", err.Error(), nil)
			}
			return printJSON(cmd.OutOrStdout(), map[string]any{
				"status":     "ok",
				"project":    projectDir,
				"installed":  plan.InstalledVersion != "",
				"enabled":    !plan.WouldEnablePlugin,
				"compatible": pluginVersionsCompatible(plan.InstalledVersion, plan.BundledVersion),
				"plugin":     plan.JSON(),
			}, false)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Godot project directory containing project.godot (required)")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}

// pluginVersionsCompatible reports major.minor equality between the installed
// and bundled plugin versions — the same rule the handshake applies. Nothing
// installed yet counts as compatible: there is nothing to refuse, the install
// fixes it.
func pluginVersionsCompatible(installed, bundled string) bool {
	if installed == "" {
		return true
	}
	iv, ierr := pluginmeta.ParseSemver(installed)
	bv, berr := pluginmeta.ParseSemver(bundled)
	if ierr != nil || berr != nil {
		return false
	}
	return pluginmeta.Compatible(iv, bv)
}
