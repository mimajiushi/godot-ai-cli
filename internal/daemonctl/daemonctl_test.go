package daemonctl_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
	"github.com/mimajiushi/godot-ai-cli/internal/daemonctl"
)

// freePort reserves an ephemeral port and releases it for the test's use.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// TestEnsureRunningAlreadyUp: a live compatible daemon short-circuits.
func TestEnsureRunningAlreadyUp(t *testing.T) {
	httpPort, wsPort := freePort(t), freePort(t)
	d, err := daemon.Start(context.Background(), daemon.Config{
		HTTPPort: httpPort, WSPort: wsPort, Version: "3.2.4",
	})
	if err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	spawned := false
	restore := stubSpawn(t, func(int, int) error { spawned = true; return nil })
	defer restore()

	start := time.Now()
	running, err := daemonctl.EnsureRunning(context.Background(), daemon.Config{HTTPPort: httpPort, WSPort: wsPort})
	if err != nil || !running {
		t.Fatalf("EnsureRunning = %v, %v", running, err)
	}
	if spawned {
		t.Error("EnsureRunning spawned a daemon although one was already running")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("already-running probe took %s, expected near-instant", time.Since(start))
	}
}

// TestEnsureRunningForeignPort: a port held by a non-godot-ai HTTP server
// must never be killed or replaced.
func TestEnsureRunningForeignPort(t *testing.T) {
	httpPort := freePort(t)
	foreign := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":"something-else"}`))
	})}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", httpPort))
	if err != nil {
		t.Fatal(err)
	}
	go foreign.Serve(ln)
	t.Cleanup(func() { foreign.Close() })

	// Even the spawn stub must not matter here: the foreign answer arrives
	// after the spawn attempt and must surface as a refusal error.
	restore := stubSpawn(t, func(int, int) error { return nil })
	defer restore()

	_, err = daemonctl.EnsureRunning(context.Background(), daemon.Config{
		HTTPPort: httpPort, WSPort: freePort(t),
	})
	if err == nil {
		t.Fatal("expected refusal error for foreign-occupied port")
	}
	if !strings.Contains(err.Error(), "foreign process") {
		t.Errorf("error = %v, want a foreign-process refusal", err)
	}
}

// TestEnsureRunningForeignGodotAI: an upstream (Python) godot-ai server
// answers /godot-ai/status with name "godot-ai" but has no /cli API. It
// must be rejected with a dedicated ForeignServerError, never adopted and
// never killed.
func TestEnsureRunningForeignGodotAI(t *testing.T) {
	httpPort := freePort(t)
	upstream := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/godot-ai/status" {
			_, _ = w.Write([]byte(`{"name":"godot-ai","version":"3.2.4","server_version":"3.2.4","ws_port":9500}`))
			return
		}
		http.NotFound(w, r) // no /godot-ai/cli/* on the upstream server
	})}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", httpPort))
	if err != nil {
		t.Fatal(err)
	}
	go upstream.Serve(ln)
	t.Cleanup(func() { upstream.Close() })

	spawned := false
	restore := stubSpawn(t, func(int, int) error { spawned = true; return nil })
	defer restore()

	_, err = daemonctl.EnsureRunning(context.Background(), daemon.Config{
		HTTPPort: httpPort, WSPort: freePort(t),
	})
	var foreignErr *daemonctl.ForeignServerError
	if !errors.As(err, &foreignErr) {
		t.Fatalf("err = %v (%T), want ForeignServerError", err, err)
	}
	if !strings.Contains(err.Error(), "Python") {
		t.Errorf("error should explain the Python server situation: %v", err)
	}
	if spawned {
		t.Error("must not spawn when a foreign godot-ai server occupies the port")
	}
}

