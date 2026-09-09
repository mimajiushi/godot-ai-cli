package pluginmeta_test

import (
	"testing"

	"github.com/mimajiushi/godot-ai-cli/internal/pluginmeta"
)

// The bridge advertises PluginVersion() in every handshake_ack and the
// handshake gate checks it for major.minor compatibility against the
// connecting plugin — pin the value so a vendored-plugin bump without a
// server-side follow-up fails loudly.
func TestPluginVersion(t *testing.T) {
	if got := pluginmeta.PluginVersion(); got != "3.2.11" {
		t.Fatalf("PluginVersion() = %q, want %q", got, "3.2.11")
	}
}

// TestParseSemver pins the strict three-segment shape: the version gate
// must never guess at two-segment, suffixed, or garbage versions.
func TestParseSemver(t *testing.T) {
	valid := map[string]pluginmeta.Semver{
		"3.2.8":   {Major: 3, Minor: 2, Patch: 8},
		"0.0.1":   {Major: 0, Minor: 0, Patch: 1},
		"10.20.3": {Major: 10, Minor: 20, Patch: 3},
	}
	for raw, want := range valid {
		got, err := pluginmeta.ParseSemver(raw)
		if err != nil || got != want {
			t.Errorf("ParseSemver(%q) = %+v, %v; want %+v", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", "3.2", "3.2.8.1", "3.2.x", "v3.2.8", "3.2.8-beta.1", "3.-2.8", "3.02.8"} {
		if _, err := pluginmeta.ParseSemver(raw); err == nil {
			t.Errorf("ParseSemver(%q) succeeded, want error", raw)
		}
	}
}

// TestCompatible pins the handshake compatibility contract: major.minor
// equality, patch free to drift in BOTH directions (3.2.6↔3.2.7); minor
// and major mismatches never interoperate.
func TestCompatible(t *testing.T) {
	mustParse := func(raw string) pluginmeta.Semver {
		v, err := pluginmeta.ParseSemver(raw)
		if err != nil {
			t.Fatalf("ParseSemver(%q): %v", raw, err)
		}
		return v
	}
	cases := []struct {
		a, b string
		want bool
	}{
		{"3.2.8", "3.2.8", true},  // identical
		{"3.2.6", "3.2.7", true},  // patch older plugin, accepted (stale)
		{"3.2.7", "3.2.6", true},  // patch newer plugin, accepted (stale)
		{"3.2.8", "3.3.0", false}, // minor drift rejected
		{"3.3.0", "3.2.8", false}, // minor drift, either direction
		{"3.2.8", "4.2.0", false}, // major drift rejected
		{"4.0.0", "3.9.9", false}, // major drift, either direction
	}
	for _, c := range cases {
		if got := pluginmeta.Compatible(mustParse(c.a), mustParse(c.b)); got != c.want {
			t.Errorf("Compatible(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestCompare pins the ordering used for the stale note's direction sign.
func TestCompare(t *testing.T) {
	mustParse := func(raw string) pluginmeta.Semver {
		v, err := pluginmeta.ParseSemver(raw)
		if err != nil {
			t.Fatalf("ParseSemver(%q): %v", raw, err)
		}
		return v
	}
	cases := []struct {
		a, b string
		want int
	}{
		{"3.2.6", "3.2.7", -1},
		{"3.2.7", "3.2.6", 1},
		{"3.2.7", "3.2.7", 0},
		{"3.10.0", "3.2.9", 1},
		{"2.9.9", "3.0.0", -1},
	}
	for _, c := range cases {
		if got := pluginmeta.Compare(mustParse(c.a), mustParse(c.b)); got != c.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
