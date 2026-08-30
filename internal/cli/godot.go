package cli

import (
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
	return cmd
}

// newGodotDetectCommand lists every resolvable Godot binary with its
// version and compatibility verdict, plus which one Find would select.
func newGodotDetectCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "detect",
		Short: "List candidate Godot binaries and which one would be used",
		Long: `godot detect probes the same sources the launch command uses —
GODOT_BIN, PATH, and the conventional per-OS install locations — runs
--version on each binary found, and reports compatibility.

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
