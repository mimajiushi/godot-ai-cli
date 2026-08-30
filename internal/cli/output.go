// Output helpers shared by all subcommands: JSON results on stdout and
// diagnostics on stderr, keeping agent consumption predictable.
package cli

import (
	"encoding/json"
	"io"
	"os"
	"text/template"
)

// stderr is the shared diagnostic writer (a var so tests can capture it).
var stderr io.Writer = os.Stderr

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
