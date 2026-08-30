package cli

import (
	"testing"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
	"github.com/mimajiushi/godot-ai-cli/internal/godot"
	"github.com/mimajiushi/godot-ai-cli/internal/pluginmeta"
)

// TestSettingsMutationGate pins when launch may touch the user's global
// EditorSettings. With default ports and no conflicting managed-server
// record the plugin's own adoption works — no mutation at all.
func TestSettingsMutationGate(t *testing.T) {
	defaults := launchOptions{httpPort: daemon.DefaultHTTPPort, wsPort: daemon.DefaultWSPort}
	custom := launchOptions{httpPort: 18099, wsPort: 19599}
	version := pluginmeta.PluginVersion()

	cases := []struct {
		name   string
		opts   launchOptions
		record godot.ManagedRecord
		want   bool
	}{
		{"default ports, no record", defaults, godot.ManagedRecord{}, false},
		{
			// Record for an OLDER plugin version is not trusted by the
			// plugin — it resolves ports from its settings (= defaults).
			"default ports, stale-version record",
			defaults,
			godot.ManagedRecord{Present: true, Version: "3.1.0", WSPort: 12345},
			false,
		},
		{
			"default ports, record matching our daemon",
			defaults,
			godot.ManagedRecord{Present: true, Version: version, WSPort: daemon.DefaultWSPort},
			false,
		},
		{
			// The trap case: a trusted record pinning a foreign WS port
			// would make the plugin reject our daemon as ws_port_mismatch.
			"default ports, trusted record pinning another ws port",
			defaults,
			godot.ManagedRecord{Present: true, Version: version, WSPort: 9501},
			true,
		},
		{
			"custom ports, no record",
			custom, godot.ManagedRecord{}, true,
		},
		{
			"custom ports, matching record",
			custom,
			godot.ManagedRecord{Present: true, Version: version, WSPort: 19599},
			true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := settingsMutationNeeded(c.opts, c.record); got != c.want {
				t.Errorf("settingsMutationNeeded(%+v, %+v) = %v, want %v",
					c.opts, c.record, got, c.want)
			}
		})
	}
}
