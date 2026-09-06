package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/mimajiushi/godot-ai-cli/internal/ops"
)

// TestCommandsMarkdownCatalog: --format md emits the complete skill op
// catalog — header count, per-domain sections, one entry per op, write
// gates, derived long-ops / CLI-extras bullets, CLI-side flag lines, and
// DocNote prose all come from the op table.
func TestCommandsMarkdownCatalog(t *testing.T) {
	out := runCommands(t, "--format", "md")

	all := ops.All()
	if !strings.Contains(out, fmt.Sprintf("commands --format md` (%d ops)", len(all))) {
		t.Errorf("header count missing or wrong; want %d ops", len(all))
	}
	if !strings.Contains(out, "godot-ai-cli commands --format md\n```") {
		t.Error("regeneration instructions missing")
	}
	for _, op := range all {
		entry := fmt.Sprintf("### `%s %s`", op.Domain, op.Name)
		if !strings.Contains(out, entry) {
			t.Errorf("catalog missing entry %s", entry)
		}
	}
	byDomain := ops.ByDomain()
	for domain, domainOps := range byDomain {
		plural := "ops"
		if len(domainOps) == 1 {
			plural = "op"
		}
		header := fmt.Sprintf("## %s (%d %s)", domain, len(domainOps), plural)
		if !strings.Contains(out, header) {
			t.Errorf("catalog missing domain header %q", header)
		}
	}
	for _, want := range []string{
		"`editor record` 75s",                        // derived long-ops bullet
		"`editor screenshot` also accepts `--out`",   // derived CLI-extras bullet
		"`batch execute` also accepts `--file`",      // derived CLI-extras bullet
		"CLI-side flags (not wire params): `--out`",  // per-op CLI flags line
		"`--assert` string[]",                        // stringArray kind label
		"**[write]**",                                // write gate marker
		"Eval code constraints:",                     // editor eval DocNote
		"matched_count",                              // logs read DocNote
		"## api (1 op)",                              // singular pluralization
		"## batch (1 op)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("catalog missing %q", want)
		}
	}
}

// TestCommandsMarkdownDomainFilter: --domain narrows the markdown catalog
// and the header count follows the filter.
func TestCommandsMarkdownDomainFilter(t *testing.T) {
	out := runCommands(t, "--format", "md", "--domain", "scene")
	if !strings.Contains(out, "## scene (") {
		t.Error("scene section missing")
	}
	if strings.Contains(out, "## node (") {
		t.Error("node section leaked into a scene-only catalog")
	}
	if !strings.Contains(out, "(6 ops)") {
		t.Errorf("header count should reflect the filtered list:\n%s",
			strings.SplitN(out, "\n", 4)[2])
	}
}

// TestCommandsFormatJSONShorthandAndConflict: --format json equals --json;
// --json combined with a contradicting --format is a usage error.
func TestCommandsFormatJSONShorthandAndConflict(t *testing.T) {
	viaFlag := decodeOps(t, runCommands(t, "--format", "json"))
	viaShort := decodeOps(t, runCommands(t, "--json"))
	if len(viaFlag) != len(viaShort) || len(viaFlag) != len(ops.All()) {
		t.Errorf("--format json listed %d ops, --json %d, table %d",
			len(viaFlag), len(viaShort), len(ops.All()))
	}

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"commands", "--json", "--format", "md"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("--json with --format md should fail as contradictory")
	} else if !strings.Contains(buf.String(), "USAGE_ERROR") {
		t.Errorf("want USAGE_ERROR envelope, got: %v\n%s", err, buf.String())
	}
}

// TestCommandsJSONCLIFlagsAndResponse: --json exposes the CLI-side-only
// flags and the response note per op, with cli_flags always an array.
func TestCommandsJSONCLIFlagsAndResponse(t *testing.T) {
	raw := runCommands(t, "--json")
	if !strings.Contains(raw, `"cli_flags":[]`) {
		t.Error("ops without CLI-side flags must emit an empty array, not null")
	}
	list := decodeOps(t, raw)

	var screenshot, state map[string]any
	for _, entry := range list {
		switch entry["domain"].(string) + " " + entry["name"].(string) {
		case "editor screenshot":
			screenshot = entry
		case "editor state":
			state = entry
		}
	}
	if screenshot == nil || state == nil {
		t.Fatal("editor screenshot / editor state missing from the listing")
	}

	flags, ok := screenshot["cli_flags"].([]any)
	if !ok || len(flags) != 5 {
		t.Fatalf("editor screenshot cli_flags = %v", screenshot["cli_flags"])
	}
	got := map[string]string{}
	for _, f := range flags {
		fm := f.(map[string]any)
		got[fm["flag"].(string)] = fm["kind"].(string)
	}
	want := map[string]string{
		"out": "string", "assert": "stringArray", "tolerance": "int",
		"full-res": "bool", "region": "string",
	}
	for flag, kind := range want {
		if got[flag] != kind {
			t.Errorf("cli flag --%s kind = %q, want %q", flag, got[flag], kind)
		}
	}
	if screenshot["response"] == "" {
		t.Error("editor screenshot must carry its response note")
	}
	if flags, ok := state["cli_flags"].([]any); !ok || len(flags) != 0 {
		t.Errorf("editor state cli_flags = %v, want empty array", state["cli_flags"])
	}
}

// TestOpCommandsRegisterCLIFlags: every CLIFlagSpec in the table is
// actually registered on its cobra command (the -h surface cannot drift
// from the JSON/markdown surface).
func TestOpCommandsRegisterCLIFlags(t *testing.T) {
	root := NewRootCommand()
	for _, op := range ops.All() {
		if len(op.CLIFlags) == 0 {
			continue
		}
		cmd, _, err := root.Find([]string{op.Domain, op.Name})
		if err != nil {
			t.Fatalf("find %s %s: %v", op.Domain, op.Name, err)
		}
		for _, f := range op.CLIFlags {
			if cmd.Flags().Lookup(f.Flag) == nil {
				t.Errorf("%s %s: CLI flag --%s declared but not registered",
					op.Domain, op.Name, f.Flag)
			}
		}
	}
}
