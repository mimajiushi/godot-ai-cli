// Package pluginmeta exposes metadata parsed from the vendored godot_ai
// editor plugin descriptor (plugin.cfg) embedded by the plugin package.
//
// The plugin enforces a STRICT equality check between its own plugin.cfg
// version and the server_version reported in the WebSocket handshake_ack
// (see plugin utils/server_lifecycle.gd _server_version_compatibility),
// so the version exposed here is the single source of truth the bridge
// must advertise.
//
// The actual parsing lives in the plugin package (plugin.PluginVersion);
// this package remains as the stable internal-facing accessor.
package pluginmeta

import (
	"github.com/mimajiushi/godot-ai-cli/plugin"
)

// PluginVersion returns the version declared by the vendored godot_ai
// editor plugin (e.g. "3.2.4").
func PluginVersion() string {
	return plugin.PluginVersion()
}
