// Op command generation: the whole <domain> <op> command tree is built
// from the declarative internal/ops table, plus a `call` escape hatch and
// the hand-wired session/custom leaves (daemon-side concerns without a
// static plugin command).
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
	"github.com/mimajiushi/godot-ai-cli/internal/ops"
)

// prettyOutput is bound to the root's persistent --pretty flag.
var prettyOutput bool

// newDomainCommands builds one parent command per ops domain with all of
// its generated op leaves attached.
func newDomainCommands() []*cobra.Command {
	byDomain := ops.ByDomain()
	var cmds []*cobra.Command
	for _, domain := range ops.Domains() {
		parent := &cobra.Command{
			Use:   domain,
			Short: ops.DomainSummary(domain),
			Args:  cobra.NoArgs,
		}
		// The flag is bound here so it exists; leaves read it back through
		// cmd.Flags() during daemon port resolution (daemon_state.go).
		httpPort := daemon.DefaultHTTPPort
		parent.PersistentFlags().IntVar(&httpPort, "http-port", daemon.DefaultHTTPPort, "daemon HTTP port")
		for _, op := range byDomain[domain] {
			parent.AddCommand(newOpCommand(op))
		}
		switch domain {
		case "session":
			parent.AddCommand(newSessionListCommand(), newSessionActivateCommand())
		case "custom":
			parent.AddCommand(newCustomListCommand(), newCustomInvokeCommand())
		}
		cmds = append(cmds, parent)
	}
	return cmds
}

// newOpCommand builds one leaf command from its OpSpec: typed flags per
// ParamSpec plus the shared --session/--params flags (and --file for
// batch execute, --out/--assert/--tolerance for editor screenshot).
func newOpCommand(op ops.OpSpec) *cobra.Command {
	cmd := &cobra.Command{
		Use:   op.Name + opUseSuffix(op),
		Short: op.Summary,
		Long: fmt.Sprintf(`%s

Plugin command: %s (timeout %s, %s)%s

Every op also accepts:
  --session <id>      pin the call to one connected editor session
  --params '<json>'   base params as a JSON object; explicit flags override colliding keys
Optional flags left at their zero value are omitted from the wire params
(unless passed via --params).

Examples:
  %s`,
			op.Summary, op.PluginCommand, op.Timeout, writeLabel(op.Write), opResponseNote(op), opExamples(op)),
		Args: boolFlagValueArgs(op),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if op.Domain == "editor" && op.Name == "screenshot" {
				return runScreenshot(cmd, op)
			}
			if op.Domain == "editor" && op.Name == "record" {
				return runRecord(cmd, op)
			}
			params, err := collectParams(cmd, op)
			if err != nil {
				return jsonError(cmd, "INVALID_PARAMS", err.Error(), nil)
			}
			return executeOp(cmd, op.PluginCommand, params, op.Timeout, op.Write)
		},
	}
	for _, p := range op.Params {
		registerParamFlag(cmd, p)
	}
	cmd.Flags().String("session", "", "pin the call to one connected editor session")
	cmd.Flags().String("params", "", "base params as a JSON object; explicit flags override colliding keys")
	if op.Domain == "batch" && op.Name == "execute" {
		cmd.Flags().String("file", "", "JSON file containing an array of {\"command\": ..., \"params\": {...}}")
	}
	if op.Domain == "editor" && op.Name == "screenshot" {
		cmd.Flags().String("out", "", "save the captured image to this file and omit image_base64 from the output")
		cmd.Flags().StringArray("assert", nil, "expected pixel as '#RRGGBB@x,y' (repeatable); fails with PIXEL_ASSERT_FAILED on mismatch")
		cmd.Flags().Int("tolerance", 0, "per-channel tolerance for --assert")
		cmd.Flags().Bool("full-res", false, "capture at full source resolution (sends max_resolution=0, no downscale cap)")
		cmd.Flags().String("region", "", "crop the capture to \"x,y,w,h\" in source-image pixels (crops first, then --max-resolution applies)")
	}
	if op.Domain == "editor" && op.Name == "record" {
		cmd.Flags().String("out-dir", "", "save each frame as PNG into this directory (frames omitted from stdout)")
		cmd.Flags().String("out", "", "with --format gif: write the animated GIF to this file")
		cmd.Flags().String("format", "png", "png (per-frame files) | gif (animated)")
		cmd.Flags().Float64("duration", 0, "capture this many seconds (frame count = duration x --fps)")
		cmd.Flags().Int("fps", 0, "frame rate used with --duration")
		cmd.Flags().Bool("full-res", false, "capture frames at full source resolution (sends max_resolution=0)")
	}
	return cmd
}

