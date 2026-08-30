// Package plugin embeds the vendored godot_ai editor plugin and installs
// it into Godot projects.
//
// go:embed cannot reach outside the embedding package's directory, so the
// embed directives live here (next to godot_ai/) and internal packages
// import the exported data. Nothing under godot_ai/ is modified.
package plugin

import (
	"embed"
	"regexp"
	"sync"
)

// FS is the complete vendored plugin tree. The all: prefix is required:
// the tree contains many `_`-prefixed GDScript files that a plain
// directory embed would silently skip.
//
//go:embed all:godot_ai
var FS embed.FS

// PluginCFG is the raw content of the vendored plugin's plugin.cfg.
//
//go:embed godot_ai/plugin.cfg
var PluginCFG []byte

// versionLine matches the `version="x.y.z"` assignment inside plugin.cfg's
// [plugin] section. Anchored to a line start so a `version` key elsewhere
// (e.g. in the description text) cannot win.
var versionLine = regexp.MustCompile(`(?m)^version="([^"]+)"`)

// ParseVersion extracts the version from plugin.cfg content. It returns ""
// when no version line exists.
func ParseVersion(cfg []byte) string {
	match := versionLine.FindSubmatch(cfg)
	if match == nil {
		return ""
	}
	return string(match[1])
}

// parsedVersion extracts the embedded plugin version exactly once.
var parsedVersion = sync.OnceValue(func() string {
	if v := ParseVersion(PluginCFG); v != "" {
		return v
	}
	// The vendored descriptor always carries a version; "unknown" only
	// signals a corrupted embed and will fail the plugin's strict server
	// version check loudly instead of silently.
	return "unknown"
})

// PluginVersion returns the version declared by the vendored godot_ai
// editor plugin (e.g. "3.2.4"). The plugin enforces a STRICT equality
// check between this and the server_version reported in the WebSocket
// handshake_ack, so this is the single source of truth the bridge
// advertises.
func PluginVersion() string {
	return parsedVersion()
}
