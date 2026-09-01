package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
	"github.com/mimajiushi/godot-ai-cli/internal/testutil/mockplugin"
)

// isolateCacheDir points the last-daemon record at a throwaway dir so stop
// tests never touch the real user cache.
func isolateCacheDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	old := userCacheDir
	userCacheDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { userCacheDir = old })
}

// startTestDaemon brings up a real daemon on ephemeral ports.
func startTestDaemon(t *testing.T) *daemon.Daemon {
	t.Helper()
	d, err := daemon.Start(context.Background(), daemon.Config{HTTPPort: 0, WSPort: 0, Version: "test"})
	if err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = d.Shutdown(ctx)
	})
	return d
}

// dialOkPlugin connects a mock plugin that answers every command with ok.
func dialOkPlugin(t *testing.T, d *daemon.Daemon, sessionID, projectPath string) *mockplugin.Plugin {
	t.Helper()
	p := mockplugin.Dial(t, fmt.Sprintf("127.0.0.1:%d", d.WSPort()), map[string]any{
		"session_id":   sessionID,
		"project_path": projectPath,
	})
	p.SetResponder(func(string, map[string]any) *mockplugin.Response {
		return &mockplugin.Response{Data: map[string]any{}}
	})
	return p
}

func runStop(t *testing.T, args ...string) (map[string]any, error) {
	t.Helper()
	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"stop"}, args...))
	err := cmd.Execute()
	var out map[string]any
	if jerr := json.Unmarshal(buf.Bytes(), &out); jerr != nil {
		t.Fatalf("stop output is not JSON: %v\n%s", jerr, buf.String())
	}
	return out, err
}

// TestStopQuitsEverySession: with several projects sharing one daemon, a
// full stop must quit EVERY session's editor — an unpinned quit only
// reaches the active session and would orphan the rest (disconnected
// editors left running against a dead daemon).
func TestStopQuitsEverySession(t *testing.T) {
	isolateCacheDir(t)
	d := startTestDaemon(t)
	p1 := dialOkPlugin(t, d, "one@0001", "C:/p/one/")
	p2 := dialOkPlugin(t, d, "two@0002", "C:/p/two/")
	port := strconv.Itoa(d.HTTPPort())

	out, err := runStop(t, "--http-port", port)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if out["status"] != "stopped" {
		t.Errorf("out = %v, want stopped", out)
	}
	if n := p1.Count("quit_editor"); n != 1 {
		t.Errorf("session one received quit_editor x%d, want 1", n)
	}
	if n := p2.Count("quit_editor"); n != 1 {
		t.Errorf("session two received quit_editor x%d, want 1 — non-active session orphaned", n)
	}
	if daemonReachable(d.HTTPPort()) {
		t.Error("daemon still reachable after stop")
	}
}

// TestStopSessionFlagQuitsOnlyThatEditor: stop --session quits exactly one
// editor and leaves the daemon plus the other session running — the
// multi-project teardown path.
func TestStopSessionFlagQuitsOnlyThatEditor(t *testing.T) {
	isolateCacheDir(t)
	d := startTestDaemon(t)
	p1 := dialOkPlugin(t, d, "one@0001", "C:/p/one/")
	p2 := mockplugin.Dial(t, fmt.Sprintf("127.0.0.1:%d", d.WSPort()), map[string]any{
		"session_id":   "two@0002",
		"project_path": "C:/p/two/",
	})
	// Reply ok, then hang up shortly after so the session-gone poll sees
	// the disconnect (the editor dying is what makes the session leave).
	p2.SetResponder(func(cmd string, _ map[string]any) *mockplugin.Response {
		if cmd == "quit_editor" {
			go func() {
				time.Sleep(300 * time.Millisecond)
				p2.Close()
			}()
		}
		return &mockplugin.Response{Data: map[string]any{}}
	})
	port := strconv.Itoa(d.HTTPPort())

	out, err := runStop(t, "--http-port", port, "--session", "two@0002")
	if err != nil {
		t.Fatalf("stop --session: %v", err)
	}
	if out["status"] != "session_stopped" || out["session_id"] != "two@0002" {
		t.Errorf("out = %v, want session_stopped for two@0002", out)
	}
	if _, warned := out["warnings"]; warned {
		t.Errorf("unexpected warnings: %v", out["warnings"])
	}
	if n := p2.Count("quit_editor"); n != 1 {
		t.Errorf("session two received quit_editor x%d, want 1", n)
	}
	if n := p1.Count("quit_editor"); n != 0 {
		t.Errorf("session one received quit_editor x%d, want 0 — other sessions must survive", n)
	}
	if !daemonReachable(d.HTTPPort()) {
		t.Error("daemon went down on stop --session, want it running")
	}
}

