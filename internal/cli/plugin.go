package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/mimajiushi/godot-ai-cli/plugin"
)

// newPluginCommand groups plugin-management subcommands.
func newPluginCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Manage the godot_ai editor plugin in a Godot project",
	}
	cmd.AddCommand(newPluginInstallCommand())
	return cmd
}

// newPluginInstallCommand installs/upgrades and enables the embedded
// plugin in one project.
func newPluginInstallCommand() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "install --project PATH",
		Short: "Install or upgrade the godot_ai plugin into a Godot project",
		Long: `plugin install extracts the plugin embedded in this binary into
<project>/addons/godot_ai (upgrading in place when versions differ) and
enables it in project.godot.

It never deletes the addon directory itself and never removes files that
do not belong to the plugin.

Examples:
  godot-ai-cli plugin install --project C:/games/rpg`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			projectDir, err := filepath.Abs(project)
			if err != nil {
				return jsonError(cmd, "INVALID_PROJECT", err.Error(), nil)
			}
			if info, err := os.Stat(filepath.Join(projectDir, "project.godot")); err != nil || info.IsDir() {
				return jsonError(cmd, "INVALID_PROJECT",
					fmt.Sprintf("%s does not contain a project.godot file", projectDir), nil)
			}
			result, err := plugin.EnsureInstalled(projectDir)
			if err != nil {
				return jsonError(cmd, "PLUGIN_INSTALL_FAILED", err.Error(), nil)
			}
			return printJSON(cmd.OutOrStdout(), map[string]any{
				"installed":        result.Installed,
				"upgraded":         result.Upgraded,
				"version":          result.Version,
				"previous_version": result.PreviousVersion,
				"path":             result.Path,
				"enabled":          result.Enabled,
			}, false)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Godot project directory containing project.godot (required)")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}
