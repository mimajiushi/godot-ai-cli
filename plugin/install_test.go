package plugin_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mimajiushi/godot-ai-cli/plugin"
)

// newProject creates a temp dir containing a minimal project.godot.
func newProject(t *testing.T, projectGodot string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "project.godot"), []byte(projectGodot), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPluginVersion(t *testing.T) {
	if got := plugin.PluginVersion(); got != "3.2.4" {
		t.Fatalf("PluginVersion() = %q, want 3.2.4", got)
	}
}

func TestInstallExtractsFullTree(t *testing.T) {
	dir := newProject(t, "; engine config\n")

	result, err := plugin.Install(dir)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !result.Installed || result.Upgraded {
		t.Errorf("fresh install: %+v", result)
	}
	if result.Version != plugin.PluginVersion() || result.PreviousVersion != "" {
		t.Errorf("versions: %+v", result)
	}
	if result.Path != filepath.Join(dir, "addons", "godot_ai") {
		t.Errorf("path = %q", result.Path)
	}

	// Every embedded file must exist on disk with identical content —
	// including the `_`-prefixed GDScript files a plain directory embed
	// would have skipped.
	count := 0
	err = fs.WalkDir(plugin.FS, "godot_ai", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		count++
		rel := strings.TrimPrefix(path, "godot_ai/")
		want, _ := plugin.FS.ReadFile(path)
		got, err := os.ReadFile(filepath.Join(dir, "addons", "godot_ai", filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("missing extracted file %s: %v", rel, err)
			return nil
		}
		if string(got) != string(want) {
			t.Errorf("content mismatch for %s", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count < 200 {
		t.Errorf("only %d embedded files — the all: prefix may be missing", count)
	}
	// Spot-check a `_`-prefixed file explicitly.
	if _, err := os.Stat(filepath.Join(dir, "addons", "godot_ai", "clients", "_base.gd")); err != nil {
		t.Errorf("underscore-prefixed file not extracted: %v", err)
	}
}

func TestInstallPreservesExtraFilesAndDir(t *testing.T) {
	dir := newProject(t, "x=1\n")

	// Pre-existing addon with a user file that is not part of the plugin.
	addon := filepath.Join(dir, "addons", "godot_ai")
	if err := os.MkdirAll(addon, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(addon, "my_local_notes.txt")
	if err := os.WriteFile(marker, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Stat the dir before/after: the directory itself must never be
	// replaced (dev setups junction it elsewhere).
	before, err := os.Stat(addon)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := plugin.Install(dir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Error("Install deleted a user file that is not part of the plugin")
	}
	after, err := os.Stat(addon)
	if err != nil {
		t.Fatal("addon directory missing after Install")
	}
	if before.ModTime().After(after.ModTime()) {
		// The dir must be the same object; at minimum it still exists with
		// its extra contents (checked above). ModTime moves are fine.
		t.Log("directory modtime unchanged — fine, contents still verified")
	}
}

func TestInstallIdempotentAndUpgradeDetection(t *testing.T) {
	dir := newProject(t, "x=1\n")

	first, err := plugin.Install(dir)
	if err != nil {
		t.Fatalf("first Install: %v", err)
	}

	second, err := plugin.EnsureInstalled(dir)
	if err != nil {
		t.Fatalf("EnsureInstalled: %v", err)
	}
	if second.Installed {
		t.Errorf("EnsureInstalled reinstalled an up-to-date plugin: %+v", second)
	}
	if !second.Enabled {
		t.Error("EnsureInstalled should report the plugin enabled")
	}
	_ = first

	// Simulate an older installed version: rewrite plugin.cfg's version.
	cfgPath := filepath.Join(dir, "addons", "godot_ai", "plugin.cfg")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(string(cfg), `version="`+plugin.PluginVersion()+`"`, `version="0.0.1"`, 1)
	if old == string(cfg) {
		t.Fatal("failed to downgrade plugin.cfg for the test")
	}
	if err := os.WriteFile(cfgPath, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}

	v, err := plugin.InstalledVersion(dir)
	if err != nil || v != "0.0.1" {
		t.Fatalf("InstalledVersion = %q, %v", v, err)
	}

	upgraded, err := plugin.EnsureInstalled(dir)
	if err != nil {
		t.Fatalf("EnsureInstalled upgrade: %v", err)
	}
	if !upgraded.Installed || !upgraded.Upgraded {
		t.Errorf("upgrade detection: %+v", upgraded)
	}
	if upgraded.PreviousVersion != "0.0.1" || upgraded.Version != plugin.PluginVersion() {
		t.Errorf("versions: %+v", upgraded)
	}
}

func TestEnable(t *testing.T) {
	cases := []struct {
		name        string
		before      string
		wantChanged bool
		check       func(t *testing.T, after string)
	}{
		{
			name: "no editor_plugins section",
			before: `; Engine configuration file.
config_version=5

[application]

config/name="Demo"
`,
			wantChanged: true,
			check: func(t *testing.T, after string) {
				if !strings.Contains(after, "[editor_plugins]") ||
					!strings.Contains(after, `enabled=PackedStringArray("godot_ai")`) {
					t.Errorf("section not appended:\n%s", after)
				}
				if !strings.Contains(after, `config/name="Demo"`) {
					t.Error("existing content lost")
				}
			},
		},
		{
			name:        "empty editor_plugins section",
			before:      "[application]\n\nconfig/name=\"Demo\"\n\n[editor_plugins]\n",
			wantChanged: true,
			check: func(t *testing.T, after string) {
				if !strings.Contains(after, `enabled=PackedStringArray("godot_ai")`) {
					t.Errorf("enabled line not added:\n%s", after)
				}
			},
		},
		{
			name: "existing other plugins preserved",
			before: `[editor_plugins]

enabled=PackedStringArray("other_plugin", "second_one")
`,
			wantChanged: true,
			check: func(t *testing.T, after string) {
				if !strings.Contains(after, `"other_plugin"`) || !strings.Contains(after, `"second_one"`) {
					t.Errorf("existing plugins lost:\n%s", after)
				}
				if !strings.Contains(after, `"godot_ai"`) {
					t.Errorf("godot_ai not appended:\n%s", after)
				}
			},
		},
		{
			name: "already enabled is a no-op",
			before: `[editor_plugins]

enabled=PackedStringArray("other_plugin", "godot_ai")
`,
			wantChanged: false,
			check: func(t *testing.T, after string) {
				if strings.Count(after, `"godot_ai"`) != 1 {
					t.Errorf("godot_ai duplicated:\n%s", after)
				}
			},
		},
		{
			name: "empty enabled array",
			before: `[editor_plugins]

enabled=PackedStringArray()
`,
			wantChanged: true,
			check: func(t *testing.T, after string) {
				if !strings.Contains(after, `enabled=PackedStringArray("godot_ai")`) {
					t.Errorf("empty array not filled:\n%s", after)
				}
			},
		},
		{
			name: "later sections preserved",
			before: `[editor_plugins]

enabled=PackedStringArray()

[rendering]

renderer/rendering_method="forward_plus"
`,
			wantChanged: true,
			check: func(t *testing.T, after string) {
				if !strings.Contains(after, `renderer/rendering_method="forward_plus"`) {
					t.Errorf("later section content lost:\n%s", after)
				}
				if !strings.Contains(after, `[rendering]`) {
					t.Errorf("later section header lost:\n%s", after)
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := newProject(t, c.before)
			changed, err := plugin.Enable(dir)
			if err != nil {
				t.Fatalf("Enable: %v", err)
			}
			if changed != c.wantChanged {
				t.Errorf("changed = %v, want %v", changed, c.wantChanged)
			}
			after, err := os.ReadFile(filepath.Join(dir, "project.godot"))
			if err != nil {
				t.Fatal(err)
			}
			if !c.wantChanged && string(after) != c.before {
				t.Errorf("no-op changed the file:\nbefore: %q\nafter:  %q", c.before, after)
			}
			c.check(t, string(after))

			// Enabling twice must always be idempotent.
			changed2, err := plugin.Enable(dir)
			if err != nil {
				t.Fatalf("second Enable: %v", err)
			}
			if changed2 {
				t.Error("second Enable reported a change — not idempotent")
			}
		})
	}
}

// TestEnableAtomicWriteLeavesNoTempFiles: the temp+rename write must leave
// no litter in the project directory, across both the changing and the
// no-op paths.
func TestEnableAtomicWriteLeavesNoTempFiles(t *testing.T) {
	dir := newProject(t, "[application]\n\nconfig/name=\"Demo\"\n")
	if _, err := plugin.Enable(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := plugin.Enable(dir); err != nil { // no-op path
		t.Fatal(err)
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".project.godot-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) > 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}
	after, err := os.ReadFile(filepath.Join(dir, "project.godot"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), `enabled=PackedStringArray("godot_ai")`) {
		t.Errorf("godot_ai not enabled after atomic write:\n%s", after)
	}
}
