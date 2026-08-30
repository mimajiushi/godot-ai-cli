//go:build windows

package godot

import "syscall"

// detachedSysProcAttr detaches the child from the CLI's console and job:
// DETACHED_PROCESS (0x00000008) gives it no console of its own, and
// CREATE_NEW_PROCESS_GROUP (0x00000200) removes it from our Ctrl-C group.
func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200}
}
