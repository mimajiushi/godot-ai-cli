// Package cli builds the cobra command tree of godot-ai-cli and
// centralizes shared output/error conventions (English JSON on stdout,
// non-zero exit code on failure).
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mimajiushi/godot-ai-cli/internal/ops"
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
	if err := NewRootCommand().Execute(); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

// versionTemplate renders -v/--version with the supported Godot range so
// users can immediately tell whether their editor is compatible.
func versionTemplate() string {
	return fmt.Sprintf(`godot-ai-cli version {{.Version}}
  release source:      https://github.com/%s/%s
  protocol version:    %d
  supported Godot:     %s+ (%s+ recommended)
  plugin command coverage: %d ops
`, version.RepoOwner, version.RepoName, version.ProtocolVersion,
		version.SupportedGodotMin, version.SupportedGodotRecommended,
		len(ops.All()))
}

// newVersionCommand provides the explicit `version` subcommand mirroring
// the -v/--version flag output.
func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI version and supported Godot versions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tpl := versionTemplate()
			data := struct{ Version string }{Version: version.Version}
			return tplExecute(cmd.OutOrStdout(), tpl, data)
		},
	}
}
