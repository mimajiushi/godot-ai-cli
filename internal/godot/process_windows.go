//go:build windows

package godot

import "syscall"

// processRunning reports whether pid refers to a live process.
func processRunning(pid int) bool {
	const processQueryLimitedInformation = 0x1000
	const stillActive = 259
	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)
	var code uint32
	if err := syscall.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	return code == stillActive
}
