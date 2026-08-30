package daemon_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
	"github.com/mimajiushi/godot-ai-cli/internal/testutil/mockplugin"
)

// testVersion mirrors the vendored plugin version the daemon advertises.
const testVersion = "3.2.4"

// startDaemon boots a daemon on ephemeral loopback ports.
func startDaemon(t *testing.T) *daemon.Daemon {
	t.Helper()
	d, err := daemon.Start(context.Background(), daemon.Config{
		HTTPPort: 0,
		WSPort:   0,
		Version:  testVersion,
	})
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

// baseURL returns the daemon's HTTP base address.
func baseURL(d *daemon.Daemon) string {
	return fmt.Sprintf("http://127.0.0.1:%d", d.HTTPPort())
}

// getJSON performs a GET and decodes the JSON body.
func getJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, decodeBody(t, resp)
}

// postJSON performs a POST with a JSON body and decodes the JSON response.
func postJSON(t *testing.T, url string, body any) (int, map[string]any) {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, decodeBody(t, resp)
}

// decodeBody reads and JSON-decodes a response body.
func decodeBody(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode body %q: %v", raw, err)
	}
	return out
}

// errorBody extracts the nested error object from a protocol error body.
func errorBody(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	if body["status"] != "error" {
		t.Fatalf("status = %v, want error (body=%v)", body["status"], body)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing error object in %v", body)
	}
	return errObj
}

func TestStatusEndpoint(t *testing.T) {
	d := startDaemon(t)

	code, body := getJSON(t, baseURL(d)+"/godot-ai/status")
	if code != http.StatusOK {
		t.Fatalf("status code = %d", code)
	}
	if body["name"] != "godot-ai" {
		t.Errorf("name = %v", body["name"])
	}
	if body["server_version"] != testVersion || body["version"] != testVersion {
		t.Errorf("version fields = %v/%v", body["server_version"], body["version"])
	}
	if int(body["ws_port"].(float64)) != d.WSPort() {
		t.Errorf("ws_port = %v, want %d", body["ws_port"], d.WSPort())
	}
	if body["attach_protocol_version"].(float64) != 1 {
		t.Errorf("attach_protocol_version = %v", body["attach_protocol_version"])
	}
	if body["package_path"] != "godot-ai-cli" {
		t.Errorf("package_path = %v", body["package_path"])
	}
	if body["pid"].(float64) <= 0 {
		t.Errorf("pid = %v", body["pid"])
	}
}

func TestHealthEndpoint(t *testing.T) {
	d := startDaemon(t)

	code, body := getJSON(t, baseURL(d)+"/godot-ai/cli/health")
	if code != http.StatusOK {
		t.Fatalf("status code = %d", code)
	}
	if body["status"] != "ok" || body["version"] != testVersion {
		t.Errorf("body = %v", body)
	}
	if body["sessions"].(float64) != 0 {
		t.Errorf("sessions = %v, want 0", body["sessions"])
	}
}

func TestSessionsEmpty(t *testing.T) {
	d := startDaemon(t)

	code, body := getJSON(t, baseURL(d)+"/godot-ai/cli/sessions")
	if code != http.StatusOK {
		t.Fatalf("status code = %d", code)
	}
	sessions, ok := body["sessions"].([]any)
	if !ok {
		t.Fatalf("sessions field = %v", body["sessions"])
	}
	if len(sessions) != 0 {
		t.Errorf("sessions = %v, want empty", sessions)
	}
}

// postRaw performs a POST with an explicit Content-Type and decodes the
// JSON response.
func postRaw(t *testing.T, url, contentType, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build POST %s: %v", url, err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, decodeBody(t, resp)
}

