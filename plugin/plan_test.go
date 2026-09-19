package plugin_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mimajiushi/godot-ai-cli/plugin"
)

// gitRun runs one git command inside dir, failing the test on error.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-c", "user.email=test@example.com", "-c", "user.name=godot-ai-cli-test"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// requireGit skips the test when no git binary is available (the plugin's own
// answer must never depend on git, so this only gates the git assertions).
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
}

// TestPreviewFreshProject: nothing installed → every embedded file is a
// create, the plugin needs enabling, and git degrades gracefully outside a
// repository.
func TestPreviewFreshProject(t *testing.T) {
	dir := newProject(t, "[application]\n\nconfig/name=\"Demo\"\n")
	plan, err := plugin.Preview(dir)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if plan.InstalledVersion != "" {
		t.Errorf("InstalledVersion = %q, want empty", plan.InstalledVersion)
	}
	if plan.BundledVersion != plugin.PluginVersion() {
		t.Errorf("BundledVersion = %q", plan.BundledVersion)
	}
	if len(plan.WouldCreate) < 200 {
		t.Errorf("only %d creates — the embedded tree looks truncated", len(plan.WouldCreate))
	}
	if len(plan.WouldUpdate) != 0 {
		t.Errorf("fresh project reported updates: %v", plan.WouldUpdate[:min(3, len(plan.WouldUpdate))])
	}
	if !plan.WouldEnablePlugin {
		t.Error("a fresh project needs project.godot enabled")
	}
	if plan.VersionMismatch() {
		t.Error("a fresh install must not count as a version mismatch")
	}
	if !plan.RequiresInstall() {
		t.Error("RequiresInstall = false for a fresh project")
	}
	if plan.Git.Available {
		t.Errorf("git reported available outside a repository: %+v", plan.Git)
	}
	if plan.Git.Reason == "" {
		t.Error("git.Available=false must carry a Reason")
	}
	if plan.Git.UntrackedCreate != len(plan.WouldCreate) {
		t.Errorf("UntrackedCreate = %d, want %d", plan.Git.UntrackedCreate, len(plan.WouldCreate))
	}
}

// TestPreviewAfterInstallIsEmpty: an in-sync project has nothing to write.
func TestPreviewAfterInstallIsEmpty(t *testing.T) {
	dir := newProject(t, "[application]\n\nconfig/name=\"Demo\"\n")
	if _, err := plugin.EnsureInstalled(dir); err != nil {
		t.Fatalf("EnsureInstalled: %v", err)
	}
	plan, err := plugin.Preview(dir)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(plan.Changes) != 0 {
		t.Errorf("in-sync project still plans %d writes: %v", len(plan.Changes), plan.WouldUpdate)
	}
	if plan.WouldEnablePlugin {
		t.Error("the plugin is already enabled after EnsureInstalled")
	}
	if plan.RequiresInstall() {
		t.Error("RequiresInstall = true for an in-sync project")
	}
	if plan.InstalledVersion != plugin.PluginVersion() {
		t.Errorf("InstalledVersion = %q", plan.InstalledVersion)
	}
	if plan.VersionMismatch() {
		t.Error("VersionMismatch = true right after installing the bundled version")
	}
}

