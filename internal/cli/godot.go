package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mimajiushi/godot-ai-cli/internal/godot"
)

// newGodotCommand groups Godot-binary related subcommands.
func newGodotCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "godot",
		Short: "Inspect the Godot binaries godot-ai-cli can use",
	}
	cmd.AddCommand(newGodotDetectCommand())
	cmd.AddCommand(newGodotUseCommand())
	return cmd
}

// newGodotDetectCommand lists every resolvable Godot binary with its
// version and compatibility verdict, plus which one Find would select.
func newGodotDetectCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "detect",
		Short: "List candidate Godot binaries and which one would be used",
		Long: `godot detect probes the same sources the launch command uses —
GODOT_BIN, the default saved by "godot use", PATH, and the conventional
per-OS install locations — runs --version on each binary found, and reports
compatibility.

Examples:
  godot-ai-cli godot detect`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			type candidate struct {
				Path       string `json:"path"`
				Version    string `json:"version"`
				Mono       bool   `json:"mono"`
				Compatible bool   `json:"compatible"`
				Warning    string `json:"warning,omitempty"`
				Error      string `json:"error,omitempty"`
			}

			selected, _ := godot.Find("")
			candidates := []candidate{}
			for _, path := range godot.Candidates() {
				entry := candidate{Path: path}
				v, err := godot.VersionFromBinary(path)
				if err != nil {
					entry.Error = err.Error()
				} else {
					entry.Version = v.String()
					entry.Mono = v.Mono
					warn, err := godot.CheckCompatibility(v)
					entry.Compatible = err == nil
					entry.Warning = warn
					if err != nil {
						entry.Error = err.Error()
					}
				}
				candidates = append(candidates, entry)
			}

			var selectedOut any
			if selected != "" {
				selectedOut = selected
			}
			return printJSON(cmd.OutOrStdout(), map[string]any{
				"candidates": candidates,
				"selected":   selectedOut,
			}, false)
		},
	}
}

// newGodotUseCommand gets, sets, or clears the default Godot binary that
// Find consults between GODOT_BIN and the PATH lookup. Setting it once
// frees users with custom install layouts from exporting GODOT_BIN in
// every shell.
func newGodotUseCommand() *cobra.Command {
	var clear bool
	cmd := &cobra.Command{
		Use:   "use [path]",
		Short: "Get, set, or clear the saved default Godot binary",
		Long: `godot use manages the default Godot binary persisted in
<user config dir>/godot-ai-cli/godot-bin.json. launch and detect consult
the saved path after --godot and GODOT_BIN but before PATH and the
conventional install locations.

With a path, the binary is validated (it must run and report a Godot
version) before being saved; with no arguments the current record is
printed; --clear removes it.

Examples:
  godot-ai-cli godot use                           # show the saved default
  godot-ai-cli godot use D:/tools/Godot/Godot.exe  # validate and save
  godot-ai-cli godot use --clear                   # forget the saved default`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case clear && len(args) == 1:
				return jsonError(cmd, "INVALID_ARGS", "--clear takes no path argument", nil)
			case clear:
				if err := godot.ClearDefaultBinary(); err != nil {
					return jsonError(cmd, "GODOT_BIN_CLEAR_FAILED", err.Error(), nil)
				}
				return printJSON(cmd.OutOrStdout(), map[string]any{
					"status":  "ok",
					"cleared": true,
				}, false)
			case len(args) == 0:
				var savedOut any
				if saved, ok := godot.LoadDefaultBinary(); ok {
					savedOut = saved
				}
				return printJSON(cmd.OutOrStdout(), map[string]any{
					"status":    "ok",
					"godot_bin": savedOut,
				}, false)
			default:
				path := args[0]
				v, err := godot.VersionFromBinary(path)
				if err != nil {
					return jsonError(cmd, "GODOT_BIN_INVALID",
						fmt.Sprintf("%s is not a runnable Godot binary: %v", path, err), nil)
				}
				if err := godot.SaveDefaultBinary(path); err != nil {
					return jsonError(cmd, "GODOT_BIN_SAVE_FAILED", err.Error(), nil)
				}
				// Reload for the persisted absolute form.
				saved, _ := godot.LoadDefaultBinary()
				return printJSON(cmd.OutOrStdout(), map[string]any{
					"status":    "ok",
					"godot_bin": saved,
					"version":   v.String(),
					"mono":      v.Mono,
				}, false)
			}
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "remove the saved default Godot binary")
	return cmd
}
