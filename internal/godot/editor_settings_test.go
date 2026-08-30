package godot_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mimajiushi/godot-ai-cli/internal/godot"
)

// withEditorConfig redirects the editor config dir into a temp dir for
// the duration of one test.
func withEditorConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", dir)
	case "darwin":
		t.Setenv("HOME", dir)
	default:
		t.Setenv("XDG_CONFIG_HOME", dir)
	}
	return dir
}

// settingsFile computes where SetPluginPorts should have written.
func settingsFile(t *testing.T, configRoot string, v godot.Version) string {
	t.Helper()
	var dir string
	switch runtime.GOOS {
	case "windows":
		dir = filepath.Join(configRoot, "Godot")
	case "darwin":
		dir = filepath.Join(configRoot, "Library", "Application Support", "Godot")
	default:
		dir = filepath.Join(configRoot, "godot")
	}
	return filepath.Join(dir, "editor_settings-4.7.tres")
}

func TestSetPluginPortsCreatesMissingFile(t *testing.T) {
	root := withEditorConfig(t)
	v := godot.Version{Major: 4, Minor: 7, Patch: 2}

	changed, err := godot.SetPluginPorts(v, 18099, 19599)
	if err != nil || !changed {
		t.Fatalf("SetPluginPorts = %v, %v", changed, err)
	}
	data, err := os.ReadFile(settingsFile(t, root, v))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`[gd_resource type="EditorSettings" format=3]`,
		"[resource]",
		"godot_ai/http_port = 18099",
		"godot_ai/ws_port = 19599",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("created file missing %q:\n%s", want, text)
		}
	}
}

func TestSetPluginPortsReplacesExisting(t *testing.T) {
	root := withEditorConfig(t)
	v := godot.Version{Major: 4, Minor: 7, Patch: 2}
	path := settingsFile(t, root, v)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := `[gd_resource type="EditorSettings" format=3]

[resource]
interface/editor/localization/editor_language = "auto"
godot_ai/http_port = 8000
godot_ai/ws_port = 9500
godot_ai/telemetry_enabled = true
`
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := godot.SetPluginPorts(v, 18099, 19599)
	if err != nil || !changed {
		t.Fatalf("SetPluginPorts = %v, %v", changed, err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	if !strings.Contains(text, "godot_ai/http_port = 18099") ||
		!strings.Contains(text, "godot_ai/ws_port = 19599") {
		t.Errorf("ports not replaced:\n%s", text)
	}
	if !strings.Contains(text, `interface/editor/localization/editor_language = "auto"`) ||
		!strings.Contains(text, "godot_ai/telemetry_enabled = true") {
		t.Errorf("unrelated settings lost:\n%s", text)
	}

	// Idempotent: same values → no change.
	changed, err = godot.SetPluginPorts(v, 18099, 19599)
	if err != nil || changed {
		t.Errorf("second SetPluginPorts = %v, %v, want false, nil", changed, err)
	}
}

func TestReadManagedRecord(t *testing.T) {
	root := withEditorConfig(t)
	v := godot.Version{Major: 4, Minor: 7, Patch: 2}

	// No settings file → absent record, no error.
	record, err := godot.ReadManagedRecord(v)
	if err != nil || record.Present {
		t.Errorf("missing file: %+v, %v", record, err)
	}

	path := settingsFile(t, root, v)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `[gd_resource type="EditorSettings" format=3]

[resource]
godot_ai/managed_server_pid = 44168
godot_ai/managed_server_version = "3.2.4"
godot_ai/managed_server_ws_port = 9500
godot_ai/managed_server_ws_token = "deadbeef"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	record, err = godot.ReadManagedRecord(v)
	if err != nil {
		t.Fatal(err)
	}
	if !record.Present || record.PID != 44168 || record.Version != "3.2.4" || record.WSPort != 9500 {
		t.Errorf("record = %+v", record)
	}
}

func TestSetPluginManagedServerRepinsRecord(t *testing.T) {
	root := withEditorConfig(t)
	v := godot.Version{Major: 4, Minor: 7, Patch: 2}
	path := settingsFile(t, root, v)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// Shape observed on a real machine after an upstream managed server.
	before := `[gd_resource type="EditorSettings" format=3]

[resource]
godot_ai/http_port = 18099
godot_ai/ws_port = 19599
godot_ai/managed_server_pid = 44168
godot_ai/managed_server_version = "3.2.4"
godot_ai/managed_server_ws_port = 9500
godot_ai/managed_server_ws_token = "deadbeef"
godot_ai/managed_server_keep_alive = false
`
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := godot.SetPluginManagedServer(v, 12345, 19599, "3.2.4")
	if err != nil || !changed {
		t.Fatalf("SetPluginManagedServer = %v, %v", changed, err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	for _, want := range []string{
		"godot_ai/managed_server_pid = 12345",
		`godot_ai/managed_server_version = "3.2.4"`,
		"godot_ai/managed_server_ws_port = 19599",
		`godot_ai/managed_server_ws_token = ""`,
		"godot_ai/managed_server_keep_alive = false", // untouched
		"godot_ai/ws_port = 19599",                   // port override untouched
	} {
		if !strings.Contains(text, want) {
			t.Errorf("managed record missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "44168") || strings.Contains(text, "deadbeef") {
		t.Errorf("stale record values survived:\n%s", text)
	}
}

func TestSetPluginPortsAppendsWhenKeyAbsent(t *testing.T) {
	root := withEditorConfig(t)
	v := godot.Version{Major: 4, Minor: 7, Patch: 2}
	path := settingsFile(t, root, v)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "[gd_resource type=\"EditorSettings\" format=3]\n\n[resource]\ninterface/editor/docks/dock_tab_style = 0\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := godot.SetPluginPorts(v, 9000, 9001); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	if !strings.HasPrefix(text, before) {
		t.Errorf("existing content was not preserved as a prefix:\n%s", text)
	}
	if !strings.Contains(text, "godot_ai/http_port = 9000") ||
		!strings.Contains(text, "godot_ai/ws_port = 9001") {
		t.Errorf("overrides not appended:\n%s", text)
	}
}