// opUseSuffix lists required flags in the Use line.
func opUseSuffix(op ops.OpSpec) string {
	var b strings.Builder
	for _, p := range op.Params {
		if p.Required {
			b.WriteString(" --" + p.Flag + " <" + p.Kind + ">")
		}
	}
	return b.String()
}

// writeLabel renders the write-gate state for help text.
func writeLabel(write bool) string {
	if write {
		return "write-gated"
	}
	return "read-only"
}

// opResponseNote renders the optional "Response:" help section.
func opResponseNote(op ops.OpSpec) string {
	if op.ResponseNote == "" {
		return ""
	}
	return "\n\nResponse:\n  " + op.ResponseNote
}

// opExamples renders one or two example invocations for the Long help.
// Ops with required params get the flag-form example. Ops without any show
// the bare call plus — when the op declares at least one optional
// string/int param — a --params demo naming one real key, so the JSON
// base-params form is discoverable from -h (a bare `node create` teaches
// nothing).
func opExamples(op ops.OpSpec) string {
	base := "godot-ai-cli " + op.Domain + " " + op.Name
	var required []string
	for _, p := range op.Params {
		if p.Required {
			required = append(required, "--"+p.Flag+" "+exampleValue(p))
		}
	}
	if len(required) > 0 {
		return base + " " + strings.Join(required, " ")
	}
	if demo := paramsDemoExample(base, op); demo != "" {
		return base + "\n  " + demo
	}
	return base
}

// paramsDemoExample builds the second example line for ops without required
// params: a --params call carrying the first optional string/int param with
// a plausible value. Returns "" when the op has no string/int param to demo
// — a made-up JSON key would teach the wrong thing.
func paramsDemoExample(base string, op ops.OpSpec) string {
	for _, p := range op.Params {
		if p.Required || (p.Kind != ops.KindString && p.Kind != ops.KindInt) {
			continue
		}
		payload, err := json.Marshal(map[string]any{p.Param: exampleParamValue(p)})
		if err != nil {
			return ""
		}
		return base + " --params '" + string(payload) + "'"
	}
	return ""
}

// exampleParamValue picks a plausible demo value for one param: the declared
// default when it is non-empty (and not the kind's zero), a "(default: X)"
// hint from the usage text, or a kind-appropriate stand-in last.
func exampleParamValue(p ops.ParamSpec) any {
	if p.Default != "" && p.Default != "0" {
		if p.Kind == ops.KindInt {
			if n, err := strconv.Atoi(p.Default); err == nil {
				return n
			}
		}
		return p.Default
	}
	if hint := usageDefaultHint(p.Usage); hint != "" {
		if p.Kind == ops.KindInt {
			if n, err := strconv.Atoi(hint); err == nil {
				return n
			}
		}
		return hint
	}
	if p.Kind == ops.KindInt {
		return 1
	}
	return "example"
}

// usageDefaultHint extracts X from usage text carrying "(default: X)".
func usageDefaultHint(usage string) string {
	i := strings.Index(usage, "(default: ")
	if i < 0 {
		return ""
	}
	rest := usage[i+len("(default: "):]
	j := strings.Index(rest, ")")
	if j <= 0 {
		return ""
	}
	return rest[:j]
}

// exampleValue renders a kind-typed placeholder value for one param in
// examples, mirroring the --flag <kind> convention of the Use line.
func exampleValue(p ops.ParamSpec) string {
	if p.Kind == ops.KindJSON {
		return `'<json>'` // quoted: raw JSON needs shell protection
	}
	return "<" + p.Kind + ">"
}

// registerParamFlag adds one typed flag for a ParamSpec.
func registerParamFlag(cmd *cobra.Command, p ops.ParamSpec) {
	switch p.Kind {
	case ops.KindInt:
		def := 0
		if p.Default != "" {
			def, _ = strconv.Atoi(p.Default)
		}
		cmd.Flags().Int(p.Flag, def, p.Usage)
	case ops.KindFloat:
		var def float64
		if p.Default != "" {
			def, _ = strconv.ParseFloat(p.Default, 64)
		}
		cmd.Flags().Float64(p.Flag, def, p.Usage)
	case ops.KindBool:
		def := p.Default == "true"
		cmd.Flags().Bool(p.Flag, def, p.Usage)
	default: // string and json both arrive as raw strings; json is validated later
		cmd.Flags().String(p.Flag, p.Default, p.Usage)
	}
}

