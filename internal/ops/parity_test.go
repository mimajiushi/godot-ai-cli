package ops_test

import (
	"os"
	"regexp"
	"testing"

	"github.com/mimajiushi/godot-ai-cli/internal/ops"
)

// registeredCommands parses the vendored plugin's register_lazy lines —
// the ground truth for the plugin's command surface. The three client
// commands appear here too although the fork guards them off at runtime;
// InternalOnly documents them, so they stay in the expected set.
func registeredCommands(t *testing.T) map[string]bool {
	t.Helper()
	data, err := os.ReadFile("../../plugin/godot_ai/plugin.gd")
	if err != nil {
		t.Fatalf("read vendored plugin.gd: %v", err)
	}
	re := regexp.MustCompile(`register_lazy\("([a-z_0-9]+)"`)
	out := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(data), -1) {
		out[m[1]] = true
	}
	if len(out) < 100 {
		t.Fatalf("only %d register_lazy lines parsed — the plugin layout may have moved", len(out))
	}
	return out
}

// TestPluginCommandParity pins: ops.All() PluginCommands ∪ InternalOnly
// == the vendored plugin's registered command set. No missing, no extra,
// and no accidental duplicates (game_command is the one legitimate
// duplicate: every game-domain op routes through that wrapper).
func TestPluginCommandParity(t *testing.T) {
	registered := registeredCommands(t)

	covered := map[string][]string{} // plugin command → ops exposing it
	for _, op := range ops.All() {
		covered[op.PluginCommand] = append(covered[op.PluginCommand], op.Domain+" "+op.Name)
	}
	for _, spec := range ops.InternalOnly {
		covered[spec.Command] = append(covered[spec.Command], "(internal-only)")
	}

	for cmd := range registered {
		if _, ok := covered[cmd]; !ok {
			t.Errorf("registered plugin command %q has no CLI op and is not in InternalOnly", cmd)
		}
	}
	for cmd, exposedBy := range covered {
		if !registered[cmd] {
			t.Errorf("CLI exposes %q (%v) but the plugin does not register it", cmd, exposedBy)
		}
		if len(exposedBy) > 1 && cmd != "game_command" {
			t.Errorf("plugin command %q exposed by multiple ops: %v (only game_command may be shared)", cmd, exposedBy)
		}
	}

	// Every InternalOnly entry must name a REAL registered command —
	// otherwise the justification comment rots.
	for _, spec := range ops.InternalOnly {
		if !registered[spec.Command] {
			t.Errorf("InternalOnly entry %q is not a registered plugin command", spec.Command)
		}
		if spec.Reason == "" {
			t.Errorf("InternalOnly entry %q lacks a justification", spec.Command)
		}
	}
}

// TestErrorCodeParity: every error code the bridge can emit must exist in
// the plugin's error_codes.gd, or be a documented transport-side-only code
// (the plugin never sees those — they describe bridge-local failures).
func TestErrorCodeParity(t *testing.T) {
	data, err := os.ReadFile("../../plugin/godot_ai/utils/error_codes.gd")
	if err != nil {
		t.Fatalf("read error_codes.gd: %v", err)
	}
	pluginCodes := map[string]bool{}
	codeRe := regexp.MustCompile(`:=\s*"([A-Z_0-9]+)"`)
	for _, m := range codeRe.FindAllStringSubmatch(string(data), -1) {
		pluginCodes[m[1]] = true
	}

	// Codes that exist only on the Go side, with the reason why.
	cliSideOnly := map[string]string{
		"TRANSPORT_TIMEOUT":   "bridge-local WS round-trip timeout; the plugin's DEFERRED_TIMEOUT covers the editor side",
		"PLUGIN_DISCONNECTED": "the session died mid-flight; the plugin cannot emit a code for its own disconnect",
		"SESSION_NOT_FOUND":   "bridge-local routing failure (unknown session id); the plugin never sees unrouted commands",
	}

	bridgeDir, err := os.ReadDir("../bridge")
	if err != nil {
		t.Fatalf("read bridge dir: %v", err)
	}
	refRe := regexp.MustCompile(`Code:\s*"([A-Z_0-9]+)"`)
	found := map[string]bool{}
	for _, entry := range bridgeDir {
		if entry.IsDir() || len(entry.Name()) < 3 || entry.Name()[len(entry.Name())-3:] != ".go" {
			continue
		}
		src, err := os.ReadFile("../bridge/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range refRe.FindAllStringSubmatch(string(src), -1) {
			found[m[1]] = true
		}
	}
	if len(found) == 0 {
		t.Fatal("no error codes found in internal/bridge — the scan regex may be stale")
	}
	for code := range found {
		if pluginCodes[code] {
			continue
		}
		if _, ok := cliSideOnly[code]; ok {
			continue
		}
		t.Errorf("bridge emits %q which is neither in plugin error_codes.gd nor documented as CLI-side-only", code)
	}
}

