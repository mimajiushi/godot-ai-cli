// Package daemon runs the combined godot-ai backend: the plugin-facing
// WebSocket bridge on one port and the agent-facing HTTP API on another.
//
// The HTTP surface is plugin-adoption-compatible with the upstream Python
// server (the GDScript plugin probes /godot-ai/status to adopt an already
// running backend), plus godot-ai-cli specific endpoints under
// /godot-ai/cli/*.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/mimajiushi/godot-ai-cli/internal/bridge"
	"github.com/mimajiushi/godot-ai-cli/internal/pluginmeta"
)

const (
	// DefaultHTTPPort is the agent-facing API port the upstream plugin
	// probes for server adoption.
	DefaultHTTPPort = 8000
	// DefaultWSPort is the port the plugin dials for its WebSocket bridge.
	DefaultWSPort = 9500

	// defaultExecuteTimeoutSec mirrors the upstream default command budget
	// when the caller does not pass timeout_sec.
	defaultExecuteTimeoutSec = 8.0
	// maxExecuteTimeoutSec caps caller-provided timeouts (test runs can
	// legitimately take minutes; beyond this the call is a bug).
	maxExecuteTimeoutSec = 600.0

	// maxMutationBodyBytes caps the request body of the POST mutation
	// endpoints. Bodies are small JSON envelopes; 8 MiB is generous headroom.
	maxMutationBodyBytes = 8 << 20
)

// Config configures one daemon instance.
type Config struct {
	// HTTPPort is the agent-facing API port (0 selects DefaultHTTPPort in
	// Run; Start binds it literally, so 0 means an ephemeral port there).
	HTTPPort int
	// WSPort is the plugin-facing WebSocket port (same 0 semantics).
	WSPort int
	// Version is reported as server_version everywhere. Defaults to the
	// vendored plugin version.
	Version string
}

// withDefaults fills zero-value fields with their production defaults.
func (c Config) withDefaults() Config {
	if c.HTTPPort == 0 {
		c.HTTPPort = DefaultHTTPPort
	}
	if c.WSPort == 0 {
		c.WSPort = DefaultWSPort
	}
	if c.Version == "" {
		c.Version = pluginmeta.PluginVersion()
	}
	return c
}

// Daemon is a running backend instance.
type Daemon struct {
	cfg    Config
	bridge *bridge.Server
	http   *http.Server
	httpLn net.Listener

	// cancel triggers a graceful shutdown (signal, Run caller, or the
	// /godot-ai/cli/shutdown endpoint); done closes once the shutdown has
	// completed.
	cancel context.CancelFunc
	done   chan struct{}
}

// Start binds both listeners and serves in the background. Port 0 selects
// an ephemeral port (read the real one via HTTPPort()/WSPort()). The
// daemon shuts down when ctx is done or RequestShutdown is called (the
// shutdown HTTP endpoint uses the latter); Done reports completion.
func Start(ctx context.Context, cfg Config) (*Daemon, error) {
	if cfg.Version == "" {
		cfg.Version = pluginmeta.PluginVersion()
	}
	b := bridge.NewServer(cfg.Version)
	if err := b.Start(cfg.WSPort); err != nil {
		return nil, fmt.Errorf("websocket bridge: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	d := &Daemon{cfg: cfg, bridge: b, cancel: cancel, done: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /godot-ai/status", d.handleStatus)
	mux.HandleFunc("GET /godot-ai/cli/health", d.handleHealth)
	mux.HandleFunc("GET /godot-ai/cli/sessions", d.handleSessions)
	mux.HandleFunc("GET /godot-ai/cli/custom-tools", d.handleCustomTools)
	mux.HandleFunc("POST /godot-ai/cli/activate", d.handleActivate)
	mux.HandleFunc("POST /godot-ai/cli/execute", d.handleExecute)
	mux.HandleFunc("POST /godot-ai/cli/shutdown", d.handleShutdown)

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.HTTPPort))
	if err != nil {
		cancel()
		_ = b.Shutdown(context.Background())
		return nil, fmt.Errorf("http listener: %w", err)
	}
	d.httpLn = ln
	d.http = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := d.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("daemon: http server failed", "err", err)
		}
	}()

	// Record our identity so `status`/`stop` can distinguish "no daemon"
	// from "stale PID file" diagnostics. Removed again on clean shutdown.
	if err := d.writePIDFile(); err != nil {
		slog.Warn("daemon: pid file write failed", "err", err)
	}

	// Single owner of the real shutdown: whoever cancels runCtx first
	// (parent ctx, RequestShutdown, or Shutdown) routes through here.
	go func() {
		<-runCtx.Done()
		shutdownCtx, release := context.WithTimeout(context.Background(), 5*time.Second)
		defer release()
		_ = d.realShutdown(shutdownCtx)
		close(d.done)
	}()
	return d, nil
}

