package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
)

// syncBuffer is a goroutine-safe output buffer for tests that run a
// command in a background goroutine while polling its output — a plain
// bytes.Buffer races in that pattern (flagged by -race in CI).
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// stubCacheDir points the last-daemon record at a throwaway dir and
// restores the real resolver afterwards. Tests using it must NOT run in
// parallel (package-global swap, same pattern as daemonctl's spawnServe).
func stubCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := userCacheDir
	userCacheDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { userCacheDir = old })
	return dir
}

// portProbeCmd builds a bare command carrying only the --http-port flag,
// like the daemon-facing leaves see it after cobra's flag merge.
func portProbeCmd(t *testing.T, setFlag string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().Int("http-port", daemon.DefaultHTTPPort, "")
	if setFlag != "" {
		if err := cmd.Flags().Set("http-port", setFlag); err != nil {
			t.Fatal(err)
		}
	}
	return cmd
}

// TestWriteAndReadLastDaemon pins the writer launch/serve share: the file
// lands at <cache>/godot-ai-cli/last-daemon.json with the documented keys,
// started_at auto-filled, and readLastDaemon parses it back.
func TestWriteAndReadLastDaemon(t *testing.T) {
	dir := stubCacheDir(t)

	rec := lastDaemonRecord{HTTPPort: 18201, WSPort: 19501, Project: "C:/games/rpg"}
	if err := writeLastDaemon(rec); err != nil {
		t.Fatalf("writeLastDaemon: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "godot-ai-cli", "last-daemon.json"))
	if err != nil {
		t.Fatalf("record file not at the documented path: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("record is not JSON: %v", err)
	}
	if raw["http_port"] != 18201.0 || raw["ws_port"] != 19501.0 {
		t.Errorf("ports = %v", raw)
	}
	if raw["project"] != "C:/games/rpg" {
		t.Errorf("project = %v", raw["project"])
	}
	if started, _ := raw["started_at"].(string); started == "" {
		t.Error("started_at missing (writer must auto-fill it)")
	}

	got, ok := readLastDaemon()
	if !ok || got.HTTPPort != 18201 || got.WSPort != 19501 || got.Project != "C:/games/rpg" {
		t.Errorf("readLastDaemon = %+v, %v", got, ok)
	}

	// A second write overwrites the stale record — no error, new content.
	if err := writeLastDaemon(lastDaemonRecord{HTTPPort: 8000, WSPort: 9500}); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if got, ok := readLastDaemon(); !ok || got.HTTPPort != 8000 {
		t.Errorf("after overwrite readLastDaemon = %+v, %v", got, ok)
	}
}

// TestReadLastDaemonToleratesMissingAndCorrupt: a missing, unparseable, or
// port-less record is a silent !ok, never an error — resolution just falls
// through to the default port.
func TestReadLastDaemonToleratesMissingAndCorrupt(t *testing.T) {
	dir := stubCacheDir(t)
	path := filepath.Join(dir, "godot-ai-cli", "last-daemon.json")

	if _, ok := readLastDaemon(); ok {
		t.Error("missing file reported ok")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := readLastDaemon(); ok {
		t.Error("corrupt file reported ok")
	}

	if err := os.WriteFile(path, []byte(`{"ws_port":9500}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := readLastDaemon(); ok {
		t.Error("record without http_port reported ok")
	}
}

// TestDaemonPortCandidates pins the resolution order: explicit flag alone >
// recorded port (with the default as fallback) > default alone.
func TestDaemonPortCandidates(t *testing.T) {
	stubCacheDir(t)

	// No record: default only.
	if got := daemonPortCandidates(portProbeCmd(t, "")); len(got) != 1 || got[0] != daemon.DefaultHTTPPort {
		t.Errorf("no record: %v", got)
	}

	// Record on a custom port: recorded first, default as fallback.
	if err := writeLastDaemon(lastDaemonRecord{HTTPPort: 18201, WSPort: 19501}); err != nil {
		t.Fatal(err)
	}
	got := daemonPortCandidates(portProbeCmd(t, ""))
	if len(got) != 2 || got[0] != 18201 || got[1] != daemon.DefaultHTTPPort {
		t.Errorf("recorded port: %v", got)
	}

	// An explicit flag wins alone, even with a record present.
	if got := daemonPortCandidates(portProbeCmd(t, "19000")); len(got) != 1 || got[0] != 19000 {
		t.Errorf("explicit flag: %v", got)
	}

	// A record pointing at the default must not duplicate the probe.
	if err := writeLastDaemon(lastDaemonRecord{HTTPPort: daemon.DefaultHTTPPort, WSPort: daemon.DefaultWSPort}); err != nil {
		t.Fatal(err)
	}
	if got := daemonPortCandidates(portProbeCmd(t, "")); len(got) != 1 || got[0] != daemon.DefaultHTTPPort {
		t.Errorf("default record: %v", got)
	}

	// Corrupt record: default only.
	if err := os.WriteFile(lastDaemonPath(), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := daemonPortCandidates(portProbeCmd(t, "")); len(got) != 1 || got[0] != daemon.DefaultHTTPPort {
		t.Errorf("corrupt record: %v", got)
	}
}

// TestResolveDaemonPortProbesInOrder: a live daemon on the recorded port is
// found without touching the default; a dead recorded port falls back to
// probing the default before giving up, with every probe named in tried.
func TestResolveDaemonPortProbesInOrder(t *testing.T) {
	stubCacheDir(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/godot-ai/cli/health" {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	livePort := testServerPort(t, server)

	if err := writeLastDaemon(lastDaemonRecord{HTTPPort: livePort, WSPort: 1}); err != nil {
		t.Fatal(err)
	}
	port, tried, ok := resolveDaemonPort(portProbeCmd(t, ""))
	if !ok || port != livePort || len(tried) != 1 {
		t.Errorf("recorded live daemon: port=%d tried=%v ok=%v", port, tried, ok)
	}

	// Dead recorded port: the default 8000 is probed as fallback and the
	// failure names both. (Assumes nothing serves a godot-ai health endpoint
	// on 8000 — same free-port assumption the other daemon tests make.)
	dead := listenFree(t)
	deadPort := dead.Addr().(*net.TCPAddr).Port
	_ = dead.Close()
	if err := writeLastDaemon(lastDaemonRecord{HTTPPort: deadPort, WSPort: 1}); err != nil {
		t.Fatal(err)
	}
	port, tried, ok = resolveDaemonPort(portProbeCmd(t, ""))
	if ok {
		t.Fatalf("dead record unexpectedly resolved to %d", port)
	}
	if len(tried) != 2 || tried[0] != deadPort || tried[1] != daemon.DefaultHTTPPort {
		t.Errorf("tried = %v", tried)
	}
}

// TestStatusResolvesRecordedDaemon drives status end-to-end with NO
// --http-port flag: the recorded port leads to the running daemon.
func TestStatusResolvesRecordedDaemon(t *testing.T) {
	stubCacheDir(t)

	d, err := daemon.Start(context.Background(), daemon.Config{HTTPPort: 0, WSPort: 0, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	if err := writeLastDaemon(lastDaemonRecord{HTTPPort: d.HTTPPort(), WSPort: d.WSPort()}); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"status"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status via recorded port: %v\n%s", err, buf.String())
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("status output is not JSON: %v\n%s", err, buf.String())
	}
	daemonInfo, _ := out["daemon"].(map[string]any)
	if out["status"] != "ok" || daemonInfo["http_port"] != float64(d.HTTPPort()) {
		t.Errorf("out = %v", out)
	}
}

// TestStopRemovesRecordOfStoppedDaemon: stopping the recorded daemon (found
// WITHOUT an explicit flag) deletes last-daemon.json.
func TestStopRemovesRecordOfStoppedDaemon(t *testing.T) {
	stubCacheDir(t)

	d, err := daemon.Start(context.Background(), daemon.Config{HTTPPort: 0, WSPort: 0, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	if err := writeLastDaemon(lastDaemonRecord{HTTPPort: d.HTTPPort(), WSPort: d.WSPort()}); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"stop"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stop: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), `"status":"stopped"`) {
		t.Errorf("stop output:\n%s", buf.String())
	}
	if _, err := os.Stat(lastDaemonPath()); !os.IsNotExist(err) {
		t.Errorf("record still present after stopping the recorded daemon: %v", err)
	}
}

// TestStopKeepsRecordOfOtherDaemon: stopping a DIFFERENT daemon (explicit
// --http-port naming another port) must keep the record — it still
// describes the daemon the user last launched.
func TestStopKeepsRecordOfOtherDaemon(t *testing.T) {
	stubCacheDir(t)

	d, err := daemon.Start(context.Background(), daemon.Config{HTTPPort: 0, WSPort: 0, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	// The record names some other (already dead) daemon.
	dead := listenFree(t)
	deadPort := dead.Addr().(*net.TCPAddr).Port
	_ = dead.Close()
	if err := writeLastDaemon(lastDaemonRecord{HTTPPort: deadPort, WSPort: 1}); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"stop", "--http-port", itoa(d.HTTPPort())})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stop: %v\n%s", err, buf.String())
	}
	rec, ok := readLastDaemon()
	if !ok || rec.HTTPPort != deadPort {
		t.Errorf("record of another daemon was removed: %+v, %v", rec, ok)
	}
}

// TestServeWritesLastDaemon: a directly-run serve records its bound ports
// (free ports reserved here — the CLI command rejects 0, ephemeral binding
// is a daemon.Start-only test facility) so one-shot commands can find it.
func TestServeWritesLastDaemon(t *testing.T) {
	stubCacheDir(t)

	httpLn := listenFree(t)
	httpPort := httpLn.Addr().(*net.TCPAddr).Port
	_ = httpLn.Close()
	wsLn := listenFree(t)
	wsPort := wsLn.Addr().(*net.TCPAddr).Port
	_ = wsLn.Close()

	cmd := NewRootCommand()
	var buf syncBuffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"serve", "--http-port", itoa(httpPort), "--ws-port", itoa(wsPort)})

	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()

	// Wait for the record to appear, then learn the bound ports from it.
	var rec lastDaemonRecord
	deadline := time.Now().Add(5 * time.Second)
	for {
		var ok bool
		if rec, ok = readLastDaemon(); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("serve never wrote last-daemon.json; output:\n%s", buf.String())
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The startup line and the record agree on the bound ports.
	if !strings.Contains(buf.String(), `"http_port":`+itoa(rec.HTTPPort)) {
		t.Errorf("startup line disagrees with the record (%+v):\n%s", rec, buf.String())
	}

	// Shut the daemon down through the recorded port; serve must return.
	if _, err := postDaemonJSON(rec.HTTPPort, "/godot-ai/cli/shutdown", map[string]any{}, 5*time.Second); err != nil {
		t.Fatalf("shutdown via recorded port: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned error: %v\n%s", err, buf.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after shutdown")
	}
}
