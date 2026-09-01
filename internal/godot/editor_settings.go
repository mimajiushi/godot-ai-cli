package godot

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// The godot_ai plugin resolves its HTTP/WS ports from EditorSettings
// (godot_ai/http_port, godot_ai/ws_port) and REFUSES to adopt a server
// whose status ws_port differs from its expectation. So a launch with
// non-default ports must write those overrides into the per-version
// EditorSettings file before the editor starts.
//
// EditorSettings is a text resource: [gd_resource] header, sub_resources,
// then one trailing [resource] section whose properties run to EOF, which
// makes "replace the line or append at EOF" a safe edit.

// editorConfigDir returns the per-OS Godot editor config directory.
func editorConfigDir() (string, error) {
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return "", fmt.Errorf("APPDATA is not set")
		}
		return filepath.Join(appData, "Godot"), nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "Godot"), nil
	default:
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, "godot"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".config", "godot"), nil
	}
}

// EditorSettingsPath returns the version-tagged EditorSettings file for a
// Godot version (e.g. editor_settings-4.7.tres for 4.7.x).
func EditorSettingsPath(v Version) (string, error) {
	dir, err := editorConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("editor_settings-%d.%d.tres", v.Major, v.Minor)), nil
}

// httpPortLine / wsPortLine match existing override lines in the file.
var (
	httpPortLine = regexp.MustCompile(`(?m)^godot_ai/http_port = .*$`)
	wsPortLine   = regexp.MustCompile(`(?m)^godot_ai/ws_port = .*$`)
)

// Managed-record lines: the plugin pins its expected WS port (and adoption
// target) to this record whenever its version matches the installed
// plugin. A stale record from a previous upstream install would otherwise
// override our http/ws port overrides and make adoption fail with
// ws_port_mismatch.
var (
	managedPIDLine     = regexp.MustCompile(`(?m)^godot_ai/managed_server_pid = .*$`)
	managedVersionLine = regexp.MustCompile(`(?m)^godot_ai/managed_server_version = .*$`)
	managedWSPortLine  = regexp.MustCompile(`(?m)^godot_ai/managed_server_ws_port = .*$`)
	managedTokenLine   = regexp.MustCompile(`(?m)^godot_ai/managed_server_ws_token = .*$`)
)

// SetPluginPorts writes godot_ai/http_port and godot_ai/ws_port overrides
// into the editor's EditorSettings file, creating a minimal file when the
// editor has never saved one. It reports whether the file changed.
//
// All other content is preserved byte-for-byte. Call this BEFORE launching
// the editor — a running editor rewrites the file on exit.
func SetPluginPorts(v Version, httpPort, wsPort int) (changed bool, err error) {
	path, err := EditorSettingsPath(v)
	if err != nil {
		return false, err
	}

	httpLine := fmt.Sprintf("godot_ai/http_port = %d", httpPort)
	wsLine := fmt.Sprintf("godot_ai/ws_port = %d", wsPort)

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// The editor has never saved settings: seed a minimal valid file.
		content := "[gd_resource type=\"EditorSettings\" format=3]\n\n[resource]\n" +
			httpLine + "\n" + wsLine + "\n"
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return false, err
		}
		return true, os.WriteFile(path, []byte(content), 0o644)
	}
	if err != nil {
		return false, err
	}

	content := string(data)
	nl := "\n"
	if strings.Contains(content, "\r\n") {
		nl = "\r\n"
	}

	content = replaceOrAppend(content, httpPortLine, httpLine, nl)
	content = replaceOrAppend(content, wsPortLine, wsLine, nl)

	if content == string(data) {
		return false, nil // overrides already in place
	}
	return true, os.WriteFile(path, []byte(content), 0o644)
}

// ManagedRecord is the plugin's persisted managed-server record subset
// that launch's mutation gate cares about.
type ManagedRecord struct {
	// Present is true when a managed_server_pid/version line exists.
	Present bool
	PID     int
	Version string
	WSPort  int
}