// TestMutationEndpointsRejectNonJSONContentType pins the browser-CSRF
// guard: cross-origin simple requests (text/plain & friends) get a 415
// envelope on every POST endpoint, and the daemon survives untouched.
func TestMutationEndpointsRejectNonJSONContentType(t *testing.T) {
	d := startDaemon(t)
	for _, path := range []string{
		"/godot-ai/cli/execute",
		"/godot-ai/cli/activate",
		"/godot-ai/cli/shutdown",
	} {
		code, body := postRaw(t, baseURL(d)+path, "text/plain", "{}")
		if code != http.StatusUnsupportedMediaType {
			t.Errorf("POST %s text/plain: status = %d, want 415", path, code)
		}
		if errObj := errorBody(t, body); errObj["code"] != "UNSUPPORTED_CONTENT_TYPE" {
			t.Errorf("POST %s text/plain: code = %v, want UNSUPPORTED_CONTENT_TYPE", path, errObj["code"])
		}
	}

	// A charset suffix on application/json stays accepted.
	code, body := postRaw(t, baseURL(d)+"/godot-ai/cli/execute",
		"application/json; charset=utf-8", `{"command":"get_editor_state"}`)
	if code != http.StatusOK {
		t.Fatalf("execute with charset suffix: status = %d, want 200", code)
	}
	if errObj := errorBody(t, body); errObj["code"] != "PLUGIN_DISCONNECTED" {
		t.Errorf("code = %v, want PLUGIN_DISCONNECTED (no editor connected)", errObj["code"])
	}

	// The rejected shutdown must not have torn the daemon down.
	if code, _ := getJSON(t, baseURL(d)+"/godot-ai/cli/health"); code != http.StatusOK {
		t.Error("daemon died despite the rejected shutdown request")
	}
}

func TestExecuteNoEditor(t *testing.T) {
	d := startDaemon(t)

	code, body := postJSON(t, baseURL(d)+"/godot-ai/cli/execute", map[string]any{
		"command": "create_node",
		"params":  map[string]any{"name": "Player"},
		"write":   true,
	})
	if code != http.StatusOK {
		t.Fatalf("status code = %d (failures stay HTTP 200, the body carries the error)", code)
	}
	errObj := errorBody(t, body)
	if errObj["code"] != "PLUGIN_DISCONNECTED" {
		t.Errorf("code = %v, want PLUGIN_DISCONNECTED", errObj["code"])
	}
	data, _ := errObj["data"].(map[string]any)
	if data["reason"] != "no_active_session" || data["retryable"] != true {
		t.Errorf("error data = %v", data)
	}
}

func TestExecuteEndToEnd(t *testing.T) {
	d := startDaemon(t)
	p := mockplugin.Dial(t, fmt.Sprintf("127.0.0.1:%d", d.WSPort()), nil)
	p.SetResponder(func(command string, params map[string]any) *mockplugin.Response {
		return &mockplugin.Response{Data: map[string]any{
			"command": command,
			"echo":    params,
		}}
	})

	code, body := postJSON(t, baseURL(d)+"/godot-ai/cli/execute", map[string]any{
		"command": "create_node",
		"params":  map[string]any{"name": "Player"},
	})
	if code != http.StatusOK {
		t.Fatalf("status code = %d", code)
	}
	if body["status"] != "ok" {
		t.Fatalf("body = %v", body)
	}
	data, _ := body["data"].(map[string]any)
	if data["command"] != "create_node" {
		t.Errorf("data = %v", data)
	}
	if echo, _ := data["echo"].(map[string]any); echo["name"] != "Player" {
		t.Errorf("echo = %v", data["echo"])
	}

	// A write against a ready editor passes the gate without probing.
	code, body = postJSON(t, baseURL(d)+"/godot-ai/cli/execute", map[string]any{
		"command": "save_scene",
		"write":   true,
	})
	if code != http.StatusOK || body["status"] != "ok" {
		t.Fatalf("write execute failed: %d %v", code, body)
	}
	if got := p.Count("get_editor_state"); got != 0 {
		t.Errorf("ready write gate probed %d times, want 0", got)
	}
}

