package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Regression: apiClient must not carry a client-level Timeout, otherwise any
// daemon call slower than that cap (a full `test run` takes minutes) dies
// with "awaiting headers" no matter how large the per-request timeout is.
func TestPostDaemonJSONHonorsLongPerRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(6 * time.Second) // beyond the old 5s client cap
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}))
	defer server.Close()

	body, err := postDaemonJSON(testServerPort(t, server), "/slow", map[string]any{}, 8*time.Second)
	if err != nil {
		t.Fatalf("slow request failed despite an 8s per-request timeout: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("unexpected body: %v", body)
	}
}

// TestDecodeBodyCapsGarbagePreview: a non-JSON response body is echoed in
// the error, but capped at 512 bytes with a truncation marker so a huge
// garbage body never floods the CLI's error output.
func TestDecodeBodyCapsGarbagePreview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 5000)))
	}))
	defer server.Close()

	_, err := getDaemonJSON(testServerPort(t, server), "/godot-ai/cli/health")
	if err == nil {
		t.Fatal("expected a decode error for a garbage body")
	}
	msg := err.Error()
	if !strings.Contains(msg, "…(truncated)") {
		t.Errorf("error missing the truncation marker: %.120s...", msg)
	}
	if strings.Contains(msg, strings.Repeat("x", 513)) {
		t.Errorf("error echoes more than 512 bytes of the body (len %d)", len(msg))
	}
}

// The flip side: a request whose own timeout expires must still fail fast.
func TestPostDaemonJSONShortTimeoutStillApplies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}))
	defer server.Close()

	if _, err := postDaemonJSON(testServerPort(t, server), "/slow", map[string]any{}, 200*time.Millisecond); err == nil {
		t.Fatal("expected the 200ms per-request timeout to fire")
	}
}
