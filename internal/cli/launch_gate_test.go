package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
	"github.com/mimajiushi/godot-ai-cli/internal/daemonctl"
	"github.com/mimajiushi/godot-ai-cli/internal/testutil/mockplugin"
)

// The launch gate after the per-project port pinning (beta.16): launch no
// longer mutates the global EditorSettings, so the old
// settingsMutationNeeded / SETTINGS_OVERRIDE_ACTIVE gate is gone. What
// remains, and what this file pins:
//   - the double-open guard (findProjectOnOtherDaemons) — a same-project
//     session on ANOTHER daemon blocks the spawn with EDITOR_ALREADY_OPEN;
//   - the compatible-daemon hint behind DAEMON_MISMATCH
//     (findCompatibleDaemon);
//   - the --upgrade-daemon teardown (shutdownDaemonKeepEditors).
// Two concurrent launches grabbing different ports are serialized by the
// global launch lock; the second one then hits the double-open guard.

// writeDaemonRecord drops a daemon-<port>.json identity file into the
// stubbed cache dir, as a running daemon would.
func writeDaemonRecord(t *testing.T, dir string, httpPort, wsPort int, version string) {
	t.Helper()
	base := filepath.Join(dir, "godot-ai-cli")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := fmt.Sprintf(`{"pid":1234,"http_port":%d,"ws_port":%d,"version":%q,"started_at":"2026-09-07T00:00:00Z"}`,
		httpPort, wsPort, version)
	if err := os.WriteFile(filepath.Join(base, fmt.Sprintf("daemon-%d.json", httpPort)), []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
}

// startRecordedDaemon starts a real daemon and writes its identity file
// into the stubbed cache dir (the daemon itself writes to the REAL user
// cache dir — tests re-record it here to stay hermetic).
func startRecordedDaemon(t *testing.T, dir, version string) *daemon.Daemon {
	t.Helper()
	d, err := daemon.Start(context.Background(), daemon.Config{HTTPPort: 0, WSPort: 0, Version: version})
	if err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })
	writeDaemonRecord(t, dir, d.HTTPPort(), d.WSPort(), version)
	return d
}

// TestDoubleOpenGuardHit: a session for the SAME project on another daemon
// produces a hit naming editor pid, daemon port, version, and session id.
func TestDoubleOpenGuardHit(t *testing.T) {
	dir := stubCacheDir(t)
	d := startRecordedDaemon(t, dir, "3.2.9")

	mockplugin.Dial(t, fmt.Sprintf("127.0.0.1:%d", d.WSPort()), map[string]any{
		"session_id": "mine@0001", "project_path": "/my/project/", "editor_pid": 4321,
	})

	hit, warnings := findProjectOnOtherDaemons(1, "/my/project") // exceptPort 1: not ours
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if hit == nil {
		t.Fatal("same-project session on another daemon not detected")
	}
	if hit["editor_pid"] != float64(4321) {
		t.Errorf("editor_pid = %v", hit["editor_pid"])
	}
	if hit["daemon_http_port"] != d.HTTPPort() || hit["session_id"] != "mine@0001" {
		t.Errorf("hit = %v", hit)
	}
	if hit["daemon_version"] != "3.2.9" {
		t.Errorf("daemon_version = %v, want the live probed version", hit["daemon_version"])
	}
}

// TestDoubleOpenGuardMiss: sessions for OTHER projects never hit; an empty
// daemon never hits.
func TestDoubleOpenGuardMiss(t *testing.T) {
	dir := stubCacheDir(t)
	d := startRecordedDaemon(t, dir, "3.2.9")
	mockplugin.Dial(t, fmt.Sprintf("127.0.0.1:%d", d.WSPort()), map[string]any{
		"session_id": "other@0001", "project_path": "/other/project/", "editor_pid": 55,
	})

	if hit, warnings := findProjectOnOtherDaemons(1, "/my/project"); hit != nil || len(warnings) != 0 {
		t.Errorf("hit = %v, warnings = %v, want no hit no warning", hit, warnings)
	}
}

// TestDoubleOpenGuardSkipsTargetDaemon: the daemon this launch targets is
// excluded — its same-project session is the REUSE path, never a block.
func TestDoubleOpenGuardSkipsTargetDaemon(t *testing.T) {
	dir := stubCacheDir(t)
	d := startRecordedDaemon(t, dir, "3.2.9")
	mockplugin.Dial(t, fmt.Sprintf("127.0.0.1:%d", d.WSPort()), map[string]any{
		"session_id": "mine@0001", "project_path": "/my/project/",
	})

	if hit, _ := findProjectOnOtherDaemons(d.HTTPPort(), "/my/project"); hit != nil {
		t.Errorf("the launch's own daemon must be skipped, got hit = %v", hit)
	}
}

// TestDoubleOpenGuardDeadDaemonSkipped: a stale record whose daemon is gone
// is skipped silently — no hit, no warning noise.
func TestDoubleOpenGuardDeadDaemonSkipped(t *testing.T) {
	dir := stubCacheDir(t)
	writeDaemonRecord(t, dir, 1, 1, "3.2.9") // port 1: nothing listens

	hit, warnings := findProjectOnOtherDaemons(9999, "/my/project")
	if hit != nil {
		t.Errorf("dead daemon produced a hit: %v", hit)
	}
	if len(warnings) != 0 {
		t.Errorf("dead daemon should be silent, got warnings = %v", warnings)
	}
}

