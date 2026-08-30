//go:build windows

package daemonctl

import "syscall"

// detachedSysProcAttr detaches the daemon from the CLI's console and job:
// DETACHED_PROCESS (0x00000008) gives it no console, CREATE_NEW_PROCESS_GROUP
// (0x00000200) removes it from our Ctrl-C group. Same pattern as the
// detached editor launch in internal/godot.
func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200}
}