func TestExecuteWriteGateProbesWhenImporting(t *testing.T) {
	d := startDaemon(t)
	p := mockplugin.Dial(t, fmt.Sprintf("127.0.0.1:%d", d.WSPort()), map[string]any{
		"readiness": "importing",
	})
	p.SetResponder(func(command string, _ map[string]any) *mockplugin.Response {
		if command == "get_editor_state" {
			// The import has already finished: the probe heals the cache.
			return &mockplugin.Response{Data: map[string]any{"readiness": "ready"}}
		}
		return &mockplugin.Response{Data: map[string]any{"written": true}}
	})

	code, body := postJSON(t, baseURL(d)+"/godot-ai/cli/execute", map[string]any{
		"command": "create_node",
		"params":  map[string]any{"name": "Enemy"},
		"write":   true,
	})
	if code != http.StatusOK || body["status"] != "ok" {
		t.Fatalf("write through healed gate failed: %d %v", code, body)
	}
	if got := p.Count("get_editor_state"); got < 1 {
		t.Errorf("importing gate probed %d times, want >= 1", got)
	}
	if got := p.Count("create_node"); got != 1 {
		t.Errorf("create_node reached plugin %d times, want 1", got)
	}
}

func TestExecuteWriteGateRejectsPlaying(t *testing.T) {
	d := startDaemon(t)
	p := mockplugin.Dial(t, fmt.Sprintf("127.0.0.1:%d", d.WSPort()), map[string]any{
		"readiness": "playing",
	})
	p.SetResponder(func(string, map[string]any) *mockplugin.Response {
		return &mockplugin.Response{Data: map[string]any{"readiness": "playing"}}
	})

	code, body := postJSON(t, baseURL(d)+"/godot-ai/cli/execute", map[string]any{
		"command": "create_node",
		"write":   true,
	})
	if code != http.StatusOK {
		t.Fatalf("status code = %d", code)
	}
	errObj := errorBody(t, body)
	if errObj["code"] != "EDITOR_NOT_READY" {
		t.Errorf("code = %v, want EDITOR_NOT_READY", errObj["code"])
	}
	data, _ := errObj["data"].(map[string]any)
	if data["sub_code"] != "EDITOR_PLAYING" || data["retryable"] != false {
		t.Errorf("error data = %v", data)
	}
	// The gated write must never reach the plugin.
	if got := p.Count("create_node"); got != 0 {
		t.Errorf("create_node reached plugin %d times while playing", got)
	}
}

func TestActivateEndpoint(t *testing.T) {
	d := startDaemon(t)
	url := baseURL(d) + "/godot-ai/cli/activate"

	// Unknown session: 404 with the protocol error body.
	code, body := postJSON(t, url, map[string]any{"session_id": "ghost@beef"})
	if code != http.StatusNotFound {
		t.Fatalf("status code = %d, want 404", code)
	}
	if errObj := errorBody(t, body); errObj["code"] != "SESSION_NOT_FOUND" {
		t.Errorf("code = %v, want SESSION_NOT_FOUND", errObj["code"])
	}

	first := mockplugin.Dial(t, fmt.Sprintf("127.0.0.1:%d", d.WSPort()), nil)
	second := mockplugin.Dial(t, fmt.Sprintf("127.0.0.1:%d", d.WSPort()), nil)

	code, body = postJSON(t, url, map[string]any{"session_id": second.SessionID})
	if code != http.StatusOK || body["status"] != "ok" || body["active_session"] != second.SessionID {
		t.Fatalf("activate = %d %v", code, body)
	}

	_, sessionsBody := getJSON(t, baseURL(d)+"/godot-ai/cli/sessions")
	sessions := sessionsBody["sessions"].([]any)
	if len(sessions) != 2 {
		t.Fatalf("sessions = %v", sessionsBody)
	}
	for _, entry := range sessions {
		s := entry.(map[string]any)
		wantActive := s["session_id"] == second.SessionID
		if s["active"] != wantActive {
			t.Errorf("session %v active = %v, want %v", s["session_id"], s["active"], wantActive)
		}
		if s["readiness"] != "ready" || s["godot_version"] != "4.7.0" || s["editor_pid"].(float64) != 4321 {
			t.Errorf("session fields = %v", s)
		}
	}
	_ = first
}
