//go:build !windows

package godot

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// enumeratePlatformCmdlines：Linux 读 /proc/<pid>/cmdline（NUL 分隔），
// macOS 没有 /proc，用 ps -axo pid=,command=。两者都 best-effort：
// 单条进程读失败/解析失败跳过，整体失败才返回 error。
func enumeratePlatformCmdlines() ([]processCmdline, error) {
	if runtime.GOOS == "darwin" {
		return enumerateDarwin()
	}
	return enumerateLinux()
}

// enumerateLinux 遍历 /proc 里数字 pid 目录的 cmdline。
func enumerateLinux() ([]processCmdline, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read /proc: %w", err)
	}
	var procs []processCmdline
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		line := strings.TrimRight(strings.ReplaceAll(string(raw), "\x00", " "), " ")
		procs = append(procs, processCmdline{pid: pid, line: line})
	}
	return procs, nil
}

// enumerateDarwin 用 ps 拿全量命令行（macOS 无 /proc）。
func enumerateDarwin() ([]processCmdline, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps enumeration: %w", err)
	}
	var procs []processCmdline
	for _, line := range bytes.Split(out, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		pidStr, cmd, found := bytes.Cut(line, []byte(" "))
		if !found {
			continue
		}
		pid, err := strconv.Atoi(string(pidStr))
		if err != nil {
			continue
		}
		procs = append(procs, processCmdline{pid: pid, line: string(bytes.TrimSpace(cmd))})
	}
	return procs, nil
}

// WindowsSystemProxy 在非 Windows 平台没有注册表可读：update --proxy auto
// 退化为只认环境变量（update 包经此函数探测）。
func WindowsSystemProxy() string { return "" }
