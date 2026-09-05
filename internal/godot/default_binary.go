// Default Godot binary memory: `godot use` records an explicit user-picked
// Godot binary in <user config dir>/godot-ai-cli/godot-bin.json so custom
// install layouts (portable zips, non-standard directories) work without a
// permanent GODOT_BIN environment variable. Find and Candidates consult the
// record between GODOT_BIN and the PATH lookup. A missing or corrupt file
// is silently tolerated (LoadDefaultBinary reports !ok); a record pointing
// at a since-deleted binary is a loud Find error, never a silent
// fall-through.
package godot

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// defaultBinaryRecord is the persisted shape of the `godot use` record.
type defaultBinaryRecord struct {
	GodotBin string `json:"godot_bin"`
}

// defaultBinaryPath returns where the record lives:
// <user config dir>/godot-ai-cli/godot-bin.json.
func defaultBinaryPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "godot-ai-cli", "godot-bin.json")
}

// LoadDefaultBinary reads the default Godot binary saved by `godot use`.
// A missing, corrupt, or empty record reports !ok — the record is a hint,
// never an error source (same tolerance contract as the daemon's
// last-daemon.json).
func LoadDefaultBinary() (string, bool) {
	data, err := os.ReadFile(defaultBinaryPath())
	if err != nil {
		return "", false
	}
	var rec defaultBinaryRecord
	if err := json.Unmarshal(data, &rec); err != nil || rec.GodotBin == "" {
		return "", false
	}
	return rec.GodotBin, true
}

// SaveDefaultBinary persists path (in absolute form) as the default Godot
// binary Find consults after GODOT_BIN.
func SaveDefaultBinary(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	data, err := json.Marshal(defaultBinaryRecord{GodotBin: abs})
	if err != nil {
		return err
	}
	p := defaultBinaryPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// ClearDefaultBinary removes the saved record; a missing file is not an
// error.
func ClearDefaultBinary() error {
	if err := os.Remove(defaultBinaryPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