// kebabName matches the CLI op-name convention.
var kebabName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// TestTableConventions enforces the table invariants the generator and the
// help output rely on.
func TestTableConventions(t *testing.T) {
	seen := map[string]bool{}
	for _, op := range ops.All() {
		key := op.Domain + "/" + op.Name
		if seen[key] {
			t.Errorf("duplicate op %s", key)
		}
		seen[key] = true

		if err := op.Validate(); err != nil {
			t.Errorf("%v", err)
		}
		if !kebabName.MatchString(op.Name) {
			t.Errorf("op %s: name %q is not kebab-case", key, op.Name)
		}
		if !kebabName.MatchString(op.Domain) {
			t.Errorf("op %s: domain %q is not kebab-case", key, op.Domain)
		}
		for _, p := range op.Params {
			if !kebabName.MatchString(p.Flag) {
				t.Errorf("op %s: flag %q is not kebab-case", key, p.Flag)
			}
			if p.Flag == "session" || p.Flag == "params" {
				t.Errorf("op %s: flag %q collides with the reserved shared flag", key, p.Flag)
			}
			if p.Usage == "" {
				t.Errorf("op %s: param --%s lacks usage text", key, p.Flag)
			}
			// The wire key must survive a round trip (snake_case expected).
			if p.Param == "" {
				t.Errorf("op %s: param --%s has an empty wire key", key, p.Flag)
			}
		}
	}
	if got := len(ops.All()); got < 130 {
		t.Errorf("only %d ops — expected 148; the table lost entries", got)
	}
}

// TestNamedToolCoverage pins the 4 always-loaded core tools and the 15
// high-traffic verbs from docs/TOOLS.md to their CLI op (or, for
// session activate, to the hand-wired daemon-side leaf).
func TestNamedToolCoverage(t *testing.T) {
	// Upstream tool → CLI op. session_activate is hand-wired in the CLI
	// (daemon-side, no plugin command), everything else lives in the table.
	named := map[string][2]string{
		// 4 core
		"editor_state":        {"editor", "state"},
		"scene_get_hierarchy": {"scene", "get-hierarchy"},
		"node_get_properties": {"node", "get-properties"},
		// 15 verbs
		"batch_execute":        {"batch", "execute"},
		"node_create":          {"node", "create"},
		"node_set_property":    {"node", "set-property"},
		"node_find":            {"node", "find"},
		"scene_open":           {"scene", "open"},
		"scene_save":           {"scene", "save"},
		"script_create":        {"script", "create"},
		"script_attach":        {"script", "attach"},
		"script_patch":         {"script", "patch"},
		"project_run":          {"project", "run"},
		"test_run":             {"test", "run"},
		"logs_read":            {"logs", "read"},
		"editor_screenshot":    {"editor", "screenshot"},
		"editor_reload_plugin": {"editor", "reload-plugin"},
		"animation_create":     {"animation", "create"},
	}
	for tool, want := range named {
		if _, ok := ops.Lookup(want[0], want[1]); !ok {
			t.Errorf("named tool %s: op %s/%s missing from the table", tool, want[0], want[1])
		}
	}
	// The 4th core tool, session_activate, is pinned separately: it must
	// stay hand-wired (documented in ops.HandWiredLeaves).
	found := false
	for _, leaf := range ops.HandWiredLeaves {
		if leaf == "session activate" {
			found = true
		}
	}
	if !found {
		t.Error("session activate must remain a hand-wired leaf (see ops.HandWiredLeaves)")
	}
}