// boolFlagValueArgs accepts the `--pressed false` spelling: pflag bool flags
// carry an implicit NoOptDefVal="true" and never consume the next token, so
// `--pressed false` strands "false" as a positional. With exactly one
// true/false positional AND exactly one explicitly-set bool flag on the
// command, treat the positional as that flag's value; anything else gets a
// steering error (bool flags take no space-separated value).
func boolFlagValueArgs(op ops.OpSpec) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}
		if len(args) == 1 && (args[0] == "true" || args[0] == "false") {
			if changed := changedBoolFlags(cmd, op); len(changed) == 1 {
				return cmd.Flags().Set(changed[0], args[0])
			}
		}
		boolFlags := boolFlagNames(op)
		hint := ""
		if len(boolFlags) > 0 {
			hint = fmt.Sprintf("; boolean flags (%s) do not take a separate value — use --%s / --%s=false",
				"--"+strings.Join(boolFlags, ", --"), boolFlags[0], boolFlags[0])
		}
		return fmt.Errorf("unknown argument %q for \"godot-ai-cli %s %s\"%s",
			args[0], op.Domain, op.Name, hint)
	}
}

// changedBoolFlags lists the command's explicitly-set bool param flags, in
// OpSpec declaration order.
func changedBoolFlags(cmd *cobra.Command, op ops.OpSpec) []string {
	var out []string
	for _, p := range op.Params {
		if p.Kind != ops.KindBool {
			continue
		}
		if flag := cmd.Flags().Lookup(p.Flag); flag != nil && flag.Changed {
			out = append(out, p.Flag)
		}
	}
	return out
}

// boolFlagNames lists every KindBool flag of the op, in declaration order.
func boolFlagNames(op ops.OpSpec) []string {
	var out []string
	for _, p := range op.Params {
		if p.Kind == ops.KindBool {
			out = append(out, p.Flag)
		}
	}
	return out
}

// collectParams merges --params (base object) with the typed flags
// (explicit flags win). Optional flags left at their kind's zero value are
// omitted from the wire params; required params must be present afterwards.
// WrapOp routes the collected params through the wrapper command shape.
func collectParams(cmd *cobra.Command, op ops.OpSpec) (map[string]any, error) {
	params := map[string]any{}
	if raw, _ := cmd.Flags().GetString("params"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &params); err != nil {
			return nil, fmt.Errorf("--params is not a valid JSON object: %v", err)
		}
		// Unmarshalling "null" zeroes the map to nil; re-initialize it so
		// the writes below (batch --file, non-zero defaults) never panic.
		if params == nil {
			params = map[string]any{}
		}
	}
	if op.Domain == "batch" && op.Name == "execute" {
		if file, _ := cmd.Flags().GetString("file"); file != "" {
			data, err := os.ReadFile(file)
			if err != nil {
				return nil, fmt.Errorf("--file: %v", err)
			}
			var commands []any
			if err := json.Unmarshal(data, &commands); err != nil {
				return nil, fmt.Errorf("--file does not contain a JSON array: %v", err)
			}
			params["commands"] = commands
		}
	}
	for _, p := range op.Params {
		flag := cmd.Flags().Lookup(p.Flag)
		if flag == nil {
			continue
		}
		if !paramIncluded(flag, p) {
			continue
		}
		value, err := parseParamValue(p, flag.Value.String())
		if err != nil {
			return nil, fmt.Errorf("--%s: %v", p.Flag, err)
		}
		params[p.Param] = value
	}
	for _, p := range op.Params {
		if p.Required {
			if _, ok := params[p.Param]; !ok {
				return nil, fmt.Errorf("missing required flag --%s (%s)", p.Flag, p.Usage)
			}
		}
	}
	if op.Domain == "batch" && op.Name == "execute" {
		if _, ok := params["commands"]; !ok {
			return nil, fmt.Errorf("batch execute needs --file <json> or --params '{\"commands\": [...]}'")
		}
	}
	if op.WrapOp != "" {
		params = map[string]any{"op": op.WrapOp, "params": params}
	}
	return params, nil
}

