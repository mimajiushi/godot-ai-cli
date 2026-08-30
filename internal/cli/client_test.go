package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
