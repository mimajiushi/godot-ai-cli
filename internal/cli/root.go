// Package cli builds the cobra command tree of godot-ai-cli and
// centralizes shared output/error conventions (English JSON on stdout,
// non-zero exit code on failure).
package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mimajiushi/godot-ai-cli/internal/ops"
	"github.com/mimajiushi/godot-ai-cli/internal/pluginmeta"
	"github.com/mimajiushi/godot-ai-cli/internal/update"
	"github.com/mimajiushi/godot-ai-cli/internal/version"
)

// NewRootCommand builds the root command with all subcommands attached.
// Keeping construction in a function (not package init) lets tests build
// isolated trees.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "godot-ai-cli",
		Short: "Drive a live Godot editor from the command line (no MCP required)",
		Long: `godot-ai-cli talks to the godot_ai editor plugin over a local
WebSocket backend. It can install the plugin into a project, launch the
Godot editor itself (headed or headless), and expose every editor
operation as a subcommand that prints JSON.

Start here:
  godot-ai-cli launch --project /path/to/project
  godot-ai-cli status
  godot-ai-cli scene get-hierarchy

Daemon port memory: launch/serve record the daemon's ports in
<user cache dir>/godot-ai-cli/last-daemon.json. Every daemon-facing
command resolves the HTTP port in this order:
  explicit --http-port flag > recorded last-daemon port > 8000 (default)
When the recorded port is unreachable, the default 8000 is tried as a
fallback before the command reports the daemon as not running. stop
removes the record when it stops that daemon.`,
		// Cobra auto-adds -v/--version from this field; the template below
		// extends the output with Godot compatibility information.
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		// A Windows self-update leaves the previous binary as <exe>.old
		// next to the executable (a running exe cannot be deleted). Remove
		// the leftover at every startup — best-effort and silent.
		PersistentPreRun: func(_ *cobra.Command, _ []string) {
			update.CleanupStaleBinary()
		},
	}
	root.SetVersionTemplate(versionTemplate())
	root.PersistentFlags().BoolVar(&prettyOutput, "pretty", false, "indent JSON output for humans")

	root.AddCommand(newVersionCommand())
	root.AddCommand(newServeCommand())
	root.AddCommand(newLaunchCommand())
	root.AddCommand(newStatusCommand())
	root.AddCommand(newStopCommand())
	root.AddCommand(newGodotCommand())
	root.AddCommand(newPluginCommand())
	root.AddCommand(newImageCommand())
	root.AddCommand(newCallCommand())
	root.AddCommand(newCommandsCommand())
	root.AddCommand(newUpdateCommand())
	for _, domainCmd := range newDomainCommands() {
		root.AddCommand(domainCmd)
	}
	return root
}

// Execute runs the root command and maps the outcome to a process exit code.
func Execute() int {
	return execute(NewRootCommand())
}

// execute runs cmd and maps the outcome to a process exit code. A failure
// the subcommand did not already report as a JSON envelope (cobra-level
// usage errors: unknown flag, bad args count, missing required flag, ...)
// gets the standard USAGE_ERROR envelope on stdout per the output contract
// (troubleshooting.md / CONTRIBUTING.md). Nothing human-readable is printed
// on top — the envelope carries code+message, and a duplicated stderr
// "Error:" line would corrupt the output of callers merging 2>&1.
func execute(cmd *cobra.Command) int {
	if err := cmd.Execute(); err != nil {
		var reported *reportedError
		if !errors.As(err, &reported) {
			_ = printJSON(cmd.OutOrStdout(), map[string]any{
				"status": "error",
				"error": map[string]any{
					"code":    "USAGE_ERROR",
					"message": err.Error(),
					"data":    map[string]any{},
				},
			}, false)
		}
		return 1
	}
	return 0
}

// versionTemplate renders -v/--version with the supported Godot range and
// the bundled plugin version (which upstream godot-ai release the vendored
// editor plugin was forked from) so users can immediately tell whether
// their editor is compatible.
func versionTemplate() string {
	return fmt.Sprintf(`godot-ai-cli version {{.Version}}
  release source:      https://github.com/%s/%s
  protocol version:    %d
  supported Godot:     %s+ (%s+ recommended)
  bundled plugin:      godot-ai v%s (forked, strict version match required)
  plugin command coverage: %d ops
`, version.RepoOwner, version.RepoName, version.ProtocolVersion,
		version.SupportedGodotMin, version.SupportedGodotRecommended,
		pluginmeta.PluginVersion(), len(ops.All()))
}

// newVersionCommand provides the explicit `version` subcommand mirroring
// the -v/--version flag output.
func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI version, supported Godot versions, and bundled plugin version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tpl := versionTemplate()
			data := struct{ Version string }{Version: version.Version}
			return tplExecute(cmd.OutOrStdout(), tpl, data)
		},
	}
}
