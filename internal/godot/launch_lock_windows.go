//go:build windows

package godot

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

// lockfileExclusiveLock requests an exclusive (not shared) byte-range lock.
const lockfileExclusiveLock = 0x00000002

// lockFile takes a blocking exclusive lock on the file's first byte. No
// LOCKFILE_FAIL_IMMEDIATELY: concurrent launch/stop runs must queue, not
// error out.
func lockFile(f *os.File) error {
	var overlapped syscall.Overlapped
	r, _, err := procLockFileEx.Call(
		f.Fd(),
		lockfileExclusiveLock,
		0,    // reserved, must be zero
		1, 0, // lock one byte at offset 0
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if r == 0 {
		return err
	}
	return nil
}

// unlockFile releases the byte-range lock taken by lockFile.
func unlockFile(f *os.File) error {
	var overlapped syscall.Overlapped
	r, _, err := procUnlockFileEx.Call(
		f.Fd(),
		0,    // reserved, must be zero
		1, 0, // the same one byte at offset 0
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if r == 0 {
		return err
	}
	return nil
}
