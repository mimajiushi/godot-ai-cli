package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
	"github.com/mimajiushi/godot-ai-cli/internal/ops"
	"github.com/mimajiushi/godot-ai-cli/internal/testutil/mockplugin"
)

// listenFree reserves an ephemeral loopback listener (close it to free the
// port for the test's use).
func listenFree(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

// itoa keeps flag argument assembly readable.
func itoa(n int) string { return strconv.Itoa(n) }

// TestCommandTreeCoversAllOps: every table op and every hand-wired leaf
// resolves in the generated command tree.
func TestCommandTreeCoversAllOps(t *testing.T) {
	root := NewRootCommand()
	for _, op := range ops.All() {
		cmd, _, err := root.Find([]string{op.Domain, op.Name})
		if err != nil || cmd == nil || cmd.Name() != op.Name {
			t.Errorf("op %s %s not reachable in the command tree (err=%v)", op.Domain, op.Name, err)
			continue
		}
		for _, p := range op.Params {
			if cmd.Flags().Lookup(p.Flag) == nil {
				t.Errorf("op %s %s: flag --%s not registered", op.Domain, op.Name, p.Flag)
			}
		}
		if cmd.Flags().Lookup("session") == nil || cmd.Flags().Lookup("params") == nil {
			t.Errorf("op %s %s: shared --session/--params flags missing", op.Domain, op.Name)
		}
	}
	for _, leaf := range ops.HandWiredLeaves {
		parts := strings.Split(leaf, " ")
		cmd, _, err := root.Find(parts)
		if err != nil || cmd == nil || cmd.Name() != parts[len(parts)-1] {
			t.Errorf("hand-wired leaf %q not reachable (err=%v)", leaf, err)
		}
	}
}

// leafFor builds the leaf command of one op for collectParams tests.
func leafFor(t *testing.T, domain, name string) (ops.OpSpec, *cobra.Command) {
	t.Helper()
	op, ok := ops.Lookup(domain, name)
	if !ok {
		t.Fatalf("op %s/%s missing", domain, name)
	}
	return op, newOpCommand(op)
}

// TestCollectParamsZeroOmission: optional flags left at their kind's zero
// value stay off the wire; explicit values and non-zero defaults are sent.
func TestCollectParamsZeroOmission(t *testing.T) {
	op, cmd := leafFor(t, "node", "create")
	if err := cmd.Flags().Set("type", "Sprite2D"); err != nil {
		t.Fatal(err)
	}
	params, err := collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["type"] != "Sprite2D" {
		t.Errorf("type = %v", params["type"])
	}
	for _, omitted := range []string{"name", "parent_path", "scene_path", "scene_file"} {
		if _, ok := params[omitted]; ok {
			t.Errorf("zero-value param %q leaked onto the wire: %v", omitted, params)
		}
	}
}

// TestCollectParamsParamsBaseAndOverride: --params supplies the base
// object; explicit flags override colliding keys.
func TestCollectParamsParamsBaseAndOverride(t *testing.T) {
	op, cmd := leafFor(t, "node", "set-property")
	must := func(flag, value string) {
		t.Helper()
		if err := cmd.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	must("params", `{"path":"Root/A","property":"position","value":[0,0],"extra":"kept"}`)
	must("value", `[1,2]`) // flag overrides the colliding --params key
	params, err := collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["extra"] != "kept" {
		t.Errorf("--params base key lost: %v", params)
	}
	arr, ok := params["value"].([]any)
	if !ok || len(arr) != 2 || arr[0] != 1.0 {
		t.Errorf("flag override lost: %v", params["value"])
	}
}

// TestCollectParamsRequiredAndJSON: missing required flags and invalid
// JSON values are errors before anything hits the wire.
func TestCollectParamsRequiredAndJSON(t *testing.T) {
	op, cmd := leafFor(t, "node", "set-property")
	if _, err := collectParams(cmd, op); err == nil {
		t.Error("missing required flags accepted")
	}

	op, cmd = leafFor(t, "node", "set-property")
	for flag, value := range map[string]string{
		"path": "Root", "property": "name", "value": "{not json",
	} {
		if err := cmd.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := collectParams(cmd, op); err == nil {
		t.Error("invalid JSON --value accepted")
	}
}

// TestCollectParamsWrapOp: game-domain ops wrap their params into the
// game_command envelope {"op": ..., "params": {...}}.
func TestCollectParamsWrapOp(t *testing.T) {
	op, cmd := leafFor(t, "game", "input-key")
	if err := cmd.Flags().Set("key", "Space"); err != nil {
		t.Fatal(err)
	}
	params, err := collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["op"] != "input_key" {
		t.Fatalf("op = %v", params["op"])
	}
	nested, ok := params["params"].(map[string]any)
	if !ok || nested["key"] != "Space" {
		t.Errorf("nested params = %v", params)
	}
}

// TestCollectParamsNullParams: --params 'null' unmarshals to a nil map;
// collectParams must re-initialize it so the later writes (batch --file,
// non-zero flag defaults) cannot panic.
func TestCollectParamsNullParams(t *testing.T) {
	// batch execute --params null --file: the commands write lands cleanly.
	op, cmd := leafFor(t, "batch", "execute")
	file := filepath.Join(t.TempDir(), "cmds.json")
	if err := os.WriteFile(file, []byte(`[{"command":"editor_state"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("params", "null"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("file", file); err != nil {
		t.Fatal(err)
	}
	params, err := collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	commands, ok := params["commands"].([]any)
	if !ok || len(commands) != 1 {
		t.Errorf("commands = %v", params["commands"])
	}

	// scene get-hierarchy --params null: the non-zero depth default lands.
	op, cmd = leafFor(t, "scene", "get-hierarchy")
	if err := cmd.Flags().Set("params", "null"); err != nil {
		t.Fatal(err)
	}
	params, err = collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["depth"] != 10 {
		t.Errorf("depth = %v, want the non-zero default 10", params["depth"])
	}
}

// TestOpExamplesParamsDemo: ops without required params get a second
// example line teaching the --params JSON form with one real optional key;
// ops with required params keep the single flag-form line; ops without any
// string/int param stay bare (a made-up key would teach the wrong thing).
func TestOpExamplesParamsDemo(t *testing.T) {
	// node create: no required params, first string param "type" carries a
	// "(default: Node)" usage hint → demo uses it.
	op, ok := ops.Lookup("node", "create")
	if !ok {
		t.Fatal("node create missing")
	}
	got := opExamples(op)
	want := "godot-ai-cli node create\n  godot-ai-cli node create --params '{\"type\":\"Node\"}'"
	if got != want {
		t.Errorf("node create examples = %q, want %q", got, want)
	}

	// scene get-hierarchy: int param with a non-zero default → typed value.
	op, _ = ops.Lookup("scene", "get-hierarchy")
	got = opExamples(op)
	want = "godot-ai-cli scene get-hierarchy\n  godot-ai-cli scene get-hierarchy --params '{\"depth\":10}'"
	if got != want {
		t.Errorf("scene get-hierarchy examples = %q, want %q", got, want)
	}

	// editor state: no params at all → bare call only.
	op, _ = ops.Lookup("editor", "state")
	if got := opExamples(op); got != "godot-ai-cli editor state" {
		t.Errorf("editor state examples = %q", got)
	}

	// editor monitors: its only param is JSON kind → bare call only.
	op, _ = ops.Lookup("editor", "monitors")
	if got := opExamples(op); got != "godot-ai-cli editor monitors" {
		t.Errorf("editor monitors examples = %q", got)
	}

	// node set-property: required params → unchanged single flag-form line.
	op, _ = ops.Lookup("node", "set-property")
	got = opExamples(op)
	want = "godot-ai-cli node set-property --path <string> --property <string> --value '<json>'"
	if got != want {
		t.Errorf("node set-property examples = %q, want %q", got, want)
	}
}

// TestOpExamplesParamsDemoKeysAreReal is the table-wide invariant: every
// generated --params demo line parses as JSON and carries exactly one key
// that is an optional string/int wire param of that op.
func TestOpExamplesParamsDemoKeysAreReal(t *testing.T) {
	for _, op := range ops.All() {
		ex := opExamples(op)
		idx := strings.Index(ex, " --params '")
		if idx < 0 {
			continue
		}
		raw := strings.TrimSuffix(ex[idx+len(" --params '"):], "'")
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Errorf("op %s %s: demo --params is not JSON: %q", op.Domain, op.Name, raw)
			continue
		}
		if len(m) != 1 {
			t.Errorf("op %s %s: demo carries %d keys, want 1", op.Domain, op.Name, len(m))
			continue
		}
		for key := range m {
			real := false
			for _, p := range op.Params {
				if p.Param == key && !p.Required && (p.Kind == ops.KindString || p.Kind == ops.KindInt) {
					real = true
				}
			}
			if !real {
				t.Errorf("op %s %s: demo key %q is not an optional string/int param", op.Domain, op.Name, key)
			}
		}
	}
}

// TestExecuteOpDaemonDown: ops against a dead daemon get the standard
// not-running envelope with the launch hint.
func TestExecuteOpDaemonDown(t *testing.T) {
	// Find a port nothing listens on.
	ln := listenFree(t)
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"editor", "state", "--http-port", itoa(port)})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a non-nil error against a dead daemon")
	}
	if !strings.Contains(buf.String(), "DAEMON_NOT_RUNNING") ||
		!strings.Contains(buf.String(), "godot-ai-cli launch") {
		t.Errorf("output missing the not-running envelope or launch hint:\n%s", buf.String())
	}
}

// TestExecuteOpRoundTrip drives the full path against a real daemon with a
// mock plugin: flags become wire params, write gating sets write:true, and
// the data payload is printed.
func TestExecuteOpRoundTrip(t *testing.T) {
	d, err := daemon.Start(context.Background(), daemon.Config{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	plugin := mockplugin.Dial(t, d.Bridge().Addr(), nil)
	plugin.SetResponder(func(command string, params map[string]any) *mockplugin.Response {
		return &mockplugin.Response{Data: map[string]any{"echo": params["property"]}}
	})

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{
		"node", "set-property", "--http-port", itoa(d.HTTPPort()),
		"--path", "Root", "--property", "name", "--value", `"Hero"`,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), `"echo":"name"`) {
		t.Errorf("data payload not printed:\n%s", buf.String())
	}

	got := plugin.Received()
	if len(got) != 1 || got[0].Command != "set_property" {
		t.Fatalf("mock received %v", got)
	}
	params := got[0].Params
	if params["path"] != "Root" || params["property"] != "name" || params["value"] != "Hero" {
		t.Errorf("wire params = %v", params)
	}
}

// TestCallEscapeHatch: `call` forwards an arbitrary command with raw
// params, and plugin errors surface as the daemon's envelope + exit 1.
func TestCallEscapeHatch(t *testing.T) {
	d, err := daemon.Start(context.Background(), daemon.Config{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	plugin := mockplugin.Dial(t, d.Bridge().Addr(), nil)
	plugin.SetResponder(func(command string, params map[string]any) *mockplugin.Response {
		return &mockplugin.Response{
			Status: "error",
			Error:  map[string]any{"code": "UNKNOWN_COMMAND", "message": "nope", "data": map[string]any{"retryable": false}},
		}
	})

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"call", "whatever", "--http-port", itoa(d.HTTPPort())})
	err = cmd.Execute()
	if err == nil {
		t.Fatal("expected the plugin error to exit non-zero")
	}
	if !strings.Contains(buf.String(), "UNKNOWN_COMMAND") {
		t.Errorf("error envelope not printed:\n%s", buf.String())
	}
	if got := plugin.Received(); len(got) != 1 || got[0].Command != "whatever" {
		t.Errorf("mock received %v", got)
	}
}

// TestCustomListRoundTrip: the catalog the plugin pushes via
// custom_tools_changed events is served back by `custom list`.
func TestCustomListRoundTrip(t *testing.T) {
	d, err := daemon.Start(context.Background(), daemon.Config{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	plugin := mockplugin.Dial(t, d.Bridge().Addr(), nil)
	plugin.PushEvent("custom_tools_changed", map[string]any{
		"tools": []any{map[string]any{"name": "my_tool", "enabled": true}},
	})
	// The event is processed asynchronously on the WS read loop.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if tools, ok := d.Bridge().CustomTools(""); ok && len(tools) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("custom tool catalog never cached")
		}
		time.Sleep(20 * time.Millisecond)
	}

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"custom", "list", "--http-port", itoa(d.HTTPPort())})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("custom list: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "my_tool") {
		t.Errorf("catalog not printed:\n%s", buf.String())
	}
}
