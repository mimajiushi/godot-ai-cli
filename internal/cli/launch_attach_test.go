package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
	"github.com/mimajiushi/godot-ai-cli/internal/godot"
	"github.com/mimajiushi/godot-ai-cli/internal/testutil/mockplugin"
)

// launch --attach / EDITOR_OPEN_UNCONNECTED 的测试（需求
// editor-attach-and-daemon-discovery §3.2/§3.3）。全部走 runLaunch 的
// foreground 模式（in-process daemon，不 spawn serve 进程），并以 seam
// 替换 Godot 解析/版本探测/编辑器启动，保证测试在 CI（无 Godot）与
// 开发机（有用户的编辑器）上都不碰真实环境。

// stubLaunchSideEffects 注入「Godot 可解析为 4.7.2」的 seam，并让编辑器
// 「启动成功」返回假 pid（流程可以继续走到等待握手）；返回的 spawned
// 记录 launchEditorFn 是否被调用（--attach / 守卫拒绝时必须为 false）。
func stubLaunchSideEffects(t *testing.T) (spawned *bool) {
	t.Helper()
	prevFind, prevVer, prevLaunch := resolveGodotBinFn, probeGodotVerFn, launchEditorFn
	resolveGodotBinFn = func(explicit string) (string, error) { return "godot-test-stub", nil }
	probeGodotVerFn = func(path string) (godot.Version, error) {
		return godot.ParseVersion("4.7.2.stable.official")
	}
	called := false
	launchEditorFn = func(opts godot.LaunchOptions) (int, error) {
		called = true
		return 43210, nil // 假 pid：流程继续，等待握手因无 session 而超时
	}
	t.Cleanup(func() {
		resolveGodotBinFn, probeGodotVerFn, launchEditorFn = prevFind, prevVer, prevLaunch
	})
	return &called
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// lockedBuffer 是并发安全的输出缓冲：runLaunch 在后台 goroutine 里写，
// 测试 goroutine 轮询长度/读内容——裸 bytes.Buffer 会被 -race 判为数据
// 竞态（Windows CI 实测）。
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func (b *lockedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// runLaunchAttachFlow 以 foreground daemon 跑 runLaunch：ready/error 打印
// 后 foreground 分支会阻塞等 ctx 取消——helper 在输出落盘后主动 cancel，
// 让调用方拿到解析后的 envelope 时进程内 daemon 也已关闭。
func runLaunchAttachFlow(t *testing.T, dir string, mutate func(*launchOptions)) (map[string]any, error) {
	t.Helper()
	opts := launchOptions{
		project:    dir,
		httpPort:   freeTCPPort(t),
		wsPort:     freeTCPPort(t),
		wait:       300 * time.Millisecond,
		foreground: true,
		attach:     true,
	}
	if mutate != nil {
		mutate(&opts)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := newLaunchCommand()
	buf := &lockedBuffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetContext(ctx)

	var runErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		runErr = runLaunch(cmd, opts)
	}()

	// 等输出落盘（ready 或 error envelope 只打印一次）。
	deadline := time.Now().Add(10 * time.Second)
	for buf.Len() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel() // 让 foreground 分支退出：输出已打印，阻塞只是等中断。
	wg.Wait()

	if buf.Len() == 0 {
		t.Fatal("launch printed nothing")
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("launch printed non-JSON: %v\n%s", err, buf.String())
	}
	return out, runErr
}

// TestLaunchAttachDoesNotSpawnAndTimesOut：--attach 在没有任何编辑器重连
// 时显式失败 EDITOR_NOT_CONNECTED（绝不静默成功）、绝不 spawn、端口钉照写。
func TestLaunchAttachDoesNotSpawnAndTimesOut(t *testing.T) {
	dir := newLaunchTestProject(t)
	spawned := stubLaunchSideEffects(t)

	out, err := runLaunchAttachFlow(t, dir, nil)
	if err == nil {
		t.Fatalf("attach with no connecting editor must fail: %v", out)
	}
	env := errorEnvelope(t, out)
	if env["code"] != "EDITOR_NOT_CONNECTED" {
		t.Fatalf("error code = %v", env["code"])
	}
	data, _ := env["data"].(map[string]any)
	if data["retryable"] != true {
		t.Errorf("retryable = %v", data["retryable"])
	}
	if data["wait_s"].(float64) <= 0 {
		t.Errorf("wait_s = %v", data["wait_s"])
	}
	if data["http_port"].(float64) <= 0 {
		t.Errorf("http_port = %v", data["http_port"])
	}
	if *spawned {
		t.Error("attach must never spawn an editor")
	}
	// 端口钉必须已写入（插件靠 项目文件 > EditorSettings > 默认 收养本 daemon）。
	ports, ok := godot.ReadProjectPorts(dir)
	if !ok {
		t.Fatal("attach must pin the daemon ports for the project")
	}
	if ports.HTTPPort == 0 || ports.WSPort == 0 {
		t.Errorf("pinned ports = %+v", ports)
	}
}

// TestLaunchAttachReusesExistingSession：目标 daemon 上已有本工程的
// session 时 --attach 直接 ready、spawned_editor=false、不 spawn。
func TestLaunchAttachReusesExistingSession(t *testing.T) {
	dir := newLaunchTestProject(t)
	spawned := stubLaunchSideEffects(t)

	d, err := daemon.Start(context.Background(), daemon.Config{
		HTTPPort: freeTCPPort(t), WSPort: freeTCPPort(t), Version: "4.2.4",
	})
	if err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })
	mockplugin.Dial(t, fmt.Sprintf("127.0.0.1:%d", d.WSPort()), d.Bridge().WSCapability, map[string]any{
		"session_id": "attach@0001", "project_path": dir, "plugin_version": "4.2.4", "editor_pid": 4321,
	})

	out, err := runLaunchAttachFlow(t, dir, func(o *launchOptions) {
		// daemon 已在该端口活着：detached 模式的 EnsureRunning 直接收养
		// 它（同 major.minor + 同 ws port），绝不能在原地再起一个。
		o.foreground = false
		o.httpPort = d.HTTPPort()
		o.wsPort = d.WSPort()
		o.wait = 5 * time.Second
	})
	if err != nil {
		t.Fatalf("attach onto a live session must succeed: %v\n%v", err, out)
	}
	if out["status"] != "ready" {
		t.Fatalf("status = %v", out["status"])
	}
	if out["spawned_editor"] != false {
		t.Errorf("spawned_editor = %v, want false (session reused)", out["spawned_editor"])
	}
	if *spawned {
		t.Error("attaching to an existing session must not spawn")
	}
}

