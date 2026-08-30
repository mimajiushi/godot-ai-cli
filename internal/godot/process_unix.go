//go:build !windows

package godot

import "syscall"

// processRunning reports whether pid refers to a live process.
func processRunning(pid int) bool {
	// Signal 0 is the portable liveness probe; EPERM still means alive.
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