// ReadManagedRecord parses the managed-server record from the editor's
// EditorSettings file. A missing file or missing keys yield a zero record
// with Present=false and no error.
func ReadManagedRecord(v Version) (ManagedRecord, error) {
	path, err := EditorSettingsPath(v)
	if err != nil {
		return ManagedRecord{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ManagedRecord{}, nil
	}
	if err != nil {
		return ManagedRecord{}, err
	}
	content := string(data)
	var record ManagedRecord
	if m := keyValueLine("godot_ai/managed_server_pid").FindStringSubmatch(content); m != nil {
		record.Present = true
		record.PID, _ = strconv.Atoi(strings.TrimSpace(m[1]))
	}
	if m := keyValueLine("godot_ai/managed_server_version").FindStringSubmatch(content); m != nil {
		record.Present = true
		record.Version = strings.Trim(strings.TrimSpace(m[1]), `"`)
	}
	if m := keyValueLine("godot_ai/managed_server_ws_port").FindStringSubmatch(content); m != nil {
		record.WSPort, _ = strconv.Atoi(strings.TrimSpace(m[1]))
	}
	return record, nil
}

// PluginPorts carries the live godot_ai/http_port and godot_ai/ws_port
// overrides found in an editor settings file. Each Present flag reports
// whether that key was found at all; the int is meaningful only then.
type PluginPorts struct {
	HTTPPort    int
	WSPort      int
	HTTPPresent bool
	WSPresent   bool
}

// ReadPluginPorts reads the CURRENT godot_ai port overrides from the editor
// settings of the given Godot version. Every failure (no file, unreadable
// file, missing keys) is reported as absent — this is a best-effort input
// to launch's mutation gate, never an error source.
func ReadPluginPorts(v Version) PluginPorts {
	var ports PluginPorts
	path, err := EditorSettingsPath(v)
	if err != nil {
		return ports
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ports
	}
	content := string(data)
	if m := keyValueLine("godot_ai/http_port").FindStringSubmatch(content); m != nil {
		if n, err := strconv.Atoi(strings.TrimSpace(m[1])); err == nil {
			ports.HTTPPort, ports.HTTPPresent = n, true
		}
	}
	if m := keyValueLine("godot_ai/ws_port").FindStringSubmatch(content); m != nil {
		if n, err := strconv.Atoi(strings.TrimSpace(m[1])); err == nil {
			ports.WSPort, ports.WSPresent = n, true
		}
	}
	return ports
}

// SetPluginManagedServer pins the plugin's managed-server record to our
// daemon, mirroring what the plugin itself writes after a managed spawn
// (plugin.gd _write_managed_server_record). Without this, a stale record
// from a previous upstream install pins the plugin's expected WS port and
// makes adoption of our custom-port daemon fail with ws_port_mismatch.
//
// The token is cleared: our bridge accepts tokenless handshakes, and a
// stale token would be a "present but wrong" shape on stricter servers.
// All other content is preserved byte-for-byte; returns whether the file
// changed. Call BEFORE launching the editor.
func SetPluginManagedServer(v Version, pid, wsPort int, version string) (changed bool, err error) {
	path, err := EditorSettingsPath(v)
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err // SetPluginPorts runs first in the launch flow
	}
	content := string(data)
	nl := "\n"
	if strings.Contains(content, "\r\n") {
		nl = "\r\n"
	}
	content = replaceOrAppend(content, managedPIDLine, fmt.Sprintf("godot_ai/managed_server_pid = %d", pid), nl)
	content = replaceOrAppend(content, managedVersionLine, fmt.Sprintf("godot_ai/managed_server_version = %q", version), nl)
	content = replaceOrAppend(content, managedWSPortLine, fmt.Sprintf("godot_ai/managed_server_ws_port = %d", wsPort), nl)
	content = replaceOrAppend(content, managedTokenLine, `godot_ai/managed_server_ws_token = ""`, nl)

	if content == string(data) {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(content), 0o644)
}

// replaceOrAppend replaces the first line matching pattern, or appends the
// line at EOF (inside the trailing [resource] section). The replacement
// goes through ReplaceAllStringFunc: a backed-up value containing "$1" or
// similar must be written back literally, not expanded as a group reference.
func replaceOrAppend(content string, pattern *regexp.Regexp, line, nl string) string {
	if pattern.MatchString(content) {
		return pattern.ReplaceAllStringFunc(content, func(string) string { return line })
	}
	if !strings.HasSuffix(content, "\n") {
		content += nl
	}
	return content + line + nl
}
