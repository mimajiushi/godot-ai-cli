// 发现「已经打开但未连接 daemon 的 Godot 编辑器」——
// godot-ai-cli 长期以来只能看到「连上 daemon 的编辑器」：用户手动打开的编辑器
// 对 CLI 完全不可见，launch 在这种状态下会再开一个实例（场景锁/保存互覆
// 风险，需求 editor-attach-and-daemon-discovery）。本包用进程命令行做
// best-effort 判定（无新依赖）：
//
//   - 编辑器进程：命令行含 --editor，且可执行名像 Godot；
//   - 游戏进程：命令行含 --remote-debug 或 --editor-pid——它的 --path 必为
//     绝对路径，还倒过来给「--path ./」形式启动的编辑器补上工程归属；
//   - --path 为相对路径时工程无法解析（WMI/ ps 都不给进程 CWD），Project 留空，
//     由调用方按「未知工程但确实活着的编辑器」呈现。
//
// 扫描全程 best-effort：任何失败（powershell 不在、/proc 不可读、超时）都返回
// 空列表 + error，调用方降级为「看不到未连接编辑器」，绝不阻断主命令。
package godot

import (
	"path/filepath"
	"strconv"
	"strings"
)

// EditorProcess 是一次扫描发现的疑似 Godot 编辑器进程。
type EditorProcess struct {
	PID     int    // 进程 pid
	Project string // 解析出的工程绝对路径；解析失败为空串（见包注释）
	// Args 是该进程完整命令行（截断到 4KB），供诊断与后续解析
	Args string
	// GameRunning 非 nil 表示同时发现了它启动的游戏进程。
	GameRunning *EditorGameProcess
}

// EditorGameProcess 是编辑器拉起的游戏进程（--remote-debug/--editor-pid 判定）。
type EditorGameProcess struct {
	PID    int
	Scene  string // --scene res:// 参数，可能为空
	Editor int    // --editor-pid 指向的编辑器 pid
}

// processCmdline 是平台枚举器交回的一条进程（pid + 完整命令行）。
type processCmdline struct {
	pid  int
	line string
}

// scanPlatform 是平台枚举 seam：生产为 powershell（Windows）或
// /proc|ps（unix）；测试注入固定样本，保持解析层可单测。
var scanPlatform = enumeratePlatformCmdlines

// scanEditorsImpl 是 ScanEditors 的实现 seam：生产为平台枚举 + 纯解析；
// 测试经 SetEditorScannerForTest 注入固定样本，让 CLI 层的
// EDITOR_OPEN_UNCONNECTED 等分支可以脱离真实进程表单测。
var scanEditorsImpl = func() ([]EditorProcess, error) {
	procs, err := scanPlatform()
	if err != nil {
		return nil, err
	}
	return parseEditorProcesses(procs), nil
}

// ScanEditors 扫描本机 Godot 编辑器进程。返回列表可为空（没有发现或
// 平台枚举失败），error 仅诊断用，调用方不得因它失败主流程。
func ScanEditors() ([]EditorProcess, error) {
	return scanEditorsImpl()
}

// SetEditorScannerForTest 替换编辑器扫描实现，返回还原函数（defer 调用）。
// 仅供测试使用；生产路径不要碰。
func SetEditorScannerForTest(fn func() ([]EditorProcess, error)) (restore func()) {
	prev := scanEditorsImpl
	scanEditorsImpl = fn
	return func() { scanEditorsImpl = prev }
}

// parseEditorProcesses 是纯解析层：把 (pid, 命令行) 样本翻译成编辑器/游戏
// 进程结构并做归属回填。样本的命令行首个 token 是可执行路径。
func parseEditorProcesses(procs []processCmdline) []EditorProcess {
	var editors []EditorProcess
	var games []EditorGameProcess
	gameProjects := map[int]string{} // editorPID → 游戏进程的绝对工程路径
	for _, p := range procs {
		exe, args := splitCmdline(p.line)
		if !looksLikeGodotImage(exe) {
			continue
		}
		if hasFlag(args, "--remote-debug") || hasFlag(args, "--editor-pid") {
			g := EditorGameProcess{PID: p.pid, Editor: flagInt(args, "--editor-pid")}
			g.Scene = flagValue(args, "--scene")
			games = append(games, g)
			// 游戏进程的 --path 必为绝对路径（引擎展开后写入命令行），
			// 顺手记下给「--path ./」启动的编辑器回填工程。
			if g.Editor > 0 {
				if project := flagValue(args, "--path"); project != "" && filepath.IsAbs(project) {
					gameProjects[g.Editor] = normalizeProjectPath(project)
				}
			}
			continue
		}
		if hasFlag(args, "--editor") {
			ed := EditorProcess{PID: p.pid, Args: truncate(p.line, 4096)}
			if project := flagValue(args, "--path"); project != "" && filepath.IsAbs(project) {
				ed.Project = normalizeProjectPath(project)
			}
			editors = append(editors, ed)
		}
	}
	// 归属回填：游戏进程给对应编辑器挂上 GameRunning；编辑器自身解析不出
	// 工程（--path ./ 形态）时用游戏的绝对路径补上。同一编辑器的多个游戏
	// 只记第一个。
	editorByPID := map[int]int{}
	for i, ed := range editors {
		editorByPID[ed.PID] = i
	}
	for _, g := range games {
		idx, ok := editorByPID[g.Editor]
		if !ok {
			continue
		}
		if editors[idx].GameRunning == nil {
			editors[idx].GameRunning = &g
		}
		if editors[idx].Project == "" {
			editors[idx].Project = gameProjects[g.Editor]
		}
	}
	return editors
}

