package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mimajiushi/godot-ai-cli/internal/ops"
)

// runCommands executes `commands` with args and captures stdout.
func runCommands(t *testing.T, args ...string) string {
	t.Helper()
	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"commands"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("commands %v: %v\n%s", args, err, buf.String())
	}
	return buf.String()
}

// decodeOps parses the `commands --json` envelope.
func decodeOps(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var envelope struct {
		Ops []map[string]any `json:"ops"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, raw)
	}
	return envelope.Ops
}

// TestCommandsJSONShapeAndOrdering: --json emits the full spec with the
// documented keys, and ops come in stable domain-then-name order.
func TestCommandsJSONShapeAndOrdering(t *testing.T) {
	list := decodeOps(t, runCommands(t, "--json"))
	if len(list) == 0 {
		t.Fatal("no ops in JSON output")
	}
	for _, keys := range []string{"domain", "name", "plugin_command", "summary", "timeout_sec", "write", "params"} {
		if _, ok := list[0][keys]; !ok {
			t.Errorf("op entry missing key %q: %v", keys, list[0])
		}
	}
	// Find an op with params and check the param keys.
	for _, entry := range list {
		params, ok := entry["params"].([]any)
		if !ok {
			t.Errorf("params is not an array: %v", entry)
			continue
		}
		if len(params) == 0 {
			continue
		}
		p := params[0].(map[string]any)
		for _, key := range []string{"flag", "param", "kind", "required", "default", "usage"} {
			if _, ok := p[key]; !ok {
				t.Errorf("param entry missing key %q: %v", key, p)
			}
		}
		break
	}
	// Ordering: domain rank (table order) then name, non-decreasing.
	rank := map[string]int{}
	for i, d := range ops.Domains() {
		rank[d] = i
	}
	for i := 1; i < len(list); i++ {
		prev, cur := list[i-1], list[i]
		pd, cd := prev["domain"].(string), cur["domain"].(string)
		if rank[pd] > rank[cd] {
			t.Errorf("domain order broken at %d: %s after %s", i, cd, pd)
		}
		if pd == cd && prev["name"].(string) > cur["name"].(string) {
			t.Errorf("name order broken in %s: %s after %s", pd, cur["name"], prev["name"])
		}
	}
}

// TestCommandsCoversEveryOpExactlyOnce: the listing matches the ops table
// one-to-one (count, uniqueness, identity).
func TestCommandsCoversEveryOpExactlyOnce(t *testing.T) {
	list := decodeOps(t, runCommands(t, "--json"))
	if len(list) != len(ops.All()) {
		t.Fatalf("listed %d ops, table has %d", len(list), len(ops.All()))
	}
	seen := map[string]int{}
	for _, entry := range list {
		key := entry["domain"].(string) + " " + entry["name"].(string)
		seen[key]++
		if _, ok := ops.Lookup(entry["domain"].(string), entry["name"].(string)); !ok {
			t.Errorf("listed op %q not in the ops table", key)
		}
	}
	for key, n := range seen {
		if n != 1 {
			t.Errorf("op %q listed %d times", key, n)
		}
	}
	for _, op := range ops.All() {
		if seen[op.Domain+" "+op.Name] != 1 {
			t.Errorf("table op %s %s missing from the listing", op.Domain, op.Name)
		}
	}
}

// TestCommandsDomainFilter: --domain narrows both JSON and text output to
// exactly that domain's ops.
func TestCommandsDomainFilter(t *testing.T) {
	list := decodeOps(t, runCommands(t, "--json", "--domain", "node"))
	if len(list) == 0 {
		t.Fatal("node domain filter returned nothing")
	}
	for _, entry := range list {
		if entry["domain"] != "node" {
			t.Errorf("domain filter leaked %v", entry["domain"])
		}
	}
	if len(list) != len(ops.ByDomain()["node"]) {
		t.Errorf("filtered %d ops, node domain has %d", len(list), len(ops.ByDomain()["node"]))
	}

	text := runCommands(t, "--domain", "node")
	if !strings.Contains(text, "## node") {
		t.Errorf("text output missing the node header:\n%s", text)
	}
	if strings.Contains(text, "## scene") || strings.Contains(text, "## editor") {
		t.Errorf("text output leaked other domains:\n%s", text)
	}
}

// TestCommandsUnknownDomain: a bogus domain is an error, not an empty list.
func TestCommandsUnknownDomain(t *testing.T) {
	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"commands", "--domain", "nope"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("unknown domain accepted")
	}
}

// TestCommandsTextFormat: the plain listing has ## headers and per-op
// lines with write gate and timeout markers.
func TestCommandsTextFormat(t *testing.T) {
	text := runCommands(t)
	if !strings.Contains(text, "## node\n") {
		t.Errorf("missing ## node header:\n%s", text[:200])
	}
	if !strings.Contains(text, "node create — ") || !strings.Contains(text, "[write] (timeout 8s)") {
		t.Errorf("op line format wrong:\n%s", text)
	}
}

// TestOpExamplesIncludeRequiredFlags: leaf -h examples show required flags
// with kind-typed placeholders, so -h alone teaches the minimal call.
func TestOpExamplesIncludeRequiredFlags(t *testing.T) {
	op, ok := ops.Lookup("node", "set-property")
	if !ok {
		t.Fatal("node set-property missing")
	}
	example := opExamples(op)
	for _, want := range []string{"--path <string>", "--property <string>", "--value '<json>'"} {
		if !strings.Contains(example, want) {
			t.Errorf("example missing %q: %s", want, example)
		}
	}
	// An op without required params keeps the bare invocation.
	op, ok = ops.Lookup("editor", "state")
	if !ok {
		t.Fatal("editor state missing")
	}
	if got := opExamples(op); got != "godot-ai-cli editor state" {
		t.Errorf("bare example = %q", got)
	}
}
