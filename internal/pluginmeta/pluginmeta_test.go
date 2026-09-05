package pluginmeta_test

import (
	"testing"

	"github.com/mimajiushi/godot-ai-cli/internal/pluginmeta"
)

// The bridge advertises PluginVersion() in every handshake_ack and the
// plugin strict-checks it against its own plugin.cfg — pin the value so a
// vendored-plugin bump without a server-side follow-up fails loudly.
func TestPluginVersion(t *testing.T) {
	if got := pluginmeta.PluginVersion(); got != "3.2.5" {
		t.Fatalf("PluginVersion() = %q, want %q", got, "3.2.5")
	}
}