// TestDoubleOpenGuardMalformedWarns: a daemon that answers but serves a
// broken sessions payload could be hiding a real editor — warn and
// continue instead of blocking the launch.
func TestDoubleOpenGuardMalformedWarns(t *testing.T) {
	dir := stubCacheDir(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/godot-ai/cli/sessions" {
			_, _ = w.Write([]byte(`{"sessions":"garbage"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	port := testServerPort(t, server)
	writeDaemonRecord(t, dir, port, 1, "3.2.9")

	hit, warnings := findProjectOnOtherDaemons(9999, "/my/project")
	if hit != nil {
		t.Errorf("malformed daemon produced a hit: %v", hit)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "malformed") {
		t.Errorf("warnings = %v, want one malformed-payload warning", warnings)
	}
}

// TestFindCompatibleDaemon: a healthy daemon whose version is
// major.minor-compatible with the request is found and named with its live
// ports; an incompatible version or a dead daemon is skipped.
func TestFindCompatibleDaemon(t *testing.T) {
	dir := stubCacheDir(t)
	d := startRecordedDaemon(t, dir, "3.2.8")
	writeDaemonRecord(t, dir, 1, 1, "3.2.8") // dead record: must be skipped

	alt, ok := findCompatibleDaemon(9999, "3.2.9")
	if !ok {
		t.Fatal("compatible daemon (3.2.8 running vs 3.2.9 requested) not found")
	}
	if alt["http_port"] != d.HTTPPort() || alt["ws_port"] != d.WSPort() || alt["version"] != "3.2.8" {
		t.Errorf("alt = %v", alt)
	}

	// The requested port itself is excluded (that is the mismatched one).
	if _, ok := findCompatibleDaemon(d.HTTPPort(), "3.2.9"); ok {
		t.Error("the mismatched port's own daemon must be excluded")
	}

	// A minor drift is incompatible: no hint.
	if _, ok := findCompatibleDaemon(9999, "3.3.0"); ok {
		t.Error("3.2.8 daemon must not be offered for a 3.3.0 request")
	}
}

// TestShutdownDaemonKeepEditors: the --upgrade-daemon teardown shuts the
// old daemon down, reports the connected editor count, and never touches
// the (mock) editor process — the plugin socket simply drops.
func TestShutdownDaemonKeepEditors(t *testing.T) {
	stubCacheDir(t)
	d, err := daemon.Start(context.Background(), daemon.Config{HTTPPort: 0, WSPort: 0, Version: "3.2.8"})
	if err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	// No t.Cleanup shutdown: the helper itself is the shutdown path.
	plug := mockplugin.Dial(t, fmt.Sprintf("127.0.0.1:%d", d.WSPort()), map[string]any{
		"session_id": "kept@0001", "project_path": "/my/project/",
	})

	editors, err := shutdownDaemonKeepEditors(d.HTTPPort())
	if err != nil {
		t.Fatalf("shutdownDaemonKeepEditors: %v", err)
	}
	if editors != 1 {
		t.Errorf("editors = %d, want 1", editors)
	}
	if daemonReachable(d.HTTPPort()) {
		t.Error("daemon still reachable after the upgrade shutdown")
	}
	select {
	case <-d.Done():
	case <-time.After(3 * time.Second):
		t.Error("daemon did not finish shutting down")
	}
	_ = plug // the mock editor was never asked to quit (no quit_editor path here)
}

// TestDaemonMismatchErrorNamesCompatibleDaemon: when a healthy
// major.minor-compatible daemon runs on ANOTHER port, the DAEMON_MISMATCH
// envelope points at it (data.same_version_daemon + a --http-port hint in
// the message); with none, the field stays absent.
func TestDaemonMismatchErrorNamesCompatibleDaemon(t *testing.T) {
	dir := stubCacheDir(t)
	d := startRecordedDaemon(t, dir, "3.2.8")

	mismatch := &daemonctl.DaemonMismatchError{
		HTTPPort: 18000, RunningWSPort: 9500, RequestedWSPort: 9500,
		RunningVersion: "3.1.0", RequestedVersion: "3.2.9",
	}

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	err := daemonMismatchError(cmd, mismatch, "3.2.9")
	if err == nil {
		t.Fatal("daemonMismatchError returned nil, want a reported error")
	}
	var out map[string]any
	if jsonErr := json.Unmarshal(buf.Bytes(), &out); jsonErr != nil {
		t.Fatalf("output is not JSON: %v\n%s", jsonErr, buf.String())
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "DAEMON_MISMATCH" {
		t.Errorf("code = %v", errObj["code"])
	}
	data := errObj["data"].(map[string]any)
	alt, ok := data["same_version_daemon"].(map[string]any)
	if !ok {
		t.Fatalf("same_version_daemon missing: %v", data)
	}
	if alt["http_port"] != float64(d.HTTPPort()) || alt["ws_port"] != float64(d.WSPort()) || alt["version"] != "3.2.8" {
		t.Errorf("same_version_daemon = %v", alt)
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "--http-port "+itoa(d.HTTPPort())) {
		t.Errorf("message missing the --http-port hint: %q", msg)
	}
	if !strings.Contains(msg, "--upgrade-daemon") {
		t.Errorf("message missing the --upgrade-daemon hint: %q", msg)
	}

	// No compatible daemon anywhere: the field stays absent.
	buf.Reset()
	mismatch.HTTPPort = d.HTTPPort() // exclude the only compatible daemon
	_ = daemonMismatchError(cmd, mismatch, "3.2.9")
	out = nil
	if jsonErr := json.Unmarshal(buf.Bytes(), &out); jsonErr != nil {
		t.Fatalf("output is not JSON: %v\n%s", jsonErr, buf.String())
	}
	data = out["error"].(map[string]any)["data"].(map[string]any)
	if _, present := data["same_version_daemon"]; present {
		t.Errorf("same_version_daemon present without a compatible daemon: %v", data)
	}
}
