package bridge_test

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/mimajiushi/godot-ai-cli/internal/bridge"
	"github.com/mimajiushi/godot-ai-cli/internal/testutil/mockplugin"
)

// testVersion is the server version every test bridge reports; it must
// equal the vendored plugin.cfg version for real deployments.
const testVersion = "3.2.4"

// startServer boots a bridge on an ephemeral loopback port.
func startServer(t *testing.T) *bridge.Server {
	t.Helper()
	s := bridge.NewServer(testVersion)
	if err := s.Start(0); err != nil {
		t.Fatalf("bridge start: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Shutdown(context.Background())
	})
	return s
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// dialRaw opens a bare WebSocket client connection without handshaking.
func dialRaw(t *testing.T, addr string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+addr, nil)
	if err != nil {
		t.Fatalf("raw dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

// dialWithOrigin opens a bare WebSocket client connection carrying the
// given Origin header ("" sends none, like the native plugin).
func dialWithOrigin(ctx context.Context, addr, origin string) (*websocket.Conn, *http.Response, error) {
	opts := &websocket.DialOptions{}
	if origin != "" {
		opts.HTTPHeader = http.Header{"Origin": []string{origin}}
	}
	return websocket.Dial(ctx, "ws://"+addr, opts)
}

// TestUpgradeOriginPolicy pins the CSRF hardening on the WS handshake: no
// Origin header (the plugin) and loopback Origins upgrade fine; any other
// Origin is rejected with 403 before the upgrade.
func TestUpgradeOriginPolicy(t *testing.T) {
	s := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// No Origin header — the native plugin's shape.
	conn, _, err := dialWithOrigin(ctx, s.Addr(), "")
	if err != nil {
		t.Fatalf("dial without Origin: %v", err)
	}
	_ = conn.CloseNow()

	// Loopback Origins (any port, both host spellings) stay accepted.
	for _, origin := range []string{"http://127.0.0.1:3000", "http://localhost:8080", "http://[::1]:9000"} {
		conn, _, err := dialWithOrigin(ctx, s.Addr(), origin)
		if err != nil {
			t.Fatalf("dial with loopback Origin %q: %v", origin, err)
		}
		_ = conn.CloseNow()
	}

	// A cross-site Origin is rejected with 403 before any upgrade.
	_, resp, err := dialWithOrigin(ctx, s.Addr(), "https://evil.example")
	if err == nil {
		t.Fatal("dial with non-loopback Origin succeeded, want 403")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("rejection status = %v, want 403", resp)
	}
}

func TestHandshakeAcceptAndAckVersion(t *testing.T) {
	s := startServer(t)
	p := mockplugin.Dial(t, s.Addr(), nil)

	if got := p.Ack["type"]; got != "handshake_ack" {
		t.Fatalf("ack type = %v, want handshake_ack", got)
	}
	if got := p.Ack["server_version"]; got != testVersion {
		t.Fatalf("ack server_version = %v, want %s", got, testVersion)
	}

	sessions := s.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	sess := sessions[0]
	if sess.ID != p.SessionID {
		t.Errorf("session id = %q, want %q", sess.ID, p.SessionID)
	}
	if sess.GodotVersion != "4.7.0" || sess.EditorPID != 4321 {
		t.Errorf("session metadata mismatch: %+v", sess)
	}
	if sess.Readiness() != "ready" {
		t.Errorf("readiness = %q, want ready", sess.Readiness())
	}
	if s.ActiveSession() != sess {
		t.Errorf("first session should become active")
	}
}

func TestFirstFrameDeadline(t *testing.T) {
	s := bridge.NewServer(testVersion)
	s.HandshakeTimeout = 150 * time.Millisecond
	if err := s.Start(0); err != nil {
		t.Fatalf("bridge start: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })

	t.Run("silence", func(t *testing.T) {
		conn := dialRaw(t, s.Addr())
		// Send nothing: the server must drop us at the deadline.
		start := time.Now()
		_, _, err := conn.Read(context.Background())
		if err == nil {
			t.Fatal("expected the server to close a silent connection")
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("deadline not enforced, closed after %s", elapsed)
		}
	})

	t.Run("garbage", func(t *testing.T) {
		conn := dialRaw(t, s.Addr())
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := conn.Write(ctx, websocket.MessageText, []byte("not json at all")); err != nil {
			t.Fatalf("write garbage: %v", err)
		}
		_, _, err := conn.Read(context.Background())
		if err == nil {
			t.Fatal("expected the server to close after a garbage first frame")
		}
	})
}

func TestDuplicateSessionClosed4001(t *testing.T) {
	s := startServer(t)
	first := mockplugin.Dial(t, s.Addr(), nil)

	conn := dialRaw(t, s.Addr())
	hs, _ := json.Marshal(map[string]any{
		"type":           "handshake",
		"session_id":     first.SessionID,
		"godot_version":  "4.7.0",
		"project_path":   "C:/projects/other",
		"plugin_version": testVersion,
		"readiness":      "ready",
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, hs); err != nil {
		t.Fatalf("write duplicate handshake: %v", err)
	}
	_, _, err := conn.Read(context.Background())
	if err == nil {
		t.Fatal("expected duplicate session to be closed")
	}
	if code := websocket.CloseStatus(err); code != 4001 {
		t.Fatalf("close code = %d, want 4001 (err=%v)", code, err)
	}
}

func TestSendCommandOK(t *testing.T) {
	s := startServer(t)
	p := mockplugin.Dial(t, s.Addr(), nil)
	p.SetResponder(func(command string, params map[string]any) *mockplugin.Response {
		return &mockplugin.Response{Data: map[string]any{
			"command": command,
			"echo":    params,
		}}
	})

	params := map[string]any{"name": "Player", "type": "Node2D"}
	data, cmdErr := s.SendCommand(context.Background(), "", "create_node", params, 2*time.Second)
	if cmdErr != nil {
		t.Fatalf("SendCommand: %v", cmdErr)
	}
	if data["command"] != "create_node" {
		t.Errorf("command = %v, want create_node", data["command"])
	}
	echo, ok := data["echo"].(map[string]any)
	if !ok || echo["name"] != "Player" || echo["type"] != "Node2D" {
		t.Errorf("params not echoed: %v", data["echo"])
	}
	if got := p.Count("create_node"); got != 1 {
		t.Errorf("plugin saw %d create_node frames, want 1", got)
	}
}

func TestSendCommandErrorStatus(t *testing.T) {
	s := startServer(t)
	p := mockplugin.Dial(t, s.Addr(), nil)
	p.SetResponder(func(string, map[string]any) *mockplugin.Response {
		return &mockplugin.Response{
			Status: "error",
			Error: map[string]any{
				"code":    "NODE_NOT_FOUND",
				"message": "node /root/Missing does not exist",
				"data":    map[string]any{"path": "/root/Missing"},
			},
		}
	})

	_, cmdErr := s.SendCommand(context.Background(), "", "delete_node", nil, 2*time.Second)
	if cmdErr == nil {
		t.Fatal("expected CommandError")
	}
	if cmdErr.Code != "NODE_NOT_FOUND" {
		t.Errorf("code = %q", cmdErr.Code)
	}
	if cmdErr.Message != "node /root/Missing does not exist" {
		t.Errorf("message = %q", cmdErr.Message)
	}
	if cmdErr.Data["path"] != "/root/Missing" {
		t.Errorf("data = %v", cmdErr.Data)
	}
}

func TestSendCommandTimeout(t *testing.T) {
	s := startServer(t)
	mockplugin.Dial(t, s.Addr(), nil) // no responder: replies never come

	_, cmdErr := s.SendCommand(context.Background(), "", "create_node", nil, 200*time.Millisecond)
	if cmdErr == nil {
		t.Fatal("expected timeout error")
	}
	if cmdErr.Code != "TRANSPORT_TIMEOUT" {
		t.Errorf("code = %q, want TRANSPORT_TIMEOUT", cmdErr.Code)
	}
	if cmdErr.Data["retryable"] != true {
		t.Errorf("retryable = %v, want true", cmdErr.Data["retryable"])
	}
}

func TestNonFiniteParamsRejected(t *testing.T) {
	s := startServer(t)
	p := mockplugin.Dial(t, s.Addr(), nil)
	p.SetResponder(func(string, map[string]any) *mockplugin.Response {
		return &mockplugin.Response{Data: map[string]any{}}
	})

	cases := map[string]map[string]any{
		"nan at top level":   {"value": math.NaN()},
		"inf nested in list": {"pos": []any{1.0, math.Inf(1)}},
		"nan nested in map":  {"transform": map[string]any{"x": math.NaN()}},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			_, cmdErr := s.SendCommand(context.Background(), "", "set_property", params, time.Second)
			if cmdErr == nil {
				t.Fatal("expected INVALID_PARAMS")
			}
			if cmdErr.Code != "INVALID_PARAMS" {
				t.Errorf("code = %q, want INVALID_PARAMS", cmdErr.Code)
			}
		})
	}
	if got := len(p.Received()); got != 0 {
		t.Errorf("non-finite params must be rejected before send; plugin saw %d frames", got)
	}
}

func TestDeferredResponseResolves(t *testing.T) {
	s := startServer(t)
	p := mockplugin.Dial(t, s.Addr(), nil)
	p.SetResponder(func(string, map[string]any) *mockplugin.Response {
		return &mockplugin.Response{
			Delay: 400 * time.Millisecond, // models a deferred plugin reply
			Data:  map[string]any{"done": true},
		}
	})

	data, cmdErr := s.SendCommand(context.Background(), "", "save_scene", nil, 2*time.Second)
	if cmdErr != nil {
		t.Fatalf("SendCommand: %v", cmdErr)
	}
	if data["done"] != true {
		t.Errorf("data = %v", data)
	}
}

func TestOutOfOrderResponses(t *testing.T) {
	s := startServer(t)
	p := mockplugin.Dial(t, s.Addr(), nil)
	p.SetResponder(func(command string, _ map[string]any) *mockplugin.Response {
		delay := time.Duration(0)
		if command == "slow" {
			delay = 300 * time.Millisecond
		}
		return &mockplugin.Response{Delay: delay, Data: map[string]any{"command": command}}
	})

	// Fire both concurrently; the "fast" reply lands first.
	var wg sync.WaitGroup
	results := make(map[string]map[string]any, 2)
	var mu sync.Mutex
	for _, cmd := range []string{"slow", "fast"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, cmdErr := s.SendCommand(context.Background(), "", cmd, nil, 3*time.Second)
			if cmdErr != nil {
				t.Errorf("%s: %v", cmd, cmdErr)
				return
			}
			mu.Lock()
			results[cmd] = data
			mu.Unlock()
		}()
	}
	wg.Wait()

	if results["slow"]["command"] != "slow" || results["fast"]["command"] != "fast" {
		t.Errorf("responses misrouted: %v", results)
	}
}

func TestEventUpdatesCachedReadiness(t *testing.T) {
	s := startServer(t)
	p := mockplugin.Dial(t, s.Addr(), nil)

	p.PushEvent("readiness_changed", map[string]any{"readiness": "playing"})
	waitFor(t, "readiness to become playing", func() bool {
		return s.ActiveSession().Readiness() == "playing"
	})

	p.PushEvent("readiness_changed", map[string]any{"readiness": "ready"})
	waitFor(t, "readiness to return to ready", func() bool {
		return s.ActiveSession().Readiness() == "ready"
	})
}

func TestEventListenerDispatched(t *testing.T) {
	s := startServer(t)
	type gotEvent struct {
		sessionID, event string
		data             map[string]any
	}
	var mu sync.Mutex
	var events []gotEvent
	s.OnEvent(func(sessionID, event string, data map[string]any) {
		mu.Lock()
		events = append(events, gotEvent{sessionID, event, data})
		mu.Unlock()
	})

	p := mockplugin.Dial(t, s.Addr(), nil)
	p.PushEvent("scene_changed", map[string]any{"current_scene": "res://main.tscn"})

	waitFor(t, "scene_changed event dispatch", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(events) == 1
	})
	mu.Lock()
	defer mu.Unlock()
	if events[0].sessionID != p.SessionID || events[0].event != "scene_changed" {
		t.Errorf("event = %+v", events[0])
	}
	if events[0].data["current_scene"] != "res://main.tscn" {
		t.Errorf("event data = %v", events[0].data)
	}
}

func TestResponseReadinessStampHealsSession(t *testing.T) {
	s := startServer(t)
	p := mockplugin.Dial(t, s.Addr(), nil)
	// A dropped readiness_changed event leaves the cache at "ready"; the
	// response envelope stamp must heal it.
	p.SetReadinessStamp("importing")
	p.SetResponder(func(string, map[string]any) *mockplugin.Response {
		return &mockplugin.Response{Data: map[string]any{}}
	})

	if _, cmdErr := s.SendCommand(context.Background(), "", "get_editor_state", nil, 2*time.Second); cmdErr != nil {
		t.Fatalf("SendCommand: %v", cmdErr)
	}
	if got := s.ActiveSession().Readiness(); got != "importing" {
		t.Errorf("readiness = %q, want importing (healed from response stamp)", got)
	}

	// Unknown stamps are ignored (forward compatibility), not propagated.
	p.SetReadinessStamp("compiling_scripts")
	if _, cmdErr := s.SendCommand(context.Background(), "", "get_editor_state", nil, 2*time.Second); cmdErr != nil {
		t.Fatalf("SendCommand: %v", cmdErr)
	}
	if got := s.ActiveSession().Readiness(); got != "importing" {
		t.Errorf("readiness = %q, want importing unchanged by unknown stamp", got)
	}
}

func TestUnknownRequestIDDropped(t *testing.T) {
	s := startServer(t)
	p := mockplugin.Dial(t, s.Addr(), nil)

	// A response nobody asked for must be dropped, not crash the session.
	frame, _ := json.Marshal(map[string]any{
		"request_id": "00000000000000000000000000000000",
		"status":     "ok",
		"data":       map[string]any{},
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.Conn.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatalf("write forged response: %v", err)
	}

	// The session must still service real commands afterwards.
	p.SetResponder(func(string, map[string]any) *mockplugin.Response {
		return &mockplugin.Response{Data: map[string]any{"alive": true}}
	})
	data, cmdErr := s.SendCommand(context.Background(), "", "ping", nil, 2*time.Second)
	if cmdErr != nil || data["alive"] != true {
		t.Errorf("session broken after forged response: data=%v err=%v", data, cmdErr)
	}
}

func TestNoSessionPluginDisconnected(t *testing.T) {
	s := startServer(t)

	_, cmdErr := s.SendCommand(context.Background(), "", "create_node", nil, time.Second)
	if cmdErr == nil {
		t.Fatal("expected PLUGIN_DISCONNECTED")
	}
	if cmdErr.Code != "PLUGIN_DISCONNECTED" {
		t.Errorf("code = %q, want PLUGIN_DISCONNECTED", cmdErr.Code)
	}
	if cmdErr.Data["reason"] != "no_active_session" || cmdErr.Data["retryable"] != true {
		t.Errorf("data = %v", cmdErr.Data)
	}
}

func TestUnknownSession(t *testing.T) {
	s := startServer(t)
	mockplugin.Dial(t, s.Addr(), nil)

	_, cmdErr := s.SendCommand(context.Background(), "ghost@beef", "create_node", nil, time.Second)
	if cmdErr == nil {
		t.Fatal("expected SESSION_NOT_FOUND")
	}
	if cmdErr.Code != "SESSION_NOT_FOUND" {
		t.Errorf("code = %q, want SESSION_NOT_FOUND", cmdErr.Code)
	}
}

func TestActivateSwitchesActive(t *testing.T) {
	s := startServer(t)
	first := mockplugin.Dial(t, s.Addr(), nil)
	second := mockplugin.Dial(t, s.Addr(), nil)

	if got := s.ActiveSession().ID; got != first.SessionID {
		t.Fatalf("active = %q, want first session %q", got, first.SessionID)
	}
	if !s.Activate(second.SessionID) {
		t.Fatal("Activate(second) = false, want true")
	}
	if got := s.ActiveSession().ID; got != second.SessionID {
		t.Fatalf("active = %q, want second session %q", got, second.SessionID)
	}
	if s.Activate("ghost@beef") {
		t.Fatal("Activate(unknown) = true, want false")
	}
	// Commands without a session id now route to the second editor.
	second.SetResponder(func(string, map[string]any) *mockplugin.Response {
		return &mockplugin.Response{Data: map[string]any{"which": "second"}}
	})
	data, cmdErr := s.SendCommand(context.Background(), "", "who", nil, 2*time.Second)
	if cmdErr != nil || data["which"] != "second" {
		t.Errorf("routing after activate: data=%v err=%v", data, cmdErr)
	}
}

func TestDisconnectRemovesSession(t *testing.T) {
	s := startServer(t)
	p := mockplugin.Dial(t, s.Addr(), nil)
	p.Close()

	waitFor(t, "session removal after disconnect", func() bool {
		return len(s.Sessions()) == 0
	})
	if s.ActiveSession() != nil {
		t.Error("active session should be cleared after disconnect")
	}

	_, cmdErr := s.SendCommand(context.Background(), "", "create_node", nil, time.Second)
	if cmdErr == nil || cmdErr.Code != "PLUGIN_DISCONNECTED" {
		t.Errorf("post-disconnect SendCommand err = %v, want PLUGIN_DISCONNECTED", cmdErr)
	}
}

func TestDisconnectFailsInFlightCommand(t *testing.T) {
	s := startServer(t)
	p := mockplugin.Dial(t, s.Addr(), nil) // no responder: command stays in flight

	errCh := make(chan *bridge.CommandError, 1)
	go func() {
		_, cmdErr := s.SendCommand(context.Background(), "", "create_node", nil, 30*time.Second)
		errCh <- cmdErr
	}()

	waitFor(t, "command to reach the plugin", func() bool {
		return p.Count("create_node") == 1
	})
	p.Close()

	select {
	case cmdErr := <-errCh:
		if cmdErr == nil || cmdErr.Code != "PLUGIN_DISCONNECTED" {
			t.Errorf("err = %v, want PLUGIN_DISCONNECTED", cmdErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight command was not failed on disconnect")
	}
}
