//go:build !windows

package godot

import "syscall"

// detachedSysProcAttr puts the child in its own process group so it
// survives the CLI's exit and never receives our terminal's signals.
func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
