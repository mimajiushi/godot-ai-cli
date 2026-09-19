package cli

import (
	"bytes"
	"strings"
	"testing"
)

// The version output must surface every compatibility-relevant version:
// the CLI itself, the supported Godot range, and the bundled plugin
// (upstream godot-ai) version the daemon checks handshakes against.
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
		"supported Godot:     4.7+ (4.7+ recommended)",
		"bundled plugin:      godot-ai v4.1.0",
		"protocol version:    2",
		"plugin command coverage:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("version output missing %q\ngot:\n%s", want, out)
		}
	}
}
