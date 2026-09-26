package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mimajiushi/godot-ai-cli/internal/pluginmeta"
)

// The version output must surface every compatibility-relevant version:
// the CLI itself, the supported Godot range, and the bundled plugin
// (upstream godot-ai) version the daemon checks handshakes against.
// 内嵌插件版本行动态取自 pluginmeta（不写死版本号——升版不该改测试）。
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
		"bundled plugin:      godot-ai v" + pluginmeta.PluginVersion(),
		"protocol version:    2",
		"plugin command coverage:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("version output missing %q\ngot:\n%s", want, out)
		}
	}
}
