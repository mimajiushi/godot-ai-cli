package godot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Per-project port pinning: launch records the daemon ports the project's
// editor should use in <project>/.godot/godot_ai_ports.json instead of the
// user's GLOBAL EditorSettings (the pre-3.2.9 behavior). The plugin
// resolves ports project file > EditorSettings > default, so several
// daemons on different ports can coexist without cross-wiring projects —
// and a launch no longer mutates state shared with other projects.
//
// The file lives under .godot (the editor's own metadata directory), which
// is git-ignored by convention, so the pin never leaks into VCS.

// ProjectPortsFileName is the contract path relative to the project root
// (res://.godot/godot_ai_ports.json).
const ProjectPortsFileName = ".godot/godot_ai_ports.json"

// ProjectPorts is the on-disk content of the per-project port file.
type ProjectPortsFile struct {
	HTTPPort int `json:"http_port"`
	WSPort   int `json:"ws_port"`
}

// ProjectPortsPath returns the port-file location for one project.
func ProjectPortsPath(projectDir string) string {
	return filepath.Join(projectDir, ProjectPortsFileName)
}

// WriteProjectPorts records the daemon ports for one project, creating the
// .godot directory when missing. Written even for default ports so the
// resolution is deterministic (the project file always wins over any
// leftover global EditorSettings pin).
func WriteProjectPorts(projectDir string, httpPort, wsPort int) error {
	payload, err := json.Marshal(ProjectPortsFile{HTTPPort: httpPort, WSPort: wsPort})
	if err != nil {
		return err
	}
	path := ProjectPortsPath(projectDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(payload, '\n'), 0o644)
}

// ReadProjectPorts loads one project's port file. A missing or corrupt
// file reports ok=false — the pin is a hint, never an error source.
func ReadProjectPorts(projectDir string) (ports ProjectPortsFile, ok bool) {
	data, err := os.ReadFile(ProjectPortsPath(projectDir))
	if err != nil {
		return ProjectPortsFile{}, false
	}
	if err := json.Unmarshal(data, &ports); err != nil || ports.HTTPPort <= 0 {
		return ProjectPortsFile{}, false
	}
	return ports, true
}

// HasProjectPorts reports whether the project carries a (parseable) port
// file — used by status's ports_override_active semantics.
func HasProjectPorts(projectDir string) bool {
	_, ok := ReadProjectPorts(projectDir)
	return ok
}

// RemoveProjectPorts deletes the port file, but ONLY when it still points
// at the daemon on httpPort: a newer launch on different ports may have
// rewritten it, and deleting that file would orphan its editor's pin. A
// missing file is a silent no-op; a corrupt or foreign-port file is left
// untouched (reported via the removed flag).
func RemoveProjectPorts(projectDir string, httpPort int) (removed bool, err error) {
	path := ProjectPortsPath(projectDir)
	ports, ok := ReadProjectPorts(projectDir)
	if !ok {
		if _, statErr := os.Stat(path); statErr != nil {
			return false, nil // absent: nothing to do
		}
		// Corrupt file: refuse to guess which daemon it pins.
		return false, fmt.Errorf("port file %s is unreadable — delete it by hand", path)
	}
	if ports.HTTPPort != httpPort {
		return false, nil // repinned by a newer launch on another daemon
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}
