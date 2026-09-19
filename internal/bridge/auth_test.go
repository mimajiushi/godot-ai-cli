package bridge_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/mimajiushi/godot-ai-cli/internal/bridge"
	"github.com/mimajiushi/godot-ai-cli/internal/testutil/mockplugin"
)

// TestAuthWrongCapability：持有错误 capability 的对端在 proof 校验处
// fail-closed（4003），且不留会话。
func TestAuthWrongCapability(t *testing.T) {
	s := startServer(t)
	wrong := strings.Repeat("ab", 32) // 合法形状、错误内容
	if wrong == s.WSCapability {
		t.Fatal("test collision: wrong capability equals the real one")
	}
	code, reason := mockplugin.DialRejected(t, s.Addr(), wrong, map[string]any{
		"session_id":              "wrong-cap@0001",
		"skip_server_proof_check": true, // 用错 capability 走到服务端校验
	})
	if code != 4003 {
		t.Fatalf("close code = %d, want 4003 (reason=%q)", code, reason)
	}
	if len(s.Sessions()) != 0 {
		t.Errorf("rejected auth left %d sessions behind", len(s.Sessions()))
	}
}

// TestAuthV3PeerRejected：v3 插件的首帧（type:"handshake"）被显式 4002
// 拒绝——混接的 v3/v4 对端 fail closed，不进入任何旧解析器。
func TestAuthV3PeerRejected(t *testing.T) {
	s := startServer(t)
	code, reason := mockplugin.DialRaw(t, s.Addr(), []byte(
		`{"type":"handshake","session_id":"v3@0001","godot_version":"4.7.0",`+
			`"project_path":"C:/projects/mock","plugin_version":"3.2.13",`+
			`"protocol_version":1,"readiness":"ready","editor_pid":1}`))
	if code != 4002 {
		t.Fatalf("close code = %d, want 4002 (reason=%q)", code, reason)
	}
	if !strings.Contains(reason, "protocol 2") {
		t.Errorf("reason = %q, want a protocol-2 hint", reason)
	}
}

// TestAuthGodotVersionGate：镜像上游 _supports_v4_editor——4.7+ 的 4.x
// 线之外的引擎版本在握手处拒绝（4002）。
func TestAuthGodotVersionGate(t *testing.T) {
	s := startServer(t)
	for _, v := range []string{"4.6.1-stable (official)", "4.5-stable (official)", "5.0-stable (official)", "garbage"} {
		code, reason := mockplugin.DialRejected(t, s.Addr(), s.WSCapability, map[string]any{
			"godot_version": v,
		})
		if code != 4002 {
			t.Errorf("godot %q: close code = %d, want 4002 (reason=%q)", v, code, reason)
		}
	}
	// 4.7 两种官方显示格式都接受（连字符与点号）。
	p := mockplugin.Dial(t, s.Addr(), s.WSCapability, map[string]any{"godot_version": "4.7-stable (official)"})
	p.Close()
	p2 := mockplugin.Dial(t, s.Addr(), s.WSCapability, map[string]any{"godot_version": "4.7.2.stable.official"})
	p2.Close()
}

// TestAuthLaunchedByDoubleTranscript：fork 的 launched_by 扩展进入
// proof 转录；不带该字段的纯上游形状按原样转录通过且 origin=user。
func TestAuthLaunchedByDoubleTranscript(t *testing.T) {
	s := startServer(t)
	cli := mockplugin.Dial(t, s.Addr(), s.WSCapability, map[string]any{"launched_by": "cli"})
	if got := s.Sessions()[0].Origin; got != "cli" {
		t.Errorf("origin = %q, want cli", got)
	}
	cli.Close()

	// 伪造：声称 cli 但按上游原样转录算 proof（缺 launchedBy 字段值），
	// 必须 4003——扩展字段不是可选项，带了就必须进转录。
	code, _ := mockplugin.DialRejected(t, s.Addr(), s.WSCapability, map[string]any{
		"launched_by":  "cli",
		"client_proof": strings.Repeat("00", 32),
	})
	if code != 4003 {
		t.Errorf("forged proof: close code = %d, want 4003", code)
	}
}

// TestAuthEmptyCapabilityFailsClosed：无 capability 的服务端（上游"锁定"
// 语义）拒绝一切编辑器连接，而不是退化为免认证。
func TestAuthEmptyCapabilityFailsClosed(t *testing.T) {
	s := bridge.NewServer(testVersion)
	s.WSCapability = "" // 显式锁定
	if err := s.Start(0); err != nil {
		t.Fatalf("bridge start: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })

	code, _ := mockplugin.DialRaw(t, s.Addr(), []byte(
		`{"type":"auth_hello","protocol_version":2,"client_nonce":"`+strings.Repeat("0a", 32)+`"}`))
	if code != 4003 {
		t.Errorf("close code = %d, want 4003", code)
	}
}

// TestAuthHandshakeTimeoutWindow：两帧共享 5s 窗口——发完 auth_hello 后
// 沉默到超时也应被关闭。
func TestAuthHandshakeTimeoutWindow(t *testing.T) {
	s := bridge.NewServer(testVersion)
	s.HandshakeTimeout = 200 * time.Millisecond
	if err := s.Start(0); err != nil {
		t.Fatalf("bridge start: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })

	conn := dialRaw(t, s.Addr())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(
		`{"type":"auth_hello","protocol_version":2,"client_nonce":"`+strings.Repeat("0a", 32)+`"}`)); err != nil {
		t.Fatalf("write auth_hello: %v", err)
	}
	// 读走 challenge 后沉默：deadline 到时连接应被关闭。
	_, _, _ = conn.Read(ctx)
	start := time.Now()
	_, _, err := conn.Read(context.Background())
	if err == nil {
		t.Fatal("expected the server to close a half-done handshake")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("handshake deadline not enforced, closed after %s", elapsed)
	}
}
