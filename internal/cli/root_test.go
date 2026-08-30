package cli

import (
	"bytes"
	"strings"
	"testing"
)

// The version output must surface every compatibility-relevant version:
// the CLI itself, the supported Godot range, and the bundled plugin
// (upstream godot-ai) version the daemon strictly matches against.
func TestVersionOutputContainsCompatibilityInfo(t *testing.T) {
	cmd := NewRootCommand()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("version command: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"godot-ai-cli version",
		"supported Godot:     4.5+ (4.7+ recommended)",
		"bundled plugin:      godot-ai v3.2.4",
		"protocol version:    1",
		"plugin command coverage:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("version output missing %q\ngot:\n%s", want, out)
		}
	}
}