// Run starts the daemon with production defaults applied, blocks until ctx
// is done or a shutdown is requested over HTTP, then returns after the
// graceful shutdown completes.
func Run(ctx context.Context, cfg Config) error {
	d, err := Start(ctx, cfg.withDefaults())
	if err != nil {
		return err
	}
	<-d.Done()
	return nil
}

// RequestShutdown asks the daemon to shut down gracefully (non-blocking).
func (d *Daemon) RequestShutdown() { d.cancel() }

// Done returns a channel that closes once shutdown has completed.
func (d *Daemon) Done() <-chan struct{} { return d.done }

// Shutdown requests the shutdown and blocks until it completes.
func (d *Daemon) Shutdown(_ context.Context) error {
	d.cancel()
	<-d.done
	return nil
}

// realShutdown stops the HTTP server, the WebSocket bridge, and removes
// the PID file. Called exactly once, by the watcher goroutine in Start.
func (d *Daemon) realShutdown(ctx context.Context) error {
	httpErr := d.http.Shutdown(ctx)
	bridgeErr := d.bridge.Shutdown(ctx)
	if err := os.Remove(pidFilePath(d.HTTPPort())); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("daemon: pid file removal failed", "err", err)
	}
	return errors.Join(httpErr, bridgeErr)
}

// Bridge exposes the WebSocket bridge (sessions, commands, events).
func (d *Daemon) Bridge() *bridge.Server { return d.bridge }

// pidFilePath is where the daemon records its identity for the given HTTP
// port: <user cache dir>/godot-ai-cli/daemon-<httpPort>.json.
func pidFilePath(httpPort int) string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "godot-ai-cli", fmt.Sprintf("daemon-%d.json", httpPort))
}

// PIDFilePath exposes the identity-file location for diagnostics (status
// uses it to explain a "daemon_not_running" result).
func PIDFilePath(httpPort int) string { return pidFilePath(httpPort) }

// pidFileContent is the identity record written while a daemon runs.
type pidFileContent struct {
	PID       int    `json:"pid"`
	HTTPPort  int    `json:"http_port"`
	WSPort    int    `json:"ws_port"`
	Version   string `json:"version"`
	StartedAt string `json:"started_at"`
}

// writePIDFile records this daemon's identity. It is a hint only — callers
// must always probe HTTP first.
func (d *Daemon) writePIDFile() error {
	path := pidFilePath(d.HTTPPort())
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.Marshal(pidFileContent{
		PID:       os.Getpid(),
		HTTPPort:  d.HTTPPort(),
		WSPort:    d.bridge.Port(),
		Version:   d.cfg.Version,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	return os.WriteFile(path, payload, 0o600)
}

// handleShutdown gracefully stops the daemon. The response goes out first;
// the actual shutdown runs after a short delay so this very request is not
// killed mid-write.
func (d *Daemon) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if !requireJSONContentType(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "shutting_down"})
	go func() {
		time.Sleep(100 * time.Millisecond)
		d.RequestShutdown()
	}()
}

// HTTPPort returns the actually bound HTTP port.
func (d *Daemon) HTTPPort() int {
	return d.httpLn.Addr().(*net.TCPAddr).Port
}

// WSPort returns the actually bound WebSocket port.
func (d *Daemon) WSPort() int { return d.bridge.Port() }

// handleStatus serves the plugin-adoption probe. The GDScript plugin GETs
// this to decide whether a compatible server is already running, so the
// field set must stay exactly compatible.
func (d *Daemon) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":                    "godot-ai",
		"server_version":          d.cfg.Version,
		"version":                 d.cfg.Version,
		"ws_port":                 d.bridge.Port(),
		"attach_protocol_version": 1,
		"package_path":            "godot-ai-cli",
		"pid":                     os.Getpid(),
		// The fork never phones home; publish it so the 3.2.5+ dock
		// telemetry tooltip reflects the running server's real state.
		"telemetry_enabled": false,
	})
}

// handleHealth is the CLI liveness probe.
func (d *Daemon) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"version":  d.cfg.Version,
		"sessions": len(d.bridge.Sessions()),
	})
}

