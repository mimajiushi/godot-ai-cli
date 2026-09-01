package cli

import (
	"testing"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
	"github.com/mimajiushi/godot-ai-cli/internal/godot"
	"github.com/mimajiushi/godot-ai-cli/internal/pluginmeta"
)

// TestSettingsMutationGate pins when launch may touch the user's global
// EditorSettings. With default ports and no conflicting managed-server
// record or live port override the plugin's own adoption works — no
// mutation at all.
func TestSettingsMutationGate(t *testing.T) {
	defaults := launchOptions{httpPort: daemon.DefaultHTTPPort, wsPort: daemon.DefaultWSPort}
	custom := launchOptions{httpPort: 18099, wsPort: 19599}
	version := pluginmeta.PluginVersion()

	cases := []struct {
		name   string
		opts   launchOptions
		record godot.ManagedRecord
		cur    godot.PluginPorts
		want   bool
	}{
		{"default ports, no record", defaults, godot.ManagedRecord{}, godot.PluginPorts{}, false},
		{
			// Record for an OLDER plugin version is not trusted by the
			// plugin — it resolves ports from its settings (= defaults).
			"default ports, stale-version record",
			defaults,
			godot.ManagedRecord{Present: true, Version: "3.1.0", WSPort: 12345},
			godot.PluginPorts{},
			false,
		},
		{
			"default ports, record matching our daemon",
			defaults,
			godot.ManagedRecord{Present: true, Version: version, WSPort: daemon.DefaultWSPort},
			godot.PluginPorts{},
			false,
		},
		{
			// The trap case: a trusted record pinning a foreign WS port
			// would make the plugin reject our daemon as ws_port_mismatch.
			"default ports, trusted record pinning another ws port",
			defaults,
			godot.ManagedRecord{Present: true, Version: version, WSPort: 9501},
			godot.PluginPorts{},
			true,
		},
		{
			// The multi-project trap: another session's custom-port
			// overrides are live in the shared EditorSettings while the
			// managed record reads clean (editor save races blank it). A
			// default launch must mutate (and hit the backup guard) instead
			// of sending our editor's plugin to the other daemon.
			"default ports, live overrides pointing elsewhere",
			defaults,
			godot.ManagedRecord{},
			godot.PluginPorts{HTTPPort: 18002, WSPort: 19502, HTTPPresent: true, WSPresent: true},
			true,
		},
		{
			"default ports, live overrides matching defaults",
			defaults,
			godot.ManagedRecord{},
			godot.PluginPorts{HTTPPort: 8000, WSPort: 9500, HTTPPresent: true, WSPresent: true},
			false,
		},
		{
			"default ports, only ws override differs",
			defaults,
			godot.ManagedRecord{},
			godot.PluginPorts{WSPort: 19502, WSPresent: true},
			true,
		},
		{
			"default ports, only http override differs",
			defaults,
			godot.ManagedRecord{},
			godot.PluginPorts{HTTPPort: 18002, HTTPPresent: true},
			true,
		},
		{
			"custom ports, no record",
			custom, godot.ManagedRecord{}, godot.PluginPorts{}, true,
		},
		{
			"custom ports, matching record",
			custom,
			godot.ManagedRecord{Present: true, Version: version, WSPort: 19599},
			godot.PluginPorts{},
			true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := settingsMutationNeeded(c.opts, c.record, c.cur); got != c.want {
				t.Errorf("settingsMutationNeeded(%+v, %+v, %+v) = %v, want %v",
					c.opts, c.record, c.cur, got, c.want)
			}
		})
	}
}
