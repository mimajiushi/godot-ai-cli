package godot

import (
	"path/filepath"
	"strings"
	"time"
)

// IsProcessRunning reports whether pid refers to a live process.
func IsProcessRunning(pid int) bool {
	return pid > 0 && processRunning(pid)
}

// IsGodotEditorProcess reports whether pid refers to a live process whose
// executable image looks like a Godot editor binary. A bare liveness probe
// is not enough: Windows recycles pids aggressively, so a quit editor's pid
// can be reassigned to an unrelated process (observed live: pid reused by
// svchost), which would make stop veto the EditorSettings restore forever.
func IsGodotEditorProcess(pid int) bool {
	return isGodotEditorPID(pid, processImageName)
}

// isGodotEditorPID is the pure core of IsGodotEditorProcess; imageName
// (injected for tests) resolves a pid to its executable image path and must
// fail when the pid names no live process, which folds the liveness probe
// into the identity check. A failed image lookup is treated as "not a
// running editor": the query only fails when the process exited in the
// race window or belongs to a foreign, access-restricted process — exactly
// the pid-reuse case this check exists to reject — whereas vetoing on an
// unverifiable pid would resurrect the recycled-pid bug.
func isGodotEditorPID(pid int, imageName func(int) (string, error)) bool {
	if pid <= 0 {
		return false
	}
	image, err := imageName(pid)
	if err != nil {
		return false
	}
	return looksLikeGodotEditorImage(image)
}

// looksLikeGodotEditorImage matches the executable's base name against the
// Godot editor naming scheme: bare "godot", or "godot" followed by a
// version/platform separator (Godot_v4.5.1-stable_win64.exe, Godot.x86_64,
// godot-4.5) or a digit (distro-style godot4). Case-insensitive: Godot
// ships capitalized Windows binaries. Prefix-adjacent tools (godotenv and
// friends) deliberately do not count.
func looksLikeGodotEditorImage(image string) bool {
	// Image paths may use Windows separators on any host (tests, logs), so
	// normalize before taking the base name.
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(image, "\\", "/")))
	base = strings.TrimSuffix(base, ".exe")
	rest := strings.TrimPrefix(base, "godot")
	if rest == base {
		return false
	}
	if rest == "" {
		return true
	}
	c := rest[0]
	return c == '_' || c == '.' || c == '-' || (c >= '0' && c <= '9')
}

// WaitProcessExit blocks until the process exits or the timeout elapses,
// returning true when the process is gone. Callers use it to sequence
// after a graceful quit request (e.g. restoring EditorSettings only after
// the editor's exit-time write has landed).
func WaitProcessExit(pid int, timeout time.Duration) bool {
	if pid <= 0 {
		return true
	}
	deadline := time.Now().Add(timeout)
	for processRunning(pid) {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
	return true
}