// paramIncluded decides whether a flag's value goes onto the wire: always
// when the user set it explicitly, otherwise only when it differs from the
// kind's zero value (a declared non-zero default mirrors the upstream
// handler default and is always sent).
func paramIncluded(flag *pflag.Flag, p ops.ParamSpec) bool {
	if flag.Changed {
		return true
	}
	switch p.Kind {
	case ops.KindBool:
		return flag.Value.String() == "true"
	case ops.KindInt, ops.KindFloat:
		return flag.Value.String() != "0"
	case ops.KindJSON:
		return false // no zero-safe default for raw JSON
	default:
		return flag.Value.String() != ""
	}
}

// parseParamValue converts a flag's string value to its wire type.
func parseParamValue(p ops.ParamSpec, raw string) (any, error) {
	switch p.Kind {
	case ops.KindInt:
		v, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("want an integer, got %q", raw)
		}
		return v, nil
	case ops.KindFloat:
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("want a number, got %q", raw)
		}
		return v, nil
	case ops.KindBool:
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("want a boolean, got %q", raw)
		}
		return v, nil
	case ops.KindJSON:
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, fmt.Errorf("want raw JSON, got %q: %v", raw, err)
		}
		return v, nil
	default:
		return raw, nil
	}
}

// executeOp posts one command to the daemon's execute endpoint and prints
// the result: the data payload on success, the daemon's error envelope
// (unchanged) plus a non-zero exit on failure. The daemon HTTP port is
// resolved per daemonPortCandidates (--http-port flag → recorded
// last-daemon port → default, with the default as recorded-port fallback).
func executeOp(cmd *cobra.Command, command string, params map[string]any, timeout time.Duration, write bool) error {
	resp, err := executeOpRaw(cmd, command, params, timeout, write)
	if err != nil {
		return err
	}
	return printExecuteResponse(cmd, resp)
}

// executeOpRaw is the transport half of executeOp: it returns the daemon's
// raw response so callers with CLI-side post-processing (editor screenshot's
// --out/--assert, the source=game preflight) can inspect it before printing.
// Transport failures are already enveloped (DAEMON_UNREACHABLE).
func executeOpRaw(cmd *cobra.Command, command string, params map[string]any, timeout time.Duration, write bool) (map[string]any, error) {
	httpPort, err := requireDaemonPort(cmd)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"command":     command,
		"params":      params,
		"timeout_sec": timeout.Seconds(),
		"write":       write,
	}
	if sessionID, _ := cmd.Flags().GetString("session"); sessionID != "" {
		body["session_id"] = sessionID
	}
	// The HTTP request must outlive the plugin-side budget; the daemon
	// itself adds its own margin on top of timeout_sec.
	resp, err := postDaemonJSON(httpPort, "/godot-ai/cli/execute", body, timeout+15*time.Second)
	if err != nil {
		return nil, jsonError(cmd, "DAEMON_UNREACHABLE", err.Error(), nil)
	}
	return resp, nil
}

// printExecuteResponse renders the daemon's execute reply: data on
// success, the error envelope verbatim (and a non-zero exit) on failure.
func printExecuteResponse(cmd *cobra.Command, resp map[string]any) error {
	if status, _ := resp["status"].(string); status == "ok" {
		data := resp["data"]
		if data == nil {
			data = map[string]any{}
		}
		return printJSON(cmd.OutOrStdout(), data, prettyOutput)
	}
	_ = printJSON(cmd.OutOrStdout(), resp, prettyOutput)
	code := "COMMAND_FAILED"
	if env, ok := resp["error"].(map[string]any); ok {
		if c, ok := env["code"].(string); ok && c != "" {
			code = c
		}
	}
	return errExit(code)
}

