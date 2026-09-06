//go:build !windows

package godot

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// processRunning reports whether pid refers to a live process.
func processRunning(pid int) bool {
	// Signal 0 is the portable liveness probe; EPERM still means alive.
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// processImageName resolves pid to its executable image path. Linux exposes
// it through /proc/<pid>/exe; elsewhere (macOS) ps reports the command
// name. It fails when the pid names no live process.
func processImageName(pid int) (string, error) {
	if target, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); err == nil {
		return target, nil
	}
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return "", fmt.Errorf("no live process with pid %d", pid)
	}
	return name, nil
}
