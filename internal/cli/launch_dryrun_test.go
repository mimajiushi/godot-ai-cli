package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mimajiushi/godot-ai-cli/plugin"
)

// newLaunchTestProject writes a minimal Godot project and returns its dir.
func newLaunchTestProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "project.godot"),
		[]byte("[application]\n\nconfig/name=\"Demo\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// runLaunchJSON runs runLaunch with explicit options and decodes what it
// printed (nil map when nothing was printed).
func runLaunchJSON(t *testing.T, opts launchOptions) (map[string]any, error) {
	t.Helper()
	cmd := newLaunchCommand()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := runLaunch(cmd, opts)
	if buf.Len() == 0 {
		return nil, err
	}
	var out map[string]any
	if jerr := json.Unmarshal(buf.Bytes(), &out); jerr != nil {
		t.Fatalf("launch printed non-JSON: %v\n%s", jerr, buf.String())
	}
	return out, err
}

// errorEnvelope digs the protocol error object out of a printed envelope.
func errorEnvelope(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	if status, _ := out["status"].(string); status != "error" {
		t.Fatalf("expected an error envelope, got %v", out)
	}
	env, ok := out["error"].(map[string]any)
	if !ok {
		t.Fatalf("error envelope missing: %v", out)
	}
	return env
}

// TestLaunchDryRunWritesNothing: --dry-run reports the plugin write plan and
// exits before the Godot probe, the daemon, the editor, or any write. It is
// the answer to "may I launch here without dirtying the project?".
func TestLaunchDryRunWritesNothing(t *testing.T) {
	cacheDir := stubCacheDir(t)
	dir := newLaunchTestProject(t)

	out, err := runLaunchJSON(t, launchOptions{project: dir, dryRun: true})
	if err != nil {
		t.Fatalf("launch --dry-run returned an error: %v (%v)", err, out)
	}
	if out["status"] != "ok" || out["dry_run"] != true {
		t.Fatalf("payload = %v", out)
	}
	plan, ok := out["plugin"].(map[string]any)
	if !ok {
		t.Fatalf("payload has no plugin plan: %v", out)
	}
	if n, _ := plan["would_create_count"].(float64); n < 200 {
		t.Errorf("would_create_count = %v, want the embedded tree", plan["would_create_count"])
	}
	if plan["would_enable_plugin"] != true {
		t.Errorf("a fresh project must report would_enable_plugin: %v", plan["would_enable_plugin"])
	}
	// Nothing written: no addon tree, no daemon record.
	if _, err := os.Stat(filepath.Join(dir, "addons")); !os.IsNotExist(err) {
		t.Errorf("--dry-run created the addon tree (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "godot-ai-cli", "last-daemon.json")); !os.IsNotExist(err) {
		t.Errorf("--dry-run wrote a daemon record (err=%v)", err)
	}
	godotCfg, err := os.ReadFile(filepath.Join(dir, "project.godot"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(godotCfg), "godot_ai") {
		t.Error("--dry-run enabled the plugin in project.godot")
	}
}

// TestLaunchNoPluginUpgradeRefuses: a version-mismatched addon tree is NOT
// rewritten; the refusal names both versions and the impact, and the project
// stays byte-identical.
func TestLaunchNoPluginUpgradeRefuses(t *testing.T) {
	stubCacheDir(t)
	dir := newLaunchTestProject(t)
	if _, err := plugin.EnsureInstalled(dir); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "addons", "godot_ai", "plugin.cfg")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	downgraded := strings.Replace(string(cfg), `version="`+plugin.PluginVersion()+`"`, `version="3.2.7"`, 1)
	if downgraded == string(cfg) {
		t.Fatal("failed to downgrade plugin.cfg")
	}
	if err := os.WriteFile(cfgPath, []byte(downgraded), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runLaunchJSON(t, launchOptions{project: dir, noPluginUpgrade: true})
	if err == nil {
		t.Fatal("expected the refusal to fail the command")
	}
	env := errorEnvelope(t, out)
	if env["code"] != "PLUGIN_VERSION_MISMATCH" {
		t.Errorf("error code = %v", env["code"])
	}
	data, _ := env["data"].(map[string]any)
	if data["installed_version"] != "3.2.7" || data["bundled_version"] != plugin.PluginVersion() {
		t.Errorf("refusal data = %v", data)
	}
	if n, _ := data["would_update_count"].(float64); n != 1 {
		t.Errorf("would_update_count = %v, want 1", data["would_update_count"])
	}
	if hint, _ := data["hint"].(string); !strings.Contains(hint, "--no-plugin-upgrade") {
		t.Errorf("hint must name the flag: %v", data["hint"])
	}
	// The project was not touched.
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != downgraded {
		t.Error("the refusal modified plugin.cfg")
	}
}

// TestLaunchNoPluginUpgradeAllowsFreshAndMatching: the gate only refuses
// version mismatches — a fresh project and an in-sync project both continue
// into the normal pipeline (proved by the later deterministic Godot error
// with an explicit bogus --godot, so no editor can ever be spawned here).
func TestLaunchNoPluginUpgradeAllowsFreshAndMatching(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string)
	}{
		{name: "fresh project", setup: func(t *testing.T, _ string) {}},
		{name: "matching version", setup: func(t *testing.T, dir string) {
			if _, err := plugin.EnsureInstalled(dir); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stubCacheDir(t)
			dir := newLaunchTestProject(t)
			c.setup(t, dir)
			out, err := runLaunchJSON(t, launchOptions{
				project:         dir,
				noPluginUpgrade: true,
				godotBin:        filepath.Join(dir, "no-such-godot.exe"),
			})
			if err == nil {
				t.Fatalf("expected the Godot resolution step to fail, got success: %v", out)
			}
			env := errorEnvelope(t, out)
			if env["code"] != "GODOT_NOT_FOUND" {
				t.Errorf("error code = %v, want GODOT_NOT_FOUND (the plugin gate must let this through)", env["code"])
			}
		})
	}
}
