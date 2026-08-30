package godot

import "time"

// IsProcessRunning reports whether pid refers to a live process.
func IsProcessRunning(pid int) bool {
	return pid > 0 && processRunning(pid)
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
