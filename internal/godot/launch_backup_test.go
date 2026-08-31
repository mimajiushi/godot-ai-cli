package godot_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mimajiushi/godot-ai-cli/internal/godot"
)

// withCacheDir redirects os.UserCacheDir into a temp dir for one test.
//
// darwin trap: both os.UserCacheDir and the production editorConfigDir
// resolve through $HOME on macOS, so a test that also redirects the editor
// config via withEditorConfig MUST pass that same root here — otherwise the
// second HOME assignment wins and the two sides read/write different trees
// (this combination only fails on macOS; Windows/Linux use distinct envs).
func withCacheDir(t *testing.T, darwinSharedHome ...string) {
	t.Helper()
	dir := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("LOCALAPPDATA", dir)
	case "darwin":
		home := dir
		if len(darwinSharedHome) > 0 {
			home = darwinSharedHome[0]
		}
		t.Setenv("HOME", home)
	default:
		t.Setenv("XDG_CACHE_HOME", dir)
		t.Setenv("HOME", dir)
	}
}

// testVersion47 is the Godot version all backup tests use.
var testVersion47 = godot.Version{Major: 4, Minor: 7, Patch: 2}

// readBackup loads the captured backup file for assertions.
func readBackup(t *testing.T, httpPort int) godot.SettingsBackup {
	t.Helper()
	data, err := os.ReadFile(godot.LaunchBackupPath(httpPort))
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	var backup godot.SettingsBackup
	if err := json.Unmarshal(data, &backup); err != nil {
		t.Fatalf("parse backup: %v", err)
	}
	return backup
}

func TestCaptureBackupVirginKeys(t *testing.T) {
	root := withEditorConfig(t)
	withCacheDir(t, root)

	created, err := godot.CaptureLaunchBackup(testVersion47, 18099, "C:/proj")
	if err != nil || !created {
		t.Fatalf("CaptureLaunchBackup = %v, %v", created, err)
	}
	backup := readBackup(t, 18099)
	if backup.FilePresent {
		t.Error("FilePresent = true, want false (no settings file existed)")
	}
	if backup.Project != "C:/proj" || backup.EditorSettingsPath == "" || backup.CreatedAt == "" {
		t.Errorf("backup metadata incomplete: %+v", backup)
	}
	for _, key := range godot.ManagedSettingKeys {
		value, ok := backup.Keys[key]
		if !ok {
			t.Errorf("key %s missing from backup", key)
			continue
		}
		if value.Present {
			t.Errorf("key %s captured as present on a virgin settings file", key)
		}
	}
}

func TestCaptureBackupRecordsExistingValues(t *testing.T) {
	root := withEditorConfig(t)
	withCacheDir(t, root)

	path := settingsFile(t, root, testVersion47)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "[gd_resource type=\"EditorSettings\" format=3]\n\n[resource]\n" +
		"godot_ai/http_port = 8000\n" +
		"godot_ai/managed_server_version = \"3.2.4\"\n" +
		"godot_ai/managed_server_ws_token = \"secret\"\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := godot.CaptureLaunchBackup(testVersion47, 18099, "C:/proj"); err != nil {
		t.Fatal(err)
	}
	backup := readBackup(t, 18099)
	if !backup.FilePresent {
		t.Error("FilePresent = false, want true")
	}
	if got := backup.Keys["godot_ai/http_port"]; !got.Present || got.Value != "8000" {
		t.Errorf("http_port captured as %+v", got)
	}
	if got := backup.Keys["godot_ai/managed_server_version"]; !got.Present || got.Value != `"3.2.4"` {
		t.Errorf("managed_server_version captured as %+v", got)
	}
	if got := backup.Keys["godot_ai/ws_port"]; got.Present {
		t.Errorf("ws_port was absent but captured as %+v", got)
	}
}

