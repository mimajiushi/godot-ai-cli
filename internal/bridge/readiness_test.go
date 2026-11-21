package bridge_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mimajiushi/godot-ai-cli/internal/bridge"
	"github.com/mimajiushi/godot-ai-cli/internal/testutil/mockplugin"
)

// startGateServer boots a bridge with shrunk readiness-gate timings so the
// bounded importing hold finishes in milliseconds instead of seconds.
func startGateServer(t *testing.T) *bridge.Server {
	t.Helper()
	s := bridge.NewServer(testVersion)
	s.ImportingHoldCap = 300 * time.Millisecond
	s.ImportingProbeInterval = 20 * time.Millisecond
	s.ProbeTimeout = time.Second
	if err := s.Start(0); err != nil {
		t.Fatalf("bridge start: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	return s
}

func TestRequireWritableReadyFastPath(t *testing.T) {
	s := startGateServer(t)
	p := mockplugin.Dial(t, s.Addr(), s.WSCapability, nil)

	if cmdErr := s.RequireWritable(context.Background(), "", false); cmdErr != nil {
		t.Fatalf("RequireWritable: %v", cmdErr)
	}
	// Fast path: the cache says writable, so no probe may hit the wire.
	if got := p.Count("get_editor_state"); got != 0 {
		t.Errorf("ready fast path probed %d times, want 0", got)
	}
}

func TestRequireWritableNoSceneFastPath(t *testing.T) {
	s := startGateServer(t)
	p := mockplugin.Dial(t, s.Addr(), s.WSCapability, map[string]any{"readiness": "no_scene"})

	// Upstream lets no_scene through: individual handlers reject when no
	// scene is open.
	if cmdErr := s.RequireWritable(context.Background(), "", false); cmdErr != nil {
		t.Fatalf("RequireWritable: %v", cmdErr)
	}
	if got := p.Count("get_editor_state"); got != 0 {
		t.Errorf("no_scene fast path probed %d times, want 0", got)
	}
}

func TestRequireWritableNoSession(t *testing.T) {
	s := startGateServer(t)
	// No editor connected: the gate is a no-op, SendCommand fails on its own.
	if cmdErr := s.RequireWritable(context.Background(), "", false); cmdErr != nil {
		t.Fatalf("RequireWritable without session: %v", cmdErr)
	}
}

func TestRequireWritableImportingClearsDuringHold(t *testing.T) {
	s := startGateServer(t)
	p := mockplugin.Dial(t, s.Addr(), s.WSCapability, map[string]any{"readiness": "importing"})

	// The editor reports "importing" twice, then becomes ready.
	var probes atomic.Int32
	p.SetResponder(func(command string, _ map[string]any) *mockplugin.Response {
		readiness := "importing"
		if probes.Add(1) >= 3 {
			readiness = "ready"
		}
		return &mockplugin.Response{Data: map[string]any{"readiness": readiness}}
	})

	if cmdErr := s.RequireWritable(context.Background(), "", false); cmdErr != nil {
		t.Fatalf("RequireWritable: %v", cmdErr)
	}
	if got := probes.Load(); got < 3 {
		t.Errorf("probes = %d, want >= 3 (hold until ready)", got)
	}
	if got := s.ActiveSession().Readiness(); got != "ready" {
		t.Errorf("readiness = %q, want ready after healing probes", got)
	}
}

func TestRequireWritablePersistentImporting(t *testing.T) {
	s := startGateServer(t)
	p := mockplugin.Dial(t, s.Addr(), s.WSCapability, map[string]any{"readiness": "importing"})
	p.SetResponder(func(string, map[string]any) *mockplugin.Response {
		return &mockplugin.Response{Data: map[string]any{"readiness": "importing"}}
	})

	cmdErr := s.RequireWritable(context.Background(), "", false)
	if cmdErr == nil {
		t.Fatal("expected EDITOR_NOT_READY")
	}
	if cmdErr.Code != "EDITOR_NOT_READY" {
		t.Errorf("code = %q, want EDITOR_NOT_READY", cmdErr.Code)
	}
	if cmdErr.Data["sub_code"] != "EDITOR_IMPORTING" {
		t.Errorf("sub_code = %v, want EDITOR_IMPORTING", cmdErr.Data["sub_code"])
	}
	if cmdErr.Data["retryable"] != true {
		t.Errorf("retryable = %v, want true", cmdErr.Data["retryable"])
	}
	if cmdErr.Data["editor_state"] != "importing" {
		t.Errorf("editor_state = %v", cmdErr.Data["editor_state"])
	}
	hint, _ := cmdErr.Data["hint"].(string)
	if hint == "" {
		t.Error("hint missing — agents need the recovery instruction")
	}
	// The hold must have probed repeatedly, not just once.
	if got := p.Count("get_editor_state"); got < 2 {
		t.Errorf("probes = %d, want >= 2 during the bounded hold", got)
	}
}

func TestRequireWritablePlayingFailsFast(t *testing.T) {
	s := startGateServer(t)
	p := mockplugin.Dial(t, s.Addr(), s.WSCapability, map[string]any{"readiness": "playing"})
	p.SetResponder(func(string, map[string]any) *mockplugin.Response {
		return &mockplugin.Response{Data: map[string]any{
			"readiness":   "playing",
			"game_status": map[string]any{"status": "live", "run_token": 7},
		}}
	})

	start := time.Now()
	cmdErr := s.RequireWritable(context.Background(), "", false)
	if cmdErr == nil {
		t.Fatal("expected EDITOR_NOT_READY")
	}
	if cmdErr.Code != "EDITOR_NOT_READY" {
		t.Errorf("code = %q, want EDITOR_NOT_READY", cmdErr.Code)
	}
	if cmdErr.Data["sub_code"] != "EDITOR_PLAYING" {
		t.Errorf("sub_code = %v, want EDITOR_PLAYING", cmdErr.Data["sub_code"])
	}
	if cmdErr.Data["retryable"] != false {
		t.Errorf("retryable = %v, want false (waiting never clears playing)", cmdErr.Data["retryable"])
	}
	gameStatus, ok := cmdErr.Data["game_status"].(map[string]any)
	if !ok || gameStatus["status"] != "live" || gameStatus["run_token"] != float64(7) {
		t.Errorf("新鲜 game_status 丢失或错误：%v", cmdErr.Data["game_status"])
	}
	// Only importing holds: playing rejects after the single probe.
	if got := p.Count("get_editor_state"); got != 1 {
		t.Errorf("probes = %d, want exactly 1 (no hold for playing)", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("playing rejection took %s, expected fail-fast", elapsed)
	}
}

func TestRequireWritableStaleCacheHealedByProbe(t *testing.T) {
	s := startGateServer(t)
	p := mockplugin.Dial(t, s.Addr(), s.WSCapability, map[string]any{"readiness": "playing"})
	// The game already stopped but the readiness_changed event was lost;
	// the live probe must heal the cache and let the write through.
	p.SetResponder(func(string, map[string]any) *mockplugin.Response {
		return &mockplugin.Response{Data: map[string]any{"readiness": "ready"}}
	})

	if cmdErr := s.RequireWritable(context.Background(), "", false); cmdErr != nil {
		t.Fatalf("RequireWritable: %v (stale cache should have been healed)", cmdErr)
	}
	if got := s.ActiveSession().Readiness(); got != "ready" {
		t.Errorf("readiness = %q, want ready", got)
	}
}

// TestRequireWritableProbeFailureEnforcesCache mirrors upstream: a failed
// probe enforces against the cached value instead of escalating to a
// connection error.
func TestRequireWritableProbeFailureEnforcesCache(t *testing.T) {
	s := startGateServer(t)
	mockplugin.Dial(t, s.Addr(), s.WSCapability, map[string]any{"readiness": "playing"})
	// No responder: the probe times out.

	cmdErr := s.RequireWritable(context.Background(), "", false)
	if cmdErr == nil {
		t.Fatal("expected EDITOR_NOT_READY from cached playing state")
	}
	if cmdErr.Code != "EDITOR_NOT_READY" || cmdErr.Data["sub_code"] != "EDITOR_PLAYING" {
		t.Errorf("err = %v", cmdErr)
	}
}

// TestRequireWritableProjectRunPlayingNoop 只有实时 probe 明确返回 playing
// 才允许 project_run 抵达插件的 already-running no-op 分支；responder 按真实
// 插件契约拒绝 params:null，防止内部 probe 再次发送 nil 而不是空 Dictionary。
func TestRequireWritableProjectRunPlayingNoop(t *testing.T) {
	s := startGateServer(t)
	p := mockplugin.Dial(t, s.Addr(), s.WSCapability, map[string]any{"readiness": "playing"})
	p.SetResponder(func(_ string, params map[string]any) *mockplugin.Response {
		if params == nil {
			return &mockplugin.Response{Status: "error", Error: map[string]any{
				"code": "INVALID_PARAMS", "message": "params must be a Dictionary", "data": map[string]any{},
			}}
		}
		return &mockplugin.Response{Data: map[string]any{
			"readiness": "playing", "game_status": map[string]any{"status": "live"},
		}}
	})
	if cmdErr := s.RequireWritable(context.Background(), "", true); cmdErr != nil {
		t.Fatalf("实时确认 playing 的 project_run 被误拒：%v", cmdErr)
	}
	if got := p.Count("get_editor_state"); got != 1 {
		t.Errorf("probe 次数 = %d，want 1", got)
	}
	for _, received := range p.Received() {
		if received.Command == "get_editor_state" && (received.Params == nil || len(received.Params) != 0) {
			t.Errorf("probe params = %#v，want 非 nil 空 map", received.Params)
		}
	}
}

// TestRequireWritableProjectRunProbeFailure 失败 probe 不得凭缓存 playing
// 放行 project_run；没有新鲜状态时也不能伪造 game_status。
func TestRequireWritableProjectRunProbeFailure(t *testing.T) {
	s := startGateServer(t)
	mockplugin.Dial(t, s.Addr(), s.WSCapability, map[string]any{"readiness": "playing"})
	cmdErr := s.RequireWritable(context.Background(), "", true)
	if cmdErr == nil || cmdErr.Code != "EDITOR_NOT_READY" || cmdErr.Data["sub_code"] != "EDITOR_PLAYING" {
		t.Fatalf("失败 probe 的 project_run 未被拒绝：%v", cmdErr)
	}
	if _, exists := cmdErr.Data["game_status"]; exists {
		t.Errorf("失败 probe 不应携带 game_status：%v", cmdErr.Data["game_status"])
	}
}

// TestRequireWritableProjectRunImportingStillBlocked project_run 的例外不覆盖
// importing；即使打开 allowPlayingNoop 也必须在有界等待后拒绝持续导入。
func TestRequireWritableProjectRunImportingStillBlocked(t *testing.T) {
	s := startGateServer(t)
	p := mockplugin.Dial(t, s.Addr(), s.WSCapability, map[string]any{"readiness": "importing"})
	p.SetResponder(func(string, map[string]any) *mockplugin.Response {
		return &mockplugin.Response{Data: map[string]any{"readiness": "importing"}}
	})
	cmdErr := s.RequireWritable(context.Background(), "", true)
	if cmdErr == nil || cmdErr.Code != "EDITOR_NOT_READY" || cmdErr.Data["sub_code"] != "EDITOR_IMPORTING" {
		t.Fatalf("持续 importing 的 project_run 未被拒绝：%v", cmdErr)
	}
}