// TestLaunchOpenUnconnectedGuard：非 attach 时进程扫描发现「工程编辑器已开
// 但未连接」必须阻止 spawn（EDITOR_OPEN_UNCONNECTED，suggest 指向
// --attach）；--force-spawn 越过守卫继续走（假编辑器连不上 → LAUNCH_TIMEOUT）。
func TestLaunchOpenUnconnectedGuard(t *testing.T) {
	dir := newLaunchTestProject(t)
	spawned := stubLaunchSideEffects(t)
	absDir := func() string {
		abs, err := filepath.Abs(dir)
		if err != nil {
			t.Fatal(err)
		}
		return abs
	}()

	restore := godot.SetEditorScannerForTest(func() ([]godot.EditorProcess, error) {
		return []godot.EditorProcess{
			{PID: 5020, Project: absDir, GameRunning: &godot.EditorGameProcess{PID: 38004, Scene: "res://x.tscn", Editor: 5020}},
			{PID: 25548, Project: "D:/other/demo"}, // 别的工程：不得误报
		}, nil
	})
	defer restore()

	out, err := runLaunchAttachFlow(t, dir, func(o *launchOptions) { o.attach = false })
	if err == nil {
		t.Fatalf("open-unconnected editor must block the spawn: %v", out)
	}
	env := errorEnvelope(t, out)
	if env["code"] != "EDITOR_OPEN_UNCONNECTED" {
		t.Fatalf("error code = %v", env["code"])
	}
	data, _ := env["data"].(map[string]any)
	editors, _ := data["editors"].([]any)
	if len(editors) != 1 {
		t.Fatalf("editors = %v", data["editors"])
	}
	suggest, _ := data["suggest"].([]any)
	if len(suggest) == 0 || !strings.Contains(suggest[0].(string), "--attach") {
		t.Errorf("suggest = %v", data["suggest"])
	}
	if *spawned {
		t.Error("guard must fire BEFORE any editor spawn")
	}

	// --force-spawn 越过守卫：spawn 被调用；假编辑器永不连接 → LAUNCH_TIMEOUT。
	out, err = runLaunchAttachFlow(t, dir, func(o *launchOptions) { o.attach = false; o.forceSpawn = true })
	if err == nil {
		t.Fatalf("force-spawned fake editor never connects: %v", out)
	}
	env = errorEnvelope(t, out)
	if env["code"] != "LAUNCH_TIMEOUT" {
		t.Fatalf("error code after force-spawn = %v", env["code"])
	}
	if !*spawned {
		t.Error("force-spawn must actually attempt the spawn")
	}
}