// TestPreviewVersionMismatch: a downgraded plugin.cfg is exactly one update and
// flips VersionMismatch (the input the launch gate acts on).
func TestPreviewVersionMismatch(t *testing.T) {
	dir := newProject(t, "[application]\n\nconfig/name=\"Demo\"\n")
	if _, err := plugin.EnsureInstalled(dir); err != nil {
		t.Fatalf("EnsureInstalled: %v", err)
	}
	cfgPath := filepath.Join(dir, "addons", "godot_ai", "plugin.cfg")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(string(cfg), `version="`+plugin.PluginVersion()+`"`, `version="3.2.7"`, 1)
	if old == string(cfg) {
		t.Fatal("failed to downgrade plugin.cfg")
	}
	if err := os.WriteFile(cfgPath, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := plugin.Preview(dir)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if plan.InstalledVersion != "3.2.7" {
		t.Errorf("InstalledVersion = %q, want 3.2.7", plan.InstalledVersion)
	}
	if !plan.VersionMismatch() {
		t.Error("VersionMismatch = false for a downgraded plugin.cfg")
	}
	if len(plan.WouldUpdate) != 1 || plan.WouldUpdate[0] != "addons/godot_ai/plugin.cfg" {
		t.Errorf("WouldUpdate = %v, want exactly the plugin.cfg entry", plan.WouldUpdate)
	}
	// The refusal the launch gate renders names both versions and the flag.
	refusal := (&plugin.VersionMismatchError{Plan: plan}).Error()
	if !strings.Contains(refusal, "3.2.7") || !strings.Contains(refusal, plugin.PluginVersion()) ||
		!strings.Contains(refusal, "--no-plugin-upgrade") {
		t.Errorf("refusal message = %q", refusal)
	}
}

// TestPreviewDetectsEditedFile: an edited embedded file is an update; a deleted
// one is a create.
func TestPreviewDetectsEditedFile(t *testing.T) {
	dir := newProject(t, "[application]\n\nconfig/name=\"Demo\"\n")
	if _, err := plugin.EnsureInstalled(dir); err != nil {
		t.Fatalf("EnsureInstalled: %v", err)
	}
	edited := filepath.Join(dir, "addons", "godot_ai", "utils", "path_validator.gd")
	if err := os.WriteFile(edited, []byte("# hand-edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "addons", "godot_ai", "mcp_dock.gd")); err != nil {
		t.Fatal(err)
	}
	plan, err := plugin.Preview(dir)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(plan.WouldUpdate) != 1 || plan.WouldUpdate[0] != "addons/godot_ai/utils/path_validator.gd" {
		t.Errorf("WouldUpdate = %v", plan.WouldUpdate)
	}
	if len(plan.WouldCreate) != 1 || plan.WouldCreate[0] != "addons/godot_ai/mcp_dock.gd" {
		t.Errorf("WouldCreate = %v", plan.WouldCreate)
	}
}

// TestPreviewGitImpact: inside a repository the plan reports the tracked /
// untracked split, so "which of these files are not my change" is answerable
// before anything is written.
func TestPreviewGitImpact(t *testing.T) {
	requireGit(t)
	dir := newProject(t, "[application]\n\nconfig/name=\"Demo\"\n")
	gitRun(t, dir, "init")
	if _, err := plugin.EnsureInstalled(dir); err != nil {
		t.Fatalf("EnsureInstalled: %v", err)
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "baseline", "--no-gpg-sign")

	plan, err := plugin.Preview(dir)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if !plan.Git.Available {
		t.Fatalf("git not detected inside a repository: %+v", plan.Git)
	}
	if plan.Git.TrackedFiles < 200 {
		t.Errorf("TrackedFiles = %d, want the whole committed addon tree", plan.Git.TrackedFiles)
	}
	if plan.Git.TrackedUpdate != 0 || plan.Git.DirtyNow {
		t.Errorf("clean in-sync tree reported drift: %+v", plan.Git)
	}

	// Dirty the tracked plugin.cfg: one tracked update, dirty now and after.
	cfgPath := filepath.Join(dir, "addons", "godot_ai", "plugin.cfg")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath,
		[]byte(strings.Replace(string(cfg), `version="`+plugin.PluginVersion()+`"`, `version="0.0.1"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err = plugin.Preview(dir)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if plan.Git.TrackedUpdate != 1 {
		t.Errorf("TrackedUpdate = %d, want 1 (the edited plugin.cfg)", plan.Git.TrackedUpdate)
	}
	if !plan.Git.DirtyNow || !plan.Git.DirtyAfter {
		t.Errorf("dirty flags = %+v, want both true", plan.Git)
	}

	// A file the tree never had is an untracked create.
	if err := os.Remove(filepath.Join(dir, "addons", "godot_ai", "clients", "_base.gd")); err != nil {
		t.Fatal(err)
	}
	plan, err = plugin.Preview(dir)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if plan.Git.UntrackedCreate != 1 {
		t.Errorf("UntrackedCreate = %d, want 1", plan.Git.UntrackedCreate)
	}
}

// TestPlanJSONShape pins the payload shared by launch --dry-run, plugin
// install --dry-run and plugin status.
func TestPlanJSONShape(t *testing.T) {
	dir := newProject(t, "[application]\n\nconfig/name=\"Demo\"\n")
	plan, err := plugin.Preview(dir)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	got := plan.JSON()
	for _, key := range []string{
		"project", "installed", "installed_version", "bundled_version", "version_match",
		"would_create", "would_update", "would_create_count", "would_update_count",
		"would_enable_plugin", "git",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("plan JSON is missing %q", key)
		}
	}
	if got["version_match"] != true {
		t.Errorf("version_match = %v for a fresh project", got["version_match"])
	}
	if got["would_create_count"] != len(plan.WouldCreate) {
		t.Errorf("would_create_count = %v, want %d", got["would_create_count"], len(plan.WouldCreate))
	}
}