// splitCmdline 把命令行拆成 (可执行路径, 参数切片)。按空白切，支持给整条
// 命令行带引号 exe 的最常见形态——Godot 路径含空格时 WMI 的 CommandLine
// 会带引号，这里把首个引号对完整剥出。
func splitCmdline(line string) (string, []string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", nil
	}
	rest := line
	exe := ""
	if strings.HasPrefix(line, `"`) {
		if i := strings.Index(line[1:], `"`); i >= 0 {
			exe = line[1 : 1+i]
			rest = strings.TrimSpace(line[2+i:])
		}
	}
	if exe == "" {
		if i := strings.IndexAny(line, " \t"); i >= 0 {
			exe, rest = line[:i], strings.TrimSpace(line[i:])
		} else {
			exe, rest = line, ""
		}
	}
	if rest == "" {
		return exe, nil
	}
	return exe, strings.Fields(rest)
}

// looksLikeGodotImage 按可执行文件名判定 Godot：官方发布名
// （Godot_v4.7.2-stable_mono_win64.exe、Godot_console.exe、
// godot.linuxbsd.editor.x86_64…）都含 "godot"。大小写不敏感。
func looksLikeGodotImage(exe string) bool {
	base := strings.ToLower(filepath.Base(exe))
	return strings.Contains(base, "godot")
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name || strings.HasPrefix(a, name+"=") {
			return true
		}
	}
	return false
}

// flagValue 取 --flag value 或 --flag=value 形态的参数值。WMI/ps 给的
// 命令行里，含空格（或被 Start-Process 无条件包裹）的参数值会带引号——
// 剥掉首尾成对引号再返回，否则 filepath.IsAbs 会被引号顶成 false
// （实测现场：WMI 给出 `--path "D:/…/demo"`，未剥离时工程解析为空）。
func flagValue(args []string, name string) string {
	raw := ""
	for i, a := range args {
		if a == name && i+1 < len(args) {
			raw = args[i+1]
			break
		}
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			raw = v
			break
		}
	}
	if len(raw) >= 2 {
		first, last := raw[0], raw[len(raw)-1]
		if first == last && (first == '"' || first == '\'') {
			return raw[1 : len(raw)-1]
		}
	}
	return raw
}

// flagInt 取整型 flag 值，解析失败为 0。
func flagInt(args []string, name string) int {
	n, _ := strconv.Atoi(flagValue(args, name))
	return n
}

// cutProxyHost 从注册表 ProxyServer 值里取 HTTP 代理地址：支持 "host:port"
// 与 "http=host:port;https=host:port;socks=..." 两种形态。放在共享文件
// （而非 windows 专属）是为了跨平台可编译、可单测——它是纯字符串解析，
// 唯一平台相关的是读取注册表本身（WindowsSystemProxy）。
func cutProxyHost(raw string) (string, bool) {
	for _, seg := range strings.Split(raw, ";") {
		k, v, found := strings.Cut(seg, "=")
		if !found {
			// 纯 host:port 形态。
			return seg, seg != ""
		}
		if strings.EqualFold(k, "http") && v != "" {
			return v, true
		}
	}
	return "", false
}

// normalizeProjectPath 归一工程路径：正斜杠、去尾斜杠（与 CLI 侧
// sameProjectPath 的归一规则一致，跨包保持同一比较口径）。
func normalizeProjectPath(p string) string {
	return strings.TrimRight(strings.ReplaceAll(p, "\\", "/"), "/")
}

func truncate(s string, n int) string {
	// 按字节截断足够（用途是诊断展示，不追求符文边界）。
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