// TestEnsureRunningSpawns: no daemon answers → EnsureRunning spawns one
// (TestHelperProcess stands in for `godot-ai-cli serve`) and waits for it.
func TestEnsureRunningSpawns(t *testing.T) {
	httpPort, wsPort := freePort(t), freePort(t)

	restore := stubSpawn(t, func(hp, wp int) error {
		// Standard TestHelperProcess pattern: re-run this test binary as a
		// helper that starts a real daemon on the requested ports.
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
		cmd.Env = append(os.Environ(),
			"GODOT_AI_CLI_HELPER=1",
			"GODOT_AI_CLI_HELPER_HTTP_PORT="+strconv.Itoa(hp),
			"GODOT_AI_CLI_HELPER_WS_PORT="+strconv.Itoa(wp),
		)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			return err
		}
		// No Process.Release here: the test keeps the handle so cleanup
		// can kill the helper. (The real spawn path releases on purpose.)
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
		return nil
	})
	defer restore()

	running, err := daemonctl.EnsureRunning(context.Background(), daemon.Config{
		HTTPPort: httpPort, WSPort: wsPort,
	})
	if err != nil || !running {
		t.Fatalf("EnsureRunning = %v, %v", running, err)
	}

	// The helper really serves: the status endpoint must answer.
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/godot-ai/status", httpPort))
	if err != nil {
		t.Fatalf("helper daemon not answering: %v", err)
	}
	resp.Body.Close()
}

// TestHelperProcess is not a test: it is the subprocess EnsureRunning
// spawns in TestEnsureRunningSpawns, standing in for the real CLI's
// `serve` command. It starts a daemon on env-provided ports and blocks.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GODOT_AI_CLI_HELPER") != "1" {
		return
	}
	httpPort, _ := strconv.Atoi(os.Getenv("GODOT_AI_CLI_HELPER_HTTP_PORT"))
	wsPort, _ := strconv.Atoi(os.Getenv("GODOT_AI_CLI_HELPER_WS_PORT"))
	d, err := daemon.Start(context.Background(), daemon.Config{
		HTTPPort: httpPort, WSPort: wsPort, Version: "3.2.4",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "helper: %v\n", err)
		os.Exit(1)
	}
	<-d.Done() // runs until killed
	os.Exit(0)
}

// stubSpawn replaces daemonctl's spawn function for one test.
func stubSpawn(t *testing.T, fn func(httpPort, wsPort int) error) (restore func()) {
	t.Helper()
	return daemonctl.StubSpawnForTest(fn)
}

// TestEnsureRunningAdoptWSPortMismatch: a running OUR daemon with a
// different WS port must not be adopted — the plugin would reject the
// handshake as ws_port_mismatch.
func TestEnsureRunningAdoptWSPortMismatch(t *testing.T) {
	httpPort, wsPort := freePort(t), freePort(t)
	d, err := daemon.Start(context.Background(), daemon.Config{
		HTTPPort: httpPort, WSPort: wsPort, Version: "3.2.4",
	})
	if err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	spawned := false
	restore := stubSpawn(t, func(int, int) error { spawned = true; return nil })
	defer restore()

	otherWS := freePort(t)
	_, err = daemonctl.EnsureRunning(context.Background(), daemon.Config{
		HTTPPort: httpPort, WSPort: otherWS, Version: "3.2.4",
	})
	var mismatchErr *daemonctl.DaemonMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("err = %v (%T), want DaemonMismatchError", err, err)
	}
	if mismatchErr.RunningWSPort != wsPort || mismatchErr.RequestedWSPort != otherWS {
		t.Errorf("mismatch detail = %+v, want running %d requested %d",
			mismatchErr, wsPort, otherWS)
	}
	if !strings.Contains(err.Error(), "godot-ai-cli stop") {
		t.Errorf("error should name the stop remedy: %v", err)
	}
	if spawned {
		t.Error("must not spawn when a mismatched daemon occupies the port")
	}
}

// TestEnsureRunningAdoptVersionMismatch: a running OUR daemon with a
// different plugin version must not be adopted — the plugin enforces
// strict version equality in the handshake.
func TestEnsureRunningAdoptVersionMismatch(t *testing.T) {
	httpPort, wsPort := freePort(t), freePort(t)
	d, err := daemon.Start(context.Background(), daemon.Config{
		HTTPPort: httpPort, WSPort: wsPort, Version: "3.2.4",
	})
	if err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	restore := stubSpawn(t, func(int, int) error { return nil })
	defer restore()

	_, err = daemonctl.EnsureRunning(context.Background(), daemon.Config{
		HTTPPort: httpPort, WSPort: wsPort, Version: "9.9.9",
	})
	var mismatchErr *daemonctl.DaemonMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("err = %v (%T), want DaemonMismatchError", err, err)
	}
	if mismatchErr.RunningVersion != "3.2.4" || mismatchErr.RequestedVersion != "9.9.9" {
		t.Errorf("mismatch detail = %+v, want running 3.2.4 requested 9.9.9", mismatchErr)
	}
}
