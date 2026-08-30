package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	// defaultHandshakeTimeout mirrors the upstream 10s first-frame deadline:
	// the plugin must send its handshake within this window or the
	// connection is dropped.
	defaultHandshakeTimeout = 10 * time.Second

	// maxFrameBytes mirrors the upstream 4 MB max WS frame (screenshot
	// base64 payloads drove the original sizing).
	maxFrameBytes = 4 * 1024 * 1024

	// closeCodeDuplicateSession mirrors the upstream application close code
	// (RFC 6455 reserves 4000-4999) used when a handshake repeats an
	// already-registered session_id.
	closeCodeDuplicateSession = 4001
)

// sessionIDPattern mirrors the upstream handshake validation: the plugin
// always produces "<slug>@<4hex>", so this only rejects malformed peers.
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,128}$`)

// Session describes one connected Godot editor. All fields except
// readiness are immutable after the handshake.
type Session struct {
	ID              string
	GodotVersion    string
	ProjectPath     string
	PluginVersion   string
	ProtocolVersion int
	EditorPID       int

	mu        sync.RWMutex
	readiness string
	// customTools caches the editor's latest custom_tools_changed catalog
	// (raw tool spec dicts), so `custom list` can answer without a round
	// trip and even for tools registered before any client asked.
	customTools []any
}

// CustomTools returns the session's cached custom-tool catalog (nil when
// the editor never sent one).
func (s *Session) CustomTools() []any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]any(nil), s.customTools...)
}

// setCustomTools stores a new custom-tool catalog snapshot.
func (s *Session) setCustomTools(tools []any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.customTools = tools
}

// Readiness returns the last readiness snapshot known for this session
// (from the handshake, readiness_changed events, or response stamps).
func (s *Session) Readiness() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readiness
}

// setReadiness stores a new readiness value.
func (s *Session) setReadiness(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readiness = value
}

// EventListener receives plugin events: (session id, event name, payload).
type EventListener func(sessionID, event string, data map[string]any)

// pendingRequest tracks one in-flight command awaiting its response.
type pendingRequest struct {
	sessionID string
	ch        chan commandResult
}

// commandResult is either a plugin response or a locally generated failure
// (e.g. the session disconnected mid-flight).
type commandResult struct {
	resp *CommandResponse
	err  *CommandError
}

// wsConn serializes writes to one plugin connection.
type wsConn struct {
	conn *websocket.Conn
	wmu  sync.Mutex
}

// Server is the WebSocket backend the godot_ai plugin connects to.
type Server struct {
	version string

	// HandshakeTimeout bounds the wait for the first frame (default 10s).
	HandshakeTimeout time.Duration
	// ImportingHoldCap bounds the readiness gate's hold while the editor
	// imports (default 8s). Tests shrink it; see readiness.go.
	ImportingHoldCap time.Duration
	// ImportingProbeInterval is the delay between hold re-probes (default
	// 500ms).
	ImportingProbeInterval time.Duration
	// ProbeTimeout bounds one get_editor_state probe round trip (default 2s).
	ProbeTimeout time.Duration

	mu        sync.Mutex
	listener  net.Listener
	http      *http.Server
	sessions  map[string]*Session
	order     []string // insertion order, for stable Sessions() output
	activeID  string
	conns     map[string]*wsConn
	pending   map[string]*pendingRequest
	listeners []EventListener
	closed    bool
	wg        sync.WaitGroup
}

// NewServer builds a Server that reports version in handshake_ack frames.
// Pass pluginmeta.PluginVersion() in production.
func NewServer(version string) *Server {
	return &Server{
		version:          version,
		HandshakeTimeout: defaultHandshakeTimeout,
		sessions:         map[string]*Session{},
		conns:            map[string]*wsConn{},
		pending:          map[string]*pendingRequest{},
	}
}

// Start binds 127.0.0.1:port (0 picks an ephemeral port) and starts
// serving the WebSocket upgrade endpoint in the background.
func (s *Server) Start(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	// The plugin dials the root path; any path upgrades to keep the
	// endpoint forgiving of trailing-slash drift.
	mux.HandleFunc("/", s.handleUpgrade)
	s.http = &http.Server{Handler: mux, ReadHeaderTimeout: s.HandshakeTimeout}

	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("bridge: websocket server failed", "err", err)
		}
	}()
	return nil
}

// Addr returns the bound address (e.g. "127.0.0.1:9500"), or "" if Start
// has not been called.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// Port returns the bound TCP port, or 0 if Start has not been called.
func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return 0
	}
	return s.listener.Addr().(*net.TCPAddr).Port
}

// Shutdown closes the listener and every plugin connection, then waits for
// all handler goroutines to exit.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	httpSrv := s.http
	ln := s.listener
	conns := make([]*wsConn, 0, len(s.conns))
	for _, c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	var err error
	if httpSrv != nil {
		err = httpSrv.Shutdown(ctx)
	} else if ln != nil {
		err = ln.Close()
	}
	for _, c := range conns {
		_ = c.conn.CloseNow()
	}
	s.wg.Wait()
	return err
}

// OnEvent registers a listener for plugin events.
func (s *Server) OnEvent(l EventListener) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners = append(s.listeners, l)
}

// Sessions returns a snapshot of all registered sessions in connect order.
func (s *Server) Sessions() []*Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Session, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.sessions[id])
	}
	return out
}

// ActiveSession returns the session commands are routed to by default:
// the first connected session unless Activate switched it.
func (s *Server) ActiveSession() *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeID == "" {
		return nil
	}
	return s.sessions[s.activeID]
}

// Activate switches the active session. It returns false when the id is
// not registered.
func (s *Server) Activate(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[id]; !ok {
		return false
	}
	s.activeID = id
	return true
}

// CustomTools returns the cached custom-tool catalog for one session
// (empty sessionID resolves to the active session). ok is false when no
// session resolves.
func (s *Server) CustomTools(sessionID string) (tools []any, ok bool) {
	sess := s.resolveSession(sessionID)
	if sess == nil {
		return nil, false
	}
	return sess.CustomTools(), true
}

// handleUpgrade accepts the WebSocket upgrade and hands the connection to
// the per-connection lifecycle.
func (s *Server) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	// CSRF hardening: browsers always send an Origin header on WebSocket
	// handshakes, so a browser page on any site could otherwise drive our
	// loopback bridge. The GDScript plugin is a native client and sends NO
	// Origin header, so: absent Origin is accepted, a loopback Origin host
	// (any port) is accepted, everything else is rejected before upgrading.
	if origin := r.Header.Get("Origin"); origin != "" && !isLoopbackOrigin(origin) {
		slog.Warn("bridge: rejecting websocket upgrade with non-loopback origin", "origin", origin)
		http.Error(w, "websocket upgrades from non-loopback origins are rejected", http.StatusForbidden)
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Loopback-only listener plus the Origin pre-check above; native
		// plugin clients carry no Origin header.
		InsecureSkipVerify: true,
		// No compression: the plugin never negotiates it.
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		slog.Warn("bridge: websocket accept failed", "err", err)
		return
	}
	s.mu.Lock()
	if s.closed {
		// Shutdown already started waiting on wg; adding now would race.
		s.mu.Unlock()
		_ = c.CloseNow()
		return
	}
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	s.handleConn(c)
}

// isLoopbackOrigin reports whether an Origin header value names a loopback
// host (127.0.0.1 / localhost / [::1], any port and either http/https
// scheme). Malformed values are not loopback.
func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

// handleConn runs the lifecycle of one plugin connection. It is invoked
// by the HTTP upgrade handler and blocks until the connection closes.
func (s *Server) handleConn(c *websocket.Conn) {
	c.SetReadLimit(maxFrameBytes)

	// First frame must be a valid handshake within the deadline.
	readCtx, cancel := context.WithTimeout(context.Background(), s.HandshakeTimeout)
	_, rawMsg, err := c.Read(readCtx)
	cancel()
	if err != nil {
		slog.Warn("bridge: first frame read failed", "err", err)
		_ = c.Close(websocket.StatusPolicyViolation, "handshake timeout or read error")
		return
	}
	var hs Handshake
	if err := json.Unmarshal(rawMsg, &hs); err != nil ||
		hs.Type != "handshake" || !sessionIDPattern.MatchString(hs.SessionID) {
		slog.Warn("bridge: rejecting malformed handshake frame")
		_ = c.Close(websocket.StatusPolicyViolation, "invalid handshake")
		return
	}

	wc := &wsConn{conn: c}
	if !s.registerSession(&hs, wc) {
		_ = c.Close(closeCodeDuplicateSession, "session id already registered")
		return
	}

	// Report the server version; the plugin strict-checks it against its
	// own plugin.cfg version.
	if err := wc.writeJSON(context.Background(), handshakeAck{
		Type:          "handshake_ack",
		ServerVersion: s.version,
	}); err != nil {
		slog.Warn("bridge: handshake_ack send failed", "session", hs.SessionID, "err", err)
		s.unregisterSession(hs.SessionID)
		_ = c.CloseNow()
		return
	}

	slog.Info("bridge: session connected",
		"session", hs.SessionID, "pid", hs.EditorPID,
		"godot", hs.GodotVersion, "project", hs.ProjectPath)

	// Read loop: no read deadlines and no ping/pong enforcement — during an
	// exclusive test run the editor cannot answer pings, and loopback TCP
	// liveness is enough.
	for {
		_, frame, err := c.Read(context.Background())
		if err != nil {
			break
		}
		s.handleFrame(hs.SessionID, frame)
	}

	slog.Info("bridge: session disconnected", "session", hs.SessionID)
	s.unregisterSession(hs.SessionID)
	_ = c.CloseNow()
}

// registerSession adds the session and connection to the registry. It
// returns false when the session_id is already registered (duplicate).
func (s *Server) registerSession(hs *Handshake, wc *wsConn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if _, exists := s.sessions[hs.SessionID]; exists {
		return false
	}
	sess := &Session{
		ID:              hs.SessionID,
		GodotVersion:    hs.GodotVersion,
		ProjectPath:     hs.ProjectPath,
		PluginVersion:   hs.PluginVersion,
		ProtocolVersion: hs.ProtocolVersion,
		EditorPID:       hs.EditorPID,
		readiness:       hs.Readiness,
	}
	s.sessions[hs.SessionID] = sess
	s.order = append(s.order, hs.SessionID)
	if s.activeID == "" {
		s.activeID = hs.SessionID
	}
	s.conns[hs.SessionID] = wc
	return true
}

// unregisterSession removes a session, fails its in-flight commands, and
// clears the active pointer when it pointed at the removed session.
func (s *Server) unregisterSession(id string) {
	s.mu.Lock()
	if _, ok := s.sessions[id]; !ok {
		s.mu.Unlock()
		return
	}
	delete(s.sessions, id)
	delete(s.conns, id)
	for i, sid := range s.order {
		if sid == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	if s.activeID == id {
		s.activeID = ""
		// Fall back to the oldest remaining session so an empty sessionID
		// keeps working when another editor is still connected.
		if len(s.order) > 0 {
			s.activeID = s.order[0]
		}
	}
	var failed []*pendingRequest
	for reqID, p := range s.pending {
		if p.sessionID == id {
			delete(s.pending, reqID)
			failed = append(failed, p)
		}
	}
	s.mu.Unlock()

	// Fail in-flight commands now instead of letting callers wait out their
	// full per-command timeout after an editor crash or plugin reload.
	for _, p := range failed {
		p.ch <- commandResult{err: &CommandError{
			Code:    "PLUGIN_DISCONNECTED",
			Message: fmt.Sprintf("Session %s disconnected while the command was in flight", id),
			Data:    map[string]any{"retryable": true, "reason": "session_disconnected"},
		}}
	}
}

// handleFrame routes one inbound frame: events go to listeners, command
// responses resolve pending requests. Malformed frames are dropped with a
// warning so a single bad frame never tears down the session.
func (s *Server) handleFrame(sessionID string, frame []byte) {
	var raw map[string]any
	if err := json.Unmarshal(frame, &raw); err != nil {
		slog.Warn("bridge: dropping non-JSON frame", "session", sessionID, "err", err)
		return
	}

	if raw["type"] == "event" {
		var ev eventFrame
		if err := json.Unmarshal(frame, &ev); err != nil {
			slog.Warn("bridge: dropping malformed event frame", "session", sessionID, "err", err)
			return
		}
		s.handleEvent(sessionID, &ev)
		return
	}

	var resp CommandResponse
	if err := json.Unmarshal(frame, &resp); err != nil || resp.RequestID == "" {
		slog.Warn("bridge: dropping malformed command response", "session", sessionID)
		return
	}

	// Heal the cached readiness from the response stamp before resolving:
	// waiters (e.g. the readiness gate) observe the fresh value immediately.
	if resp.Readiness != "" {
		s.syncReadiness(sessionID, resp.Readiness)
	}

	s.mu.Lock()
	p, ok := s.pending[resp.RequestID]
	if ok {
		if p.sessionID != sessionID {
			// Cross-session response: only the connection the command was
			// sent to may resolve it. Drop the forged frame.
			s.mu.Unlock()
			slog.Warn("bridge: dropping response from wrong session",
				"session", sessionID, "request", resp.RequestID)
			return
		}
		delete(s.pending, resp.RequestID)
	}
	s.mu.Unlock()

	if !ok {
		// Unknown request_id (late answer to a timed-out command, etc.).
		slog.Warn("bridge: dropping response with unknown request_id",
			"session", sessionID, "request", resp.RequestID)
		return
	}
	p.ch <- commandResult{resp: &resp}
}

// handleEvent updates cached session state and notifies listeners.
func (s *Server) handleEvent(sessionID string, ev *eventFrame) {
	if ev.Event == "readiness_changed" {
		if value, ok := ev.Data["readiness"].(string); ok {
			s.mu.Lock()
			if sess := s.sessions[sessionID]; sess != nil {
				sess.setReadiness(value)
			}
			s.mu.Unlock()
		}
	}
	if ev.Event == "custom_tools_changed" {
		if tools, ok := ev.Data["tools"].([]any); ok {
			s.mu.Lock()
			if sess := s.sessions[sessionID]; sess != nil {
				sess.setCustomTools(tools)
			}
			s.mu.Unlock()
		}
	}

	s.mu.Lock()
	listeners := append([]EventListener(nil), s.listeners...)
	s.mu.Unlock()
	for _, l := range listeners {
		l(sessionID, ev.Event, ev.Data)
	}
}

// syncReadiness copies an authoritative readiness snapshot onto a session,
// mirroring upstream sync_readiness_for_session: unknown values are ignored
// (forward compatibility with newer plugins), not propagated.
func (s *Server) syncReadiness(sessionID, value string) {
	if !knownReadiness[value] {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess := s.sessions[sessionID]; sess != nil {
		sess.setReadiness(value)
	}
}

// SendCommand sends a command to a session and waits for its response.
//
// An empty sessionID targets the active session. timeout bounds the whole
// round trip including the socket write. A *CommandError is returned on
// every failure path; it is nil exactly when data is valid.
func (s *Server) SendCommand(ctx context.Context, sessionID, command string, params map[string]any, timeout time.Duration) (map[string]any, *CommandError) {
	// NaN/Infinity are not representable in JSON: a write-tool param that
	// went NaN upstream would serialize as null and silently corrupt scene
	// data while reporting success. Reject before sending instead.
	if path := findNonFiniteFloat(params, "params"); path != "" {
		return nil, &CommandError{
			Code:    "INVALID_PARAMS",
			Message: fmt.Sprintf("non-finite float at %s: NaN/Infinity are not representable in JSON", path),
			Data:    map[string]any{"path": path, "retryable": false},
		}
	}

	s.mu.Lock()
	if sessionID == "" {
		sessionID = s.activeID
		if sessionID == "" {
			s.mu.Unlock()
			return nil, &CommandError{
				Code:    "PLUGIN_DISCONNECTED",
				Message: "No Godot editor is connected to this server",
				Data:    map[string]any{"retryable": true, "reason": "no_active_session"},
			}
		}
	}
	sess := s.sessions[sessionID]
	if sess == nil {
		s.mu.Unlock()
		return nil, &CommandError{
			Code:    "SESSION_NOT_FOUND",
			Message: fmt.Sprintf("No session registered with id %q", sessionID),
			Data:    map[string]any{"session_id": sessionID, "retryable": false},
		}
	}
	wc := s.conns[sessionID]

	p := &pendingRequest{sessionID: sessionID, ch: make(chan commandResult, 1)}
	requestID := newRequestID()
	s.pending[requestID] = p
	s.mu.Unlock()

	// Always unregister on exit: the receiver pops on the happy path, so
	// this is a no-op there; on write failure / timeout / cancellation it
	// prevents entries leaking into the pending map forever.
	defer func() {
		s.mu.Lock()
		delete(s.pending, requestID)
		s.mu.Unlock()
	}()

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	err := wc.writeJSON(cmdCtx, commandRequest{
		RequestID: requestID,
		Command:   command,
		Params:    params,
	})
	if err != nil {
		if cmdCtx.Err() != nil && ctx.Err() == nil {
			return nil, timeoutError(command, timeout, sessionID)
		}
		return nil, &CommandError{
			Code:    "PLUGIN_DISCONNECTED",
			Message: fmt.Sprintf("Failed to send command %s to session %s: %v", command, sessionID, err),
			Data:    map[string]any{"retryable": true, "reason": "session_disconnected"},
		}
	}

	select {
	case result := <-p.ch:
		if result.err != nil {
			return nil, result.err
		}
		resp := result.resp
		if resp.Status == "error" {
			if resp.Error != nil {
				return nil, &CommandError{
					Code:    resp.Error.Code,
					Message: resp.Error.Message,
					Data:    resp.Error.Data,
				}
			}
			return nil, &CommandError{
				Code:    "INTERNAL_ERROR",
				Message: "Plugin returned an error status without an error detail",
				Data:    map[string]any{"retryable": false},
			}
		}
		return resp.Data, nil
	case <-cmdCtx.Done():
		if ctx.Err() != nil && !errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			return nil, &CommandError{
				Code:    "TRANSPORT_TIMEOUT",
				Message: fmt.Sprintf("Command %s cancelled on session %s: %v", command, sessionID, ctx.Err()),
				Data:    map[string]any{"retryable": true},
			}
		}
		return nil, timeoutError(command, timeout, sessionID)
	}
}

// timeoutError mirrors the upstream TRANSPORT_TIMEOUT shape: the request
// was delivered but the reply did not arrive in budget — retryable.
func timeoutError(command string, timeout time.Duration, sessionID string) *CommandError {
	return &CommandError{
		Code:    "TRANSPORT_TIMEOUT",
		Message: fmt.Sprintf("Command %s timed out after %s on session %s", command, timeout, sessionID),
		Data:    map[string]any{"retryable": true},
	}
}

// writeJSON marshals v and sends it as one text frame. Writes are
// serialized per connection so concurrent SendCommand calls never
// interleave frames.
func (w *wsConn) writeJSON(ctx context.Context, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	w.wmu.Lock()
	defer w.wmu.Unlock()
	return w.conn.Write(ctx, websocket.MessageText, payload)
}

// newRequestID returns a uuid4-style hex id without dashes, matching the
// upstream uuid4().hex request ids.
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand never fails on supported platforms; fall back to a
		// time-based id rather than panicking.
		return fmt.Sprintf("%032x", time.Now().UnixNano())
	}
	// Set the uuid4 version/variant bits for readers familiar with the shape.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[:])
}

// findNonFiniteFloat returns the key path of the first non-finite float in
// a params tree, or "" when every float is finite. Mirrors upstream
// envelope.py find_non_finite_float.
func findNonFiniteFloat(value any, path string) string {
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return path
		}
	case float32:
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return path
		}
	case map[string]any:
		for key, item := range v {
			if found := findNonFiniteFloat(item, path+"."+key); found != "" {
				return found
			}
		}
	case []any:
		for i, item := range v {
			if found := findNonFiniteFloat(item, fmt.Sprintf("%s[%d]", path, i)); found != "" {
				return found
			}
		}
	}
	return ""
}