// handleSessions lists every connected editor session.
func (d *Daemon) handleSessions(w http.ResponseWriter, _ *http.Request) {
	active := d.bridge.ActiveSession()
	activeID := ""
	if active != nil {
		activeID = active.ID
	}
	sessions := make([]map[string]any, 0)
	for _, s := range d.bridge.Sessions() {
		sessions = append(sessions, map[string]any{
			"session_id":    s.ID,
			"godot_version": s.GodotVersion,
			"project_path":  s.ProjectPath,
			"readiness":     s.Readiness(),
			"editor_pid":    s.EditorPID,
			"active":        s.ID == activeID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

// handleCustomTools serves the cached custom-tool catalog of one session
// (default: the active session) for `custom list`.
func (d *Daemon) handleCustomTools(w http.ResponseWriter, r *http.Request) {
	tools, ok := d.bridge.CustomTools(r.URL.Query().Get("session_id"))
	if !ok {
		writeJSON(w, http.StatusOK, (&bridge.CommandError{
			Code:    "NO_SESSION",
			Message: "No editor session is connected",
			Data:    map[string]any{"retryable": true},
		}).ToJSON())
		return
	}
	if tools == nil {
		tools = []any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": tools, "tool_count": len(tools)})
}

// activateRequest is the POST /godot-ai/cli/activate body.
type activateRequest struct {
	SessionID string `json:"session_id"`
}

// handleActivate switches the active session.
func (d *Daemon) handleActivate(w http.ResponseWriter, r *http.Request) {
	if !requireJSONContentType(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMutationBodyBytes)
	var req activateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SessionID == "" {
		writeJSON(w, http.StatusBadRequest, (&bridge.CommandError{
			Code:    "INVALID_PARAMS",
			Message: "Body must be JSON with a non-empty session_id",
			Data:    map[string]any{"retryable": false},
		}).ToJSON())
		return
	}
	if !d.bridge.Activate(req.SessionID) {
		writeJSON(w, http.StatusNotFound, (&bridge.CommandError{
			Code:    "SESSION_NOT_FOUND",
			Message: fmt.Sprintf("No session registered with id %q", req.SessionID),
			Data:    map[string]any{"session_id": req.SessionID, "retryable": false},
		}).ToJSON())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"active_session": req.SessionID,
	})
}

// executeRequest is the POST /godot-ai/cli/execute body.
type executeRequest struct {
	Command    string         `json:"command"`
	Params     map[string]any `json:"params"`
	SessionID  string         `json:"session_id"`
	TimeoutSec float64        `json:"timeout_sec"`
	Write      bool           `json:"write"`
}

// handleExecute runs one plugin command. All outcomes return HTTP 200 with
// a JSON body — agents read the body; transport-level HTTP codes are not
// part of the contract.
func (d *Daemon) handleExecute(w http.ResponseWriter, r *http.Request) {
	if !requireJSONContentType(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMutationBodyBytes)
	var req executeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Command == "" {
		writeJSON(w, http.StatusOK, (&bridge.CommandError{
			Code:    "INVALID_PARAMS",
			Message: "Body must be JSON with a non-empty command",
			Data:    map[string]any{"retryable": false},
		}).ToJSON())
		return
	}

	timeout := time.Duration(req.TimeoutSec * float64(time.Second))
	if req.TimeoutSec <= 0 {
		timeout = time.Duration(defaultExecuteTimeoutSec * float64(time.Second))
	} else if req.TimeoutSec > maxExecuteTimeoutSec {
		timeout = time.Duration(maxExecuteTimeoutSec * float64(time.Second))
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout+5*time.Second)
	defer cancel()

	if req.Write {
		if cmdErr := d.bridge.RequireWritable(ctx, req.SessionID); cmdErr != nil {
			writeJSON(w, http.StatusOK, cmdErr.ToJSON())
			return
		}
	}

	data, cmdErr := d.bridge.SendCommand(ctx, req.SessionID, req.Command, req.Params, timeout)
	if cmdErr != nil {
		writeJSON(w, http.StatusOK, cmdErr.ToJSON())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "data": data})
}

// requireJSONContentType guards the loopback mutation endpoints against
// browser CSRF: cross-origin forms/fetch can only send CORS-safelisted
// content types (text/plain & friends) without a preflight this server
// never answers, so requiring application/json rejects those requests
// outright. A charset suffix on application/json is accepted.
func requireJSONContentType(w http.ResponseWriter, r *http.Request) bool {
	ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || ct != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType, (&bridge.CommandError{
			Code:    "UNSUPPORTED_CONTENT_TYPE",
			Message: "Content-Type must be application/json",
			Data:    map[string]any{"retryable": false},
		}).ToJSON())
		return false
	}
	return true
}

// writeJSON serializes v as the response body.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("daemon: response encode failed", "err", err)
	}
}
