package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mimajiushi/godot-ai-cli/internal/testutil/mockplugin"
	"github.com/mimajiushi/godot-ai-cli/plugin"
)

// runPluginCmd executes the plugin command group with args and decodes what it
// printed.
func runPluginCmd(t *testing.T, args ...string) (map[string]any, error) {
	t.Helper()
	cmd := newPluginCommand()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs(args)
	err := cmd.Execute()
	if buf.Len() == 0 {
		return nil, err
	}
	var out map[string]any
	if jerr := json.Unmarshal(buf.Bytes(), &out); jerr != nil {
		t.Fatalf("plugin command printed non-JSON: %v\n%s", jerr, buf.String())
	}
	return out, err
}

// writeProjectFile writes a minimal Godot project and returns its directory.
func writeProjectFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "project.godot"),
		[]byte("[application]\n\nconfig/name=\"Demo\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestPluginStatusFreshProject: nothing installed → the report says so and
// still carries the pending write plan.
func TestPluginStatusFreshProject(t *testing.T) {
	dir := writeProjectFile(t)
	out, err := runPluginCmd(t, "status", "--project", dir)
	if err != nil {
		t.Fatalf("plugin status: %v (%v)", err, out)
	}
	if out["installed"] != false || out["enabled"] != false {
		t.Errorf("fresh project reported installed/enabled: %v", out)
	}
	if out["compatible"] != true {
		t.Errorf("nothing installed must count as compatible: %v", out["compatible"])
	}
	plan, _ := out["plugin"].(map[string]any)
	if n, _ := plan["would_create_count"].(float64); n < 200 {
		t.Errorf("would_create_count = %v", plan["would_create_count"])
	}
	if plan["version_match"] != true {
		t.Errorf("version_match = %v", plan["version_match"])
	}
}

// TestPluginStatusReportsDrift: an installed-but-patch-drifted plugin is
// reported as installed, incompatible-free (patch drift is accepted by the
// handshake) but version_match=false with exactly the files that would change.
func TestPluginStatusReportsDrift(t *testing.T) {
	dir := writeProjectFile(t)
	if _, err := plugin.EnsureInstalled(dir); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "addons", "godot_ai", "plugin.cfg")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	// 与 bundled（4.2.x）同 major.minor 的 patch 漂移——握手仍判兼容。
	if err := os.WriteFile(cfgPath,
		[]byte(strings.Replace(string(cfg), `version="`+plugin.PluginVersion()+`"`, `version="4.2.9"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runPluginCmd(t, "status", "--project", dir)
	if err != nil {
		t.Fatalf("plugin status: %v (%v)", err, out)
	}
	if out["installed"] != true || out["enabled"] != true {
		t.Errorf("installed/enabled = %v / %v", out["installed"], out["enabled"])
	}
	if out["compatible"] != true {
		t.Errorf("4.2.9 vs %s is major.minor compatible: %v", plugin.PluginVersion(), out["compatible"])
	}
	plan, _ := out["plugin"].(map[string]any)
	if plan["version_match"] != false {
		t.Errorf("version_match = %v, want false", plan["version_match"])
	}
	if plan["installed_version"] != "4.2.9" {
		t.Errorf("installed_version = %v", plan["installed_version"])
	}
	update, _ := plan["would_update"].([]any)
	if len(update) != 1 || update[0] != "addons/godot_ai/plugin.cfg" {
		t.Errorf("would_update = %v", plan["would_update"])
	}
}

// TestPluginStatusIncompatibleMinor: a minor drift is reported as
// incompatible (the handshake would refuse it) instead of as a mere mismatch.
func TestPluginStatusIncompatibleMinor(t *testing.T) {
	dir := writeProjectFile(t)
	if _, err := plugin.EnsureInstalled(dir); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "addons", "godot_ai", "plugin.cfg")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath,
		[]byte(strings.Replace(string(cfg), `version="`+plugin.PluginVersion()+`"`, `version="4.0.0"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runPluginCmd(t, "status", "--project", dir)
	if err != nil {
		t.Fatalf("plugin status: %v (%v)", err, out)
	}
	if out["compatible"] != false {
		t.Errorf("4.0.0 vs %s must report compatible=false: %v", plugin.PluginVersion(), out["compatible"])
	}
}

// TestPluginInstallDryRunWritesNothing: the preview must not create the addon
// tree or enable the plugin.
func TestPluginInstallDryRunWritesNothing(t *testing.T) {
	dir := writeProjectFile(t)
	out, err := runPluginCmd(t, "install", "--project", dir, "--dry-run")
	if err != nil {
		t.Fatalf("plugin install --dry-run: %v (%v)", err, out)
	}
	if out["dry_run"] != true || out["status"] != "ok" {
		t.Errorf("payload = %v", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "addons")); !os.IsNotExist(err) {
		t.Errorf("--dry-run created the addon tree (err=%v)", err)
	}
	cfg, err := os.ReadFile(filepath.Join(dir, "project.godot"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfg), "godot_ai") {
		t.Error("--dry-run enabled the plugin in project.godot")
	}
}

// TestPluginInstallVersionGuard: --version is a guard, not a picker — this CLI
// embeds exactly one plugin version.
func TestPluginInstallVersionGuard(t *testing.T) {
	dir := writeProjectFile(t)
	out, err := runPluginCmd(t, "install", "--project", dir, "--version", "9.9.9")
	if err == nil {
		t.Fatal("expected --version 9.9.9 to fail")
	}
	env := errorEnvelope(t, out)
	if env["code"] != "PLUGIN_VERSION_UNSUPPORTED" {
		t.Errorf("error code = %v", env["code"])
	}
	data, _ := env["data"].(map[string]any)
	if data["requested_version"] != "9.9.9" || data["bundled_version"] != plugin.PluginVersion() {
		t.Errorf("data = %v", data)
	}
	// The bundled version itself is accepted.
	if _, err := runPluginCmd(t, "install", "--project", dir, "--version", plugin.PluginVersion()); err != nil {
		t.Errorf("--version %s must be accepted: %v", plugin.PluginVersion(), err)
	}
}

// TestPluginStatusRejectsNonProject: a directory without project.godot is an
// INVALID_PROJECT input error.
func TestPluginStatusRejectsNonProject(t *testing.T) {
	out, err := runPluginCmd(t, "status", "--project", t.TempDir())
	if err == nil {
		t.Fatal("expected INVALID_PROJECT")
	}
	if env := errorEnvelope(t, out); env["code"] != "INVALID_PROJECT" {
		t.Errorf("error code = %v", env["code"])
	}
}

// TestPluginInstallWarnsOnInMemoryMismatch：磁盘安装成功后，若 daemon 报告
// 本工程的编辑器内存里仍是旧插件版本（已连接 session 的 plugin_version
// 或被拒握手的 peer_version），install 输出必须带 PROJECT_PLUGIN_MISMATCH
// warning + next_steps（需求 handshake-rejection-visibility §4.2）。
func TestPluginInstallWarnsOnInMemoryMismatch(t *testing.T) {
	dir := stubCacheDir(t)
	d := startRecordedDaemon(t, dir, "4.2.5")
	projectDir := writeProjectFile(t)

	// 该工程有一个被拒的握手：peer 插件 4.1.0（旧 minor），磁盘将装 4.2.5。
	addr := fmt.Sprintf("127.0.0.1:%d", d.WSPort())
	mockplugin.DialRejected(t, addr, d.Bridge().WSCapability, map[string]any{
		"session_id": "pi@0001", "plugin_version": "4.1.0", "editor_pid": 5020,
		"project_path": projectDir,
	})

	out, err := runPluginCmd(t, "install", "--project", projectDir,
		"--http-port", strconv.Itoa(d.HTTPPort()))
	if err != nil {
		t.Fatalf("plugin install: %v (%v)", err, out)
	}
	if out["version"] != plugin.PluginVersion() {
		t.Fatalf("installed version = %v", out["version"])
	}
	warning, _ := out["warning"].(string)
	if !strings.Contains(warning, "PROJECT_PLUGIN_MISMATCH") || !strings.Contains(warning, "完全退出并重新打开编辑器") {
		t.Errorf("warning = %q", warning)
	}
	if !strings.Contains(warning, "4.1.0") {
		t.Errorf("warning must name the in-memory version: %q", warning)
	}
	steps, ok := out["next_steps"].([]any)
	if !ok || len(steps) != 2 {
		t.Errorf("next_steps = %v", out["next_steps"])
	}
}

// TestPluginInstallCleanDaemonNoWarning：daemon 上没有本工程的旧版本痕迹
// 时，install 输出保持原形状（无 warning 键）。
func TestPluginInstallCleanDaemonNoWarning(t *testing.T) {
	dir := stubCacheDir(t)
	d := startRecordedDaemon(t, dir, "4.2.5")
	projectDir := writeProjectFile(t)

	out, err := runPluginCmd(t, "install", "--project", projectDir,
		"--http-port", strconv.Itoa(d.HTTPPort()))
	if err != nil {
		t.Fatalf("plugin install: %v (%v)", err, out)
	}
	if _, present := out["warning"]; present {
		t.Errorf("unexpected warning on a clean daemon: %v", out["warning"])
	}
}