func TestCaptureBackupKeepsOriginal(t *testing.T) {
	root := withEditorConfig(t)
	withCacheDir(t, root)
	path := settingsFile(t, root, testVersion47)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "[resource]\ngodot_ai/http_port = 8000\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := godot.CaptureLaunchBackup(testVersion47, 18099, "C:/proj")
	if err != nil || !created {
		t.Fatalf("first capture = %v, %v", created, err)
	}

	// Simulate a mutated settings file (our own launch overrides), then a
	// second capture — it must NOT overwrite the original backup.
	mutated := "[resource]\ngodot_ai/http_port = 18099\n"
	if err := os.WriteFile(path, []byte(mutated), 0o644); err != nil {
		t.Fatal(err)
	}
	created, err = godot.CaptureLaunchBackup(testVersion47, 18099, "C:/proj")
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("second capture overwrote the original backup")
	}
	if got := readBackup(t, 18099).Keys["godot_ai/http_port"].Value; got != "8000" {
		t.Errorf("backup holds %q, want the original 8000", got)
	}
}

func TestRestoreRoundTripByteIdentical(t *testing.T) {
	root := withEditorConfig(t)
	withCacheDir(t, root)
	path := settingsFile(t, root, testVersion47)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := `[gd_resource type="EditorSettings" format=3]

[resource]
interface/editor/localization/editor_language = "auto"
godot_ai/http_port = 8000
godot_ai/ws_port = 9500
godot_ai/telemetry_enabled = true
godot_ai/managed_server_pid = 44168
godot_ai/managed_server_version = "3.2.4"
godot_ai/managed_server_ws_port = 9500
godot_ai/managed_server_ws_token = "deadbeef"
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := godot.CaptureLaunchBackup(testVersion47, 18099, "C:/proj"); err != nil {
		t.Fatal(err)
	}
	// Simulate the launch mutations.
	if _, err := godot.SetPluginPorts(testVersion47, 18099, 19599); err != nil {
		t.Fatal(err)
	}
	if _, err := godot.SetPluginManagedServer(testVersion47, 999, 19599, "3.2.4"); err != nil {
		t.Fatal(err)
	}

	restored, err := godot.RestoreLaunchBackup(18099)
	if err != nil || !restored {
		t.Fatalf("RestoreLaunchBackup = %v, %v", restored, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Errorf("restore not byte-identical:\n--- original ---\n%q\n--- restored ---\n%q", original, after)
	}
	// The backup file is consumed by a successful restore.
	if _, err := os.Stat(godot.LaunchBackupPath(18099)); !os.IsNotExist(err) {
		t.Error("backup file should be deleted after a successful restore")
	}
}

func TestRestoreRemovesKeysThatWereAbsent(t *testing.T) {
	root := withEditorConfig(t)
	withCacheDir(t, root)
	path := settingsFile(t, root, testVersion47)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "[gd_resource type=\"EditorSettings\" format=3]\n\n[resource]\ninterface/editor/docks/dock_tab_style = 0\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := godot.CaptureLaunchBackup(testVersion47, 18099, "C:/proj"); err != nil {
		t.Fatal(err)
	}
	if _, err := godot.SetPluginPorts(testVersion47, 18099, 19599); err != nil {
		t.Fatal(err)
	}
	if _, err := godot.SetPluginManagedServer(testVersion47, 999, 19599, "3.2.4"); err != nil {
		t.Fatal(err)
	}

	if _, err := godot.RestoreLaunchBackup(18099); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != original {
		t.Errorf("appended keys not removed byte-identically:\n%q\nwant:\n%q", after, original)
	}
}

func TestRestoreDeletesFileWeCreated(t *testing.T) {
	root := withEditorConfig(t) // no settings file exists
	withCacheDir(t, root)

	if _, err := godot.CaptureLaunchBackup(testVersion47, 18099, "C:/proj"); err != nil {
		t.Fatal(err)
	}
	// Launch creates the settings file from scratch.
	if _, err := godot.SetPluginPorts(testVersion47, 18099, 19599); err != nil {
		t.Fatal(err)
	}
	path, err := godot.EditorSettingsPath(testVersion47)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("settings file should exist after SetPluginPorts: %v", err)
	}

	if _, err := godot.RestoreLaunchBackup(18099); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("settings file we created should be deleted on restore")
	}
}

func TestRestoreNoBackupIsNoop(t *testing.T) {
	withCacheDir(t)
	restored, err := godot.RestoreLaunchBackup(18099)
	if err != nil || restored {
		t.Errorf("RestoreLaunchBackup without backup = %v, %v, want false, nil", restored, err)
	}
}
