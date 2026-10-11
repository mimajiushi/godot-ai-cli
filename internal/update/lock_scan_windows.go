//go:build windows

// Windows 侧的「谁把这个文件当映像映射着」扫描（需求 R-7 b）：自更新失败时
// 只抛 Windows 的原文错误（"Access is denied."）没法让人知道该停掉哪个进程。
package update

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processQueryLimitedInformation 是 PROCESS_QUERY_LIMITED_INFORMATION (0x1000)：
// 只为读映像路径，它比 PROCESS_QUERY_INFORMATION 更容易在受保护进程上成功；
// x/sys v0.12.0 尚未导出这个常量（选 v0.12.0 是为了不给 go.mod 涨 go 指令——
// v0.41.0 起要求 go 1.24.0，会把 CI 的 go-version-file 从 1.24.x 钉成 1.24.0）。
const processQueryLimitedInformation = 0x1000

// processesLocking 遍历系统进程，返回把 path 当映像（running image）映射着的
// 那些进程（pid / exe / cmdline）——Windows 允许对运行中的映像改名、禁止删除，
// 需求 R-7 的原始故障正是「上一次升级留下的 <exe>.old 仍是某个活着的 daemon
// 的映像」。任何一步失败都只是少一条结果，绝不升级为错误：诊断失败不该盖住
// 原始故障。当前进程自身不算持有者（改名失败才是它的问题，而那不是「被谁占用」）。
func processesLocking(path string) []map[string]any {
	out := []map[string]any{}
	want := path
	if abs, err := filepath.Abs(path); err == nil {
		want = abs
	}
	// 先按文件身份（卷内文件 ID）比较，短名/长名、大小写、硬链接都能对上；
	// 目标不存在（例如刚被挪走）时退回路径比较。
	wantInfo, wantStatErr := os.Stat(want)
	name := strings.ToLower(filepath.Base(want))
	self := uint32(os.Getpid())

	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return out
	}
	defer windows.CloseHandle(snapshot)

	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err := windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		pid := entry.ProcessID
		if pid == 0 || pid == self {
			continue
		}
		// 快照自带的短名先筛一遍：只有名字相符的进程才值得开句柄查完整路径。
		if name != "" && strings.ToLower(windows.UTF16ToString(entry.ExeFile[:])) != name {
			continue
		}
		exe, ok := processImagePath(pid)
		if !ok || !sameImage(exe, want, wantInfo, wantStatErr) {
			continue
		}
		item := map[string]any{"pid": int(pid), "exe": exe}
		// 命令行尽力给：拿不到就省略字段，而不是编一个空串。
		if cmdline := processCommandLine(pid); cmdline != "" {
			item["cmdline"] = cmdline
		}
		out = append(out, item)
	}
	return out
}

// sameImage 判定一个进程的映像路径是否就是目标文件。
func sameImage(exe, want string, wantInfo os.FileInfo, wantStatErr error) bool {
	if wantStatErr == nil {
		if exeInfo, err := os.Stat(exe); err == nil && os.SameFile(wantInfo, exeInfo) {
			return true
		}
	}
	return strings.EqualFold(exe, want)
}

// processImagePath 用 QueryFullProcessImageName 取一个进程的完整映像路径。
// 受保护进程（系统 / 其它用户）打不开句柄是正常情形，返回 false 跳过即可。
func processImagePath(pid uint32) (string, bool) {
	h, err := windows.OpenProcess(processQueryLimitedInformation, false, pid)
	if err != nil {
		return "", false
	}
	defer windows.CloseHandle(h)
	// 4096 个 UTF-16 码元足以容纳长路径（MAX_PATH 只是常规上限）。
	buf := make([]uint16, 4096)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return "", false
	}
	return windows.UTF16ToString(buf[:size]), true
}

// processBasicInformation 是 NtQueryInformationProcess(ProcessBasicInformation)
// 的返回结构：只需 PebBaseAddress（x64 偏移 8 / x86 偏移 4），其余字段按指针宽度
// 占位以保证对齐一致。
type processBasicInformation struct {
	ExitStatus                   uintptr
	PebBaseAddress               uintptr
	AffinityMask                 uintptr
	BasePriority                 uintptr
	UniqueProcessID              uintptr
	InheritedFromUniqueProcessID uintptr
}

// unicodeString 是 UNICODE_STRING：Length/MaximumLength 各 2 字节，Buffer 在
// 指针宽度上对齐。
type unicodeString struct {
	Length        uint16
	MaximumLength uint16
	Buffer        uintptr
}

// processCommandLine 尽力读取一个进程的命令行：PEB → ProcessParameters →
// CommandLine（UNICODE_STRING）。命令行是「好知道、但不是必需」的字段，任何
// 一步失败都返回空串。
//
// 只处理「目标与本进程同为 64 位 / 同为 32 位」的情形：WOW64 目标（32 位进程跑
// 在 64 位宿主上）的 PEB 结构偏移不同，宁可没有 cmdline 也不返回乱码。
func processCommandLine(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	var isWow64 bool
	if err := windows.IsWow64Process(h, &isWow64); err == nil && isWow64 {
		return ""
	}
	var pbi processBasicInformation
	if err := windows.NtQueryInformationProcess(h, 0, unsafe.Pointer(&pbi),
		uint32(unsafe.Sizeof(pbi)), nil); err != nil || pbi.PebBaseAddress == 0 {
		return ""
	}
	// PEB.ProcessParameters / RTL_USER_PROCESS_PARAMETERS.CommandLine 的偏移
	// 随位宽固定（x64：0x20 / 0x70；x86：0x10 / 0x40）。
	ptr := unsafe.Sizeof(uintptr(0))
	pebParams := pbi.PebBaseAddress + 0x20
	cmdOffset := uintptr(0x70)
	if ptr == 4 {
		pebParams = pbi.PebBaseAddress + 0x10
		cmdOffset = 0x40
	}
	var paramsAddr uintptr
	if !readMemory(h, pebParams, unsafe.Pointer(&paramsAddr), ptr) || paramsAddr == 0 {
		return ""
	}
	var us unicodeString
	if !readMemory(h, paramsAddr+cmdOffset, unsafe.Pointer(&us), unsafe.Sizeof(us)) {
		return ""
	}
	// 长度必须是偶数个字节（Length 是字节数，且 UNICODE_STRING 上限 32K 字符
	// 由 uint16 自身兜住）；长度为 0 或指针为空说明偏移对不上，直接放弃。
	if us.Length == 0 || us.Buffer == 0 || us.Length%2 != 0 {
		return ""
	}
	buf := make([]uint16, us.Length/2)
	if !readMemory(h, us.Buffer, unsafe.Pointer(&buf[0]), uintptr(us.Length)) {
		return ""
	}
	return strings.TrimRight(windows.UTF16ToString(buf), "\x00")
}

// readMemory 从目标进程读 size 字节到 dst，读满才算成功。
func readMemory(h windows.Handle, addr uintptr, dst unsafe.Pointer, size uintptr) bool {
	var read uintptr
	if err := windows.ReadProcessMemory(h, addr, (*byte)(dst), size, &read); err != nil {
		return false
	}
	return read == size
}