// newCallCommand is the escape hatch: call ANY plugin command directly.
func newCallCommand() *cobra.Command {
	var (
		httpPort   int
		timeoutSec int
		paramsJSON string
		sessionID  string
		write      bool
	)
	cmd := &cobra.Command{
		Use:   "call <plugin_command>",
		Short: "Call any plugin command directly (escape hatch)",
		Long: `call sends an arbitrary plugin command through the execute path —
for commands with no typed subcommand (e.g. third-party custom tools) or
for quick protocol experiments.

Examples:
  godot-ai-cli call get_editor_state
  godot-ai-cli call set_property --params '{"path":"Root","property":"name","value":"X"}' --write`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			params := map[string]any{}
			if paramsJSON != "" {
				if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
					return jsonError(cmd, "INVALID_PARAMS",
						fmt.Sprintf("--params is not a valid JSON object: %v", err), nil)
				}
			}
			cmd.Flags().Set("session", sessionID)
			return executeOp(cmd, args[0], params,
				time.Duration(timeoutSec)*time.Second, write)
		},
	}
	cmd.Flags().IntVar(&httpPort, "http-port", daemon.DefaultHTTPPort, "daemon HTTP port")
	cmd.Flags().IntVar(&timeoutSec, "timeout", 8, "plugin command budget in seconds")
	cmd.Flags().StringVar(&paramsJSON, "params", "", "params as a JSON object")
	cmd.Flags().StringVar(&sessionID, "session", "", "pin the call to one connected editor session")
	cmd.Flags().BoolVar(&write, "write", false, "gate on editor writability (for mutating commands)")
	return cmd
}

// newSessionListCommand lists connected editor sessions (daemon-side; the
// upstream session_manage rollup is a server concern, not a plugin command).
func newSessionListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List connected Godot editor sessions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			httpPort, err := requireDaemonPort(cmd)
			if err != nil {
				return err
			}
			body, err := getDaemonJSON(httpPort, "/godot-ai/cli/sessions")
			if err != nil {
				return jsonError(cmd, "DAEMON_UNREACHABLE", err.Error(), nil)
			}
			return printJSON(cmd.OutOrStdout(), body, prettyOutput)
		},
	}
}

// newSessionActivateCommand pins the active session by exact id (the
// upstream tool also accepts substring hints; the CLI keeps exact ids —
// `session list` prints them).
func newSessionActivateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "activate <session-id>",
		Short: "Pin subsequent calls to one connected editor session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			httpPort, err := requireDaemonPort(cmd)
			if err != nil {
				return err
			}
			body, err := postDaemonJSON(httpPort, "/godot-ai/cli/activate",
				map[string]any{"session_id": args[0]}, 5*time.Second)
			if err != nil {
				return jsonError(cmd, "DAEMON_UNREACHABLE", err.Error(), nil)
			}
			return printExecuteResponse(cmd, body)
		},
	}
}

// newCustomListCommand lists the custom tools the connected editor
// registered (catalog cached by the daemon from plugin events).
func newCustomListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List custom tools registered by the connected editor",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			httpPort, err := requireDaemonPort(cmd)
			if err != nil {
				return err
			}
			body, err := getDaemonJSON(httpPort, "/godot-ai/cli/custom-tools")
			if err != nil {
				return jsonError(cmd, "DAEMON_UNREACHABLE", err.Error(), nil)
			}
			return printJSON(cmd.OutOrStdout(), body, prettyOutput)
		},
	}
}

// newCustomInvokeCommand invokes a third-party custom tool; the plugin
// routes it as custom_tool:<name>.
func newCustomInvokeCommand() *cobra.Command {
	var (
		tool       string
		paramsJSON string
		timeoutSec int
		sessionID  string
	)
	cmd := &cobra.Command{
		Use:   "invoke --tool <name>",
		Short: "Invoke a custom tool registered by an editor addon",
		Long: `invoke routes to the plugin as custom_tool:<name>. The tool's own
spec decides whether it is write-gated; pass --params with the tool's
declared parameters.

Examples:
  godot-ai-cli custom list
  godot-ai-cli custom invoke --tool my_tool --params '{"key":"value"}'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if tool == "" {
				return jsonError(cmd, "INVALID_PARAMS", "missing required flag --tool", nil)
			}
			params := map[string]any{}
			if paramsJSON != "" {
				if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
					return jsonError(cmd, "INVALID_PARAMS",
						fmt.Sprintf("--params is not a valid JSON object: %v", err), nil)
				}
			}
			cmd.Flags().Set("session", sessionID)
			return executeOp(cmd, "custom_tool:"+tool, params,
				time.Duration(timeoutSec)*time.Second, false)
		},
	}
	cmd.Flags().StringVar(&tool, "tool", "", "custom tool name (see custom list)")
	cmd.Flags().StringVar(&paramsJSON, "params", "", "params as a JSON object")
	cmd.Flags().IntVar(&timeoutSec, "timeout", 8, "tool budget in seconds (its spec may allow up to 120)")
	cmd.Flags().StringVar(&sessionID, "session", "", "pin the call to one connected editor session")
	return cmd
}
