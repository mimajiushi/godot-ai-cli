package godot

import (
	"strings"
	"testing"
)

// envValue reports the value of the last KEY=value entry for key in env
// (last wins, matching how duplicate entries resolve in child processes).
func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	found := false
	value := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			value = strings.TrimPrefix(kv, prefix)
			found = true
		}
	}
	return value, found
}

// TestEditorEnvMarksCliLaunch: every editor the CLI spawns carries
// GODOT_AI_CLI_LAUNCHED=1 (the plugin turns this into launched_by="cli" in
// its handshake); the headless opt-in stays headless-only.
func TestEditorEnvMarksCliLaunch(t *testing.T) {
	for _, headless := range []bool{false, true} {
		env := editorEnv(headless)
		if v, ok := envValue(env, "GODOT_AI_CLI_LAUNCHED"); !ok || v != "1" {
			t.Errorf("editorEnv(%v): GODOT_AI_CLI_LAUNCHED = %q (present %v), want \"1\"",
				headless, v, ok)
		}
		_, hasHeadless := envValue(env, "GODOT_AI_ALLOW_HEADLESS")
		if hasHeadless != headless {
			t.Errorf("editorEnv(%v): GODOT_AI_ALLOW_HEADLESS present = %v, want %v",
				headless, hasHeadless, headless)
		}
	}
}
