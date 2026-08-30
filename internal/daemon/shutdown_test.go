package daemon_test

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
)

// TestShutdownEndpoint: POST /godot-ai/cli/shutdown must answer first and
// then bring the daemon down (health probe fails afterwards), removing the
// PID file as part of the clean shutdown.
func TestShutdownEndpoint(t *testing.T) {
	d := startDaemon(t)
	base := baseURL(d)

	pidFile := daemon.PIDFilePath(d.HTTPPort())
	if _, err := os.Stat(pidFile); err != nil {
		t.Fatalf("pid file not written on startup: %v (%s)", err, pidFile)
	}

	code, body := postJSON(t, base+"/godot-ai/cli/shutdown", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("status code = %d", code)
	}
	if body["status"] != "shutting_down" {
		t.Fatalf("body = %v", body)
	}

	// The daemon must actually exit: the health probe fails afterwards.
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := http.Get(base + "/godot-ai/cli/health")
		if err != nil {
			break // connection refused: daemon is down
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon still answers health after shutdown endpoint")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Clean shutdown removes the PID file; the removal runs after the HTTP
	// listener closes (bridge shutdown sits between), so poll briefly
	// instead of racing it.
	deadline = time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(pidFile); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Error("pid file still present after shutdown")
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestPIDFileContent records the fields agents may use for diagnostics.
func TestPIDFileContent(t *testing.T) {
	d := startDaemon(t)
	raw, err := os.ReadFile(daemon.PIDFilePath(d.HTTPPort()))
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	text := string(raw)
	for _, field := range []string{
		fmt.Sprintf("\"pid\":%d", os.Getpid()),
		fmt.Sprintf("\"http_port\":%d", d.HTTPPort()),
		fmt.Sprintf("\"ws_port\":%d", d.WSPort()),
		"\"version\":\"" + testVersion + "\"",
		"\"started_at\":",
	} {
		if !contains(text, field) {
			t.Errorf("pid file %s missing %s", text, field)
		}
	}
}

// contains is a tiny strings.Contains stand-in to keep imports minimal.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
