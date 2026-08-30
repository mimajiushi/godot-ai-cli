package plugin

import (
	"strings"
	"testing"
)

// TestInstallPlanWritesPluginCFGLast pins the Install invariant: a kill
// mid-extract must never leave a version-matching plugin.cfg without the
// full file tree behind it (InstalledVersion keys off plugin.cfg, so it
// doubles as the "install complete" marker).
func TestInstallPlanWritesPluginCFGLast(t *testing.T) {
	plan, err := installPlan()
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) < 200 {
		t.Errorf("plan has only %d files — the embed may be incomplete", len(plan))
	}
	if last := plan[len(plan)-1]; last != "godot_ai/plugin.cfg" {
		t.Errorf("last planned file = %q, want godot_ai/plugin.cfg", last)
	}
	count := 0
	for _, path := range plan {
		if strings.HasSuffix(path, "/plugin.cfg") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("plugin.cfg appears %d times in the plan, want exactly 1", count)
	}
}
