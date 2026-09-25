package godot

import "testing"

// parseEditorProcesses 的样本取自 editor-attach-and-daemon-discovery 需求
// 文档第 0 节的实测进程列表（2026-09-19 现场），覆盖：手动打开的编辑器
// （--path ./ 相对路径）、它启动的游戏（--remote-debug + --editor-pid +
// 绝对 --path）、CLI spawn 的 headless 编辑器、无关进程。

func fieldSample() []processCmdline {
	return []processCmdline{
		{5020, `D:\software\Godot_v4.7.2-stable_mono_win64\Godot_v4.7.2-stable_mono_win64.exe --path ./ --editor`},
		{38004, `D:\software\Godot_v4.7.2-stable_mono_win64\Godot_v4.7.2-stable_mono_win64.exe --path D:/Godot_project/shoot-2d --remote-debug tcp://127.0.0.1:6007 --editor-pid 5020 --scene res://scene/test/pickup_test.tscn --wid 1969626 --position "4032,504" --resolution 1920x1080`},
		{11672, `C:\Users\chenwenjie\AppData\Local\Programs\godot-ai-cli\godot-ai-cli.exe serve --http-port 8030 --ws-port 9530`},
		{25548, `"C:\Program Files\Godot\Godot_v4.7.2-stable_mono_win64.exe" --path D:/Godot_project/godot-ai-skill-project/demo --editor --headless`},
		{9999, `C:\Windows\System32\notepad.exe --path D:/whatever --editor`},
	}
}

func TestParseEditorProcessesFindsEditorsAndGames(t *testing.T) {
	got := parseEditorProcesses(fieldSample())
	if len(got) != 2 {
		t.Fatalf("editors = %d, want 2 (user editor + cli headless editor): %+v", len(got), got)
	}
	// 样本顺序：5020 在前、25548 在后。
	user, headless := got[0], got[1]
	if user.PID != 5020 {
		t.Fatalf("first editor pid = %d, want 5020", user.PID)
	}
	// --path ./ 相对路径无法解析：Project 留空，靠游戏进程回填。
	if user.Project != "D:/Godot_project/shoot-2d" {
		t.Errorf("user editor project = %q, want game-backfilled shoot-2d", user.Project)
	}
	if user.GameRunning == nil {
		t.Fatal("user editor GameRunning = nil, want the pickup_test game")
	}
	if user.GameRunning.PID != 38004 || user.GameRunning.Scene != "res://scene/test/pickup_test.tscn" {
		t.Errorf("GameRunning = %+v", user.GameRunning)
	}
	if headless.PID != 25548 || headless.Project != "D:/Godot_project/godot-ai-skill-project/demo" {
		t.Errorf("headless editor = %+v", headless)
	}
	if headless.GameRunning != nil {
		t.Errorf("headless editor must not carry a game: %+v", headless.GameRunning)
	}
}

func TestParseEditorProcessesIgnoresForeignImages(t *testing.T) {
	// 非 Godot 可执行即使带 --editor 也不算（notepad 样本）；godot-ai-cli
	// 自己的 serve 进程不含 --editor，天然被排除。
	got := parseEditorProcesses(fieldSample())
	for _, ed := range got {
		if ed.PID == 9999 || ed.PID == 11672 {
			t.Errorf("foreign process misdetected as editor: %+v", ed)
		}
	}
}

func TestParseEditorProcessesAbsolutePathEditor(t *testing.T) {
	// 绝对 --path 直接解析；= 形态也认（IsAbs 按当前平台判定，样本用本机形态）。
	got := parseEditorProcesses([]processCmdline{
		{7, `godot --path=D:/home/dev/game --editor`},
	})
	if len(got) != 1 || got[0].Project != "D:/home/dev/game" {
		t.Errorf("absolute-path editor = %+v", got)
	}
}

func TestParseEditorProcessesQuotedArgsWMI(t *testing.T) {
	// WMI/Start-Process 会给参数值加引号（即使值内无空格）——引号必须剥掉，
	// 否则 filepath.IsAbs 失败、工程解析为空（RS-039 实测现场）。
	got := parseEditorProcesses([]processCmdline{
		{5208, `"D:\software\Godot_v4.7.2-stable_mono_win64\Godot_v4.7.2-stable_mono_win64.exe" --path "D:/Godot_project/godot-ai-skill-project/.replay-lanes/lane-2/demo" --editor --headless`},
	})
	if len(got) != 1 {
		t.Fatalf("editors = %+v", got)
	}
	if got[0].Project != "D:/Godot_project/godot-ai-skill-project/.replay-lanes/lane-2/demo" {
		t.Errorf("project = %q", got[0].Project)
	}
}

func TestParseEditorProcessesEmptyAndGarbage(t *testing.T) {
	if got := parseEditorProcesses(nil); len(got) != 0 {
		t.Errorf("nil sample → %+v", got)
	}
	if got := parseEditorProcesses([]processCmdline{{1, ""}, {2, "   "}, {3, "godot"}}); len(got) != 0 {
		t.Errorf("garbage sample → %+v", got)
	}
	// 游戏指向不存在的编辑器：静默丢弃回填，不 panic。
	got := parseEditorProcesses([]processCmdline{
		{42, `godot.exe --path D:/game --remote-debug tcp://127.0.0.1:6007 --editor-pid 7777`},
	})
	if len(got) != 0 {
		t.Errorf("orphan game must not surface as editor: %+v", got)
	}
}

func TestSplitCmdlineQuotedExe(t *testing.T) {
	exe, args := splitCmdline(`"C:\Program Files\Godot\Godot_v4.7.2.exe" --path D:/game --editor`)
	if exe != `C:\Program Files\Godot\Godot_v4.7.2.exe` {
		t.Errorf("exe = %q", exe)
	}
	if len(args) != 3 || args[0] != "--path" || args[1] != "D:/game" || args[2] != "--editor" {
		t.Errorf("args = %v", args)
	}
}

func TestWindowsSystemProxyParsing(t *testing.T) {
	// 注册表 ProxyServer 的两种形态：纯 host:port 与分协议段。
	if host, ok := cutProxyHost("127.0.0.1:7897"); !ok || host != "127.0.0.1:7897" {
		t.Errorf("plain host:port = %q, %v", host, ok)
	}
	if host, ok := cutProxyHost("http=127.0.0.1:7897;https=127.0.0.1:7897"); !ok || host != "127.0.0.1:7897" {
		t.Errorf("multi-scheme = %q, %v", host, ok)
	}
	if host, ok := cutProxyHost("socks=127.0.0.1:7890"); ok {
		t.Errorf("socks-only must not be picked as http proxy: %q", host)
	}
	if _, ok := cutProxyHost(""); ok {
		t.Error("empty value must be rejected")
	}
}
