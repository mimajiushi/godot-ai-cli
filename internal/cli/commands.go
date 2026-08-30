// Op surface discovery: `commands` lists every OpSpec-backed subcommand
// as plain text for humans or full-fidelity JSON for agents/scripts.
package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mimajiushi/godot-ai-cli/internal/ops"
)

// opJSON is the wire shape of one op in `commands --json`.
type opJSON struct {
	Domain        string      `json:"domain"`
	Name          string      `json:"name"`
	PluginCommand string      `json:"plugin_command"`
	Summary       string      `json:"summary"`
	TimeoutSec    float64     `json:"timeout_sec"`
	Write         bool        `json:"write"`
	Params        []paramJSON `json:"params"`
}

// paramJSON is the wire shape of one param in `commands --json`.
type paramJSON struct {
	Flag     string `json:"flag"`
	Param    string `json:"param"`
	Kind     string `json:"kind"`
	Required bool   `json:"required"`
	Default  string `json:"default"`
	Usage    string `json:"usage"`
}

// newCommandsCommand builds the `commands` discovery subcommand.
func newCommandsCommand() *cobra.Command {
	var (
		domainFilter string
		jsonOut      bool
	)
	cmd := &cobra.Command{
		Use:   "commands",
		Short: "List every op subcommand (plain text or JSON)",
		Long: `commands exposes the full op surface generated from the internal op
table: one entry per <domain> <op> leaf with its plugin command, timeout,
write gate, and typed params.

Plain text groups ops under ## <domain> headers; --json emits the complete
spec for machine consumption (honors the root --pretty flag); --domain
narrows the listing to a single domain.

Examples:
  godot-ai-cli commands
  godot-ai-cli commands --domain node
  godot-ai-cli commands --json | jq '.ops[].plugin_command'
  godot-ai-cli --pretty commands --json --domain scene`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			list := sortedOps()
			if domainFilter != "" {
				list = filterDomain(list, domainFilter)
				if len(list) == 0 {
					return fmt.Errorf("unknown domain %q (valid: %s)",
						domainFilter, strings.Join(ops.SortedDomainsWithOps(), ", "))
				}
			}
			if jsonOut {
				return printJSON(cmd.OutOrStdout(),
					map[string]any{"ops": toOpJSON(list)}, prettyOutput)
			}
			return printOpsText(cmd.OutOrStdout(), list)
		},
	}
	cmd.Flags().StringVar(&domainFilter, "domain", "", "only list ops of one domain (e.g. node)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the full op spec as JSON")
	return cmd
}

// sortedOps returns All() in stable display order: domain (table order),
// then op name.
func sortedOps() []ops.OpSpec {
	rank := map[string]int{}
	for i, d := range ops.Domains() {
		rank[d] = i
	}
	list := ops.All()
	sort.SliceStable(list, func(i, j int) bool {
		if rank[list[i].Domain] != rank[list[j].Domain] {
			return rank[list[i].Domain] < rank[list[j].Domain]
		}
		return list[i].Name < list[j].Name
	})
	return list
}

// filterDomain keeps only the ops of one domain.
func filterDomain(list []ops.OpSpec, domain string) []ops.OpSpec {
	var out []ops.OpSpec
	for _, op := range list {
		if op.Domain == domain {
			out = append(out, op)
		}
	}
	return out
}

// toOpJSON converts specs to their JSON wire shape (params always an
// array, never null).
func toOpJSON(list []ops.OpSpec) []opJSON {
	out := make([]opJSON, 0, len(list))
	for _, op := range list {
		params := make([]paramJSON, 0, len(op.Params))
		for _, p := range op.Params {
			params = append(params, paramJSON{
				Flag:     p.Flag,
				Param:    p.Param,
				Kind:     p.Kind,
				Required: p.Required,
				Default:  p.Default,
				Usage:    p.Usage,
			})
		}
		out = append(out, opJSON{
			Domain:        op.Domain,
			Name:          op.Name,
			PluginCommand: op.PluginCommand,
			Summary:       op.Summary,
			TimeoutSec:    op.Timeout.Seconds(),
			Write:         op.Write,
			Params:        params,
		})
	}
	return out
}

// printOpsText renders the plain-text listing: ops grouped under
// ## <domain> headers, one line per op with write gate and timeout.
func printOpsText(w io.Writer, list []ops.OpSpec) error {
	lastDomain := ""
	for _, op := range list {
		if op.Domain != lastDomain {
			if lastDomain != "" {
				if _, err := fmt.Fprintln(w); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintf(w, "## %s\n", op.Domain); err != nil {
				return err
			}
			lastDomain = op.Domain
		}
		line := fmt.Sprintf("%s %s — %s", op.Domain, op.Name, op.Summary)
		if op.Write {
			line += " [write]"
		}
		line += fmt.Sprintf(" (timeout %ds)", int64(op.Timeout.Seconds()))
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}
