//go:build windows

package godot

import (
	"syscall"
	"unsafe"
)

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

var queryFullProcessImageName = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryFullProcessImageNameW")

// processImageName resolves pid to its executable image path. It fails when
// the pid names no live process or the image cannot be queried.
func processImageName(pid int) (string, error) {
	const processQueryLimitedInformation = 0x1000
	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(handle)
	// The NT long-path ceiling; the API truncates to what fits.
	buf := make([]uint16, 32768)
	size := uint32(len(buf))
	r1, _, callErr := queryFullProcessImageName.Call(
		uintptr(handle), 0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)))
	if r1 == 0 {
		return "", callErr
	}
	return syscall.UTF16ToString(buf[:size]), nil
}
