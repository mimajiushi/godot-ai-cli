// Output helpers shared by all subcommands: JSON results on stdout and
// diagnostics on stderr, keeping agent consumption predictable. Errors are
// reported solely through the stdout JSON envelope (never a stderr "Error:"
// line), so output stays single-JSON even under 2>&1 merging.
package cli

import (
	"encoding/json"
	"io"
	"text/template"
)

// printJSON writes v as JSON to w. When pretty is true the output is
// indented for humans; otherwise it is a single line for agents/pipes.
func printJSON(w io.Writer, v any, pretty bool) error {
	enc := json.NewEncoder(w)
	if pretty {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(v)
}

// tplExecute renders a text/template string with data into w.
func tplExecute(w io.Writer, tpl string, data any) error {
	t, err := template.New("out").Parse(tpl)
	if err != nil {
		return err
	}
	return t.Execute(w, data)
}
