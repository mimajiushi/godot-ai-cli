// Package pluginmeta exposes metadata parsed from the vendored godot_ai
// editor plugin descriptor (plugin.cfg) embedded by the plugin package.
//
// The plugin↔daemon handshake is compatible on MAJOR.MINOR equality
// (see plugin utils/server_lifecycle.gd _server_version_compatibility):
// a patch-level drift (3.2.6 plugin vs 3.2.7 daemon) is accepted and
// flagged as plugin_stale, only a minor/major mismatch is rejected. The
// version exposed here is the single source of truth the bridge
// advertises, and Semver/Compatible are the single source of truth for
// the compatibility rule on the Go side.
//
// The actual descriptor parsing lives in the plugin package
// (plugin.PluginVersion); this package remains as the stable
// internal-facing accessor.
package pluginmeta

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mimajiushi/godot-ai-cli/plugin"
)

// PluginVersion returns the version declared by the vendored godot_ai
// editor plugin (e.g. "3.2.11").
func PluginVersion() string {
	return plugin.PluginVersion()
}

// Semver is a parsed three-segment semantic version.
type Semver struct {
	Major int
	Minor int
	Patch int
}

// ParseSemver parses a strict "major.minor.patch" version — exactly three
// non-negative numeric segments, no pre-release/build suffixes. Anything
// else (empty, two segments, garbage) is an error: the version gate must
// never guess.
func ParseSemver(raw string) (Semver, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Semver{}, fmt.Errorf("version %q is not major.minor.patch", raw)
	}
	var v Semver
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || strconv.Itoa(n) != part {
			return Semver{}, fmt.Errorf("version %q has a non-numeric segment %q", raw, part)
		}
		switch i {
		case 0:
			v.Major = n
		case 1:
			v.Minor = n
		case 2:
			v.Patch = n
		}
	}
	return v, nil
}

// Compatible reports whether two versions may interoperate: major and
// minor must match, the patch segment may drift freely (3.2.6 ↔ 3.2.7).
// A malformed input is never compatible.
func Compatible(a, b Semver) bool {
	return a.Major == b.Major && a.Minor == b.Minor
}

// Compare orders two versions lexicographically by segment: -1 when a is
// older, 0 when equal, +1 when newer.
func Compare(a, b Semver) int {
	if a.Major != b.Major {
		return cmpInt(a.Major, b.Major)
	}
	if a.Minor != b.Minor {
		return cmpInt(a.Minor, b.Minor)
	}
	return cmpInt(a.Patch, b.Patch)
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