// TestStopSessionNotFound: an unknown --session id is a SESSION_NOT_FOUND
// error (exit non-zero) naming the known sessions.
func TestStopSessionNotFound(t *testing.T) {
	isolateCacheDir(t)
	d := startTestDaemon(t)
	dialOkPlugin(t, d, "one@0001", "C:/p/one/")
	port := strconv.Itoa(d.HTTPPort())

	out, err := runStop(t, "--http-port", port, "--session", "ghost@beef")
	if err == nil {
		t.Fatal("stop --session with unknown id succeeded, want SESSION_NOT_FOUND")
	}
	errObj, ok := out["error"].(map[string]any)
	if !ok || errObj["code"] != "SESSION_NOT_FOUND" {
		t.Errorf("out = %v, want SESSION_NOT_FOUND envelope", out)
	}
	data, _ := errObj["data"].(map[string]any)
	known, _ := data["known_sessions"].([]any)
	if len(known) != 1 || known[0] != "one@0001" {
		t.Errorf("known_sessions = %v, want [one@0001]", data["known_sessions"])
	}
	if !daemonReachable(d.HTTPPort()) {
		t.Error("daemon went down on a failed stop --session, want it running")
	}
}

// TestStopSessionQuitRequested: when the editor ignores the quit (stays
// connected), stop --session must NOT claim "session_stopped" — it reports
// "quit_requested" with a warning and exit 0, and the daemon keeps running.
func TestStopSessionQuitRequested(t *testing.T) {
	isolateCacheDir(t)
	old := sessionGoneTimeout
	sessionGoneTimeout = 1200 * time.Millisecond
	t.Cleanup(func() { sessionGoneTimeout = old })

	d := startTestDaemon(t)
	p := dialOkPlugin(t, d, "stubborn@0001", "C:/p/stubborn/") // answers ok, never disconnects
	port := strconv.Itoa(d.HTTPPort())

	out, err := runStop(t, "--http-port", port, "--session", "stubborn@0001")
	if err != nil {
		t.Fatalf("stop --session: %v", err)
	}
	if out["status"] != "quit_requested" {
		t.Errorf("status = %v, want quit_requested (the session never left)", out["status"])
	}
	if _, warned := out["warnings"]; !warned {
		t.Errorf("warnings missing on quit_requested: %v", out)
	}
	if n := p.Count("quit_editor"); n != 1 {
		t.Errorf("quit_editor count = %d, want 1", n)
	}
	if !daemonReachable(d.HTTPPort()) {
		t.Error("daemon went down on stop --session, want it running")
	}
}

// sanity: stop --session with no daemon running stays parseable and
// reports not_running (the not-running branch wins over --session).
func TestStopSessionWithoutDaemon(t *testing.T) {
	isolateCacheDir(t)
	out, err := runStop(t, "--http-port", "1", "--session", "x@1")
	if err != nil {
		t.Fatalf("stop --session with no daemon: %v", err)
	}
	if out["status"] != "not_running" {
		t.Errorf("out = %v, want not_running", out)
	}
	if !strings.Contains(fmt.Sprint(out["ports_tried"]), "1") {
		t.Errorf("ports_tried = %v, want [1]", out["ports_tried"])
	}
}
