//go:build !windows

package godot

import (
	"os"
	"syscall"
)

// lockFile takes a blocking exclusive flock on the whole file: concurrent
// launch/stop runs queue instead of erroring out.
func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

// unlockFile releases the flock taken by lockFile.
func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
