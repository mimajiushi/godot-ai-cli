//go:build !windows

package daemonctl

import "syscall"

// detachedSysProcAttr puts the daemon in its own process group so it
// survives the CLI's exit and never receives our terminal's signals.
func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
