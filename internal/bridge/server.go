package bridge

import (
	"context"
	"crypto/hmac"
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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/mimajiushi/godot-ai-cli/internal/pluginmeta"
)

const (
	// defaultHandshakeTimeout mirrors the upstream v4 5s handshake window
	// (DEFAULT_HANDSHAKE_TIMEOUT_SECONDS): the plugin must complete the
	// auth_hello → auth_response exchange within it or the connection is
	// dropped. The PRE-upgrade header wait stays generous — see
	// upgradeHeaderTimeout.
	defaultHandshakeTimeout = 5 * time.Second

	// upgradeHeaderTimeout bounds the PRE-upgrade HTTP header wait. It is
	// deliberately much looser than the first-frame deadline: the plugin's
	// WebSocketPeer only writes the upgrade request from its `_process`
	// ticks, so an editor whose frame loop stalls (import scan, debugger
	// break, scheduling hiccup on a loaded host) sends headers seconds
	// after the TCP connect. A tight deadline here turned such stalls into
	// failed dials that burned the plugin's backoff slots, and kept
	// editors missed the `--upgrade-daemon` reconnect grace (RS-021).
	// The listener is loopback-only, so idle-connection exposure is
	// bounded; post-upgrade silence stays capped by HandshakeTimeout.
	upgradeHeaderTimeout = 60 * time.Second

	// maxFrameBytes mirrors the upstream 4 MB max WS frame (screenshot
	// base64 payloads drove the original sizing). Applied only AFTER the
	// v4 auth handshake completes; pre-auth frames stay capped by
	// maxHandshakeFrameBytes (auth.go).
	maxFrameBytes = 4 * 1024 * 1024
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
	// PluginStale marks a handshake accepted with a patch-level version
	// drift: compatible major.minor, unequal patch (e.g. plugin 4.1.0 vs
	// bundled 4.1.1). The session works, but the plugin may miss ops the
	// CLI advertises — status/launch surface it as a warning.
	PluginStale bool
	// Origin is the normalized provenance from the handshake's launched_by
	// field (fork 扩展，见 auth.go authResponse): "cli" for editors the CLI
	// spawned, "user" for everything else (including pure-upstream 4.x
	// plugins that carry no launched_by field).
	Origin string

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

// HandshakeRejection 是一次被拒绝的 v4 握手。daemon 把它留在环形缓冲里，
// 让 CLI/agent 侧能回答「编辑器明明活着、daemon 也活着，为什么 sessions
// 是空的」（需求 handshake-rejection-visibility）：真实现场只有坐在编辑器
// 前的人能在 Godot AI 面板上看到 "Incompatible server"，CLI 侧此前只有
// 一句 no_active_session。
type HandshakeRejection struct {
	At           time.Time `json:"at"`                      // 拒绝时刻（RFC3339）
	Reason       string    `json:"reason"`                  // plugin_version_mismatch / malformed_hello / godot_version_unsupported / nonce_mismatch / proof_failed / duplicate_session / handshake_timeout ...
	PeerVersion  string    `json:"peer_version,omitempty"`  // 对端插件版本（auth_response 携带时）
	Expected     string    `json:"expected,omitempty"`      // 本 daemon 版本（版本类拒绝时）
	SessionID    string    `json:"session_id,omitempty"`    // 对端自报的 session id（可用时）
	EditorPID    int       `json:"editor_pid,omitempty"`    // 对端自报的编辑器 pid（可用时）
	ProjectPath  string    `json:"project_path,omitempty"`  // 对端自报的工程路径（可用时）
	GodotVersion string    `json:"godot_version,omitempty"` // 对端自报的 Godot 版本（可用时）
}

// maxHandshakeRejections 是拒绝记录的环形缓冲容量：足够覆盖「多版本
// CLI 交替试连」的诊断窗口，又不让长活 daemon 无限积累。
const maxHandshakeRejections = 8

// Server is the WebSocket backend the godot_ai plugin connects to.
type Server struct {
	version string

	// WSCapability 是本实例的编辑器通道 capability（64 位小写 hex，
	// 上游 validate_ws_capability 的形状）。daemon 在启动时注入（环境
	// 变量 GODOT_AI_WS_TOKEN 或随机生成并随 capability 记录发布）；
	// NewServer 的默认随机值只服务单测。认证在 handleConn 里完成，
	// 空值等价于"锁定"：所有编辑器连接以 4003 fail-closed。
	WSCapability string

	// HandshakeTimeout bounds the whole v4 auth exchange (default 5s,
	// mirroring upstream DEFAULT_HANDSHAKE_TIMEOUT_SECONDS).
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

	// rejectMu/rejections：握手拒绝环形缓冲。独立于 mu——握手路径绝不
	// 与 session 注册互斥；record 只在关闭连接前发生，RecentRejections
	// 读副本，两者都在 rejectMu 下完成。
	rejectMu   sync.Mutex
	rejections []HandshakeRejection
}

// NewServer builds a Server that reports version in handshake_ack frames.
// Pass pluginmeta.PluginVersion() in production. The WS capability defaults
// to a random per-instance value (production daemons always override it
// with the resolved/published one before Start).
func NewServer(version string) *Server {
	return &Server{
		version:          version,
		WSCapability:     newServerNonce(),
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
	s.http = &http.Server{Handler: mux, ReadHeaderTimeout: upgradeHeaderTimeout}

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

// recordRejection 记一条握手拒绝（环形缓冲，新→旧，容量
// maxHandshakeRejections）。失败形状也记——「something connected and never
// spoke」对排查同样有价值；字段允许稀疏（传输层失败没有对端身份）。
func (s *Server) recordRejection(r HandshakeRejection) {
	r.At = time.Now().UTC()
	s.rejectMu.Lock()
	defer s.rejectMu.Unlock()
	s.rejections = append([]HandshakeRejection{r}, s.rejections...)
	if len(s.rejections) > maxHandshakeRejections {
		s.rejections = s.rejections[:maxHandshakeRejections]
	}
}

// RecentRejections 返回拒绝记录副本（新→旧）。从无拒绝到上限不等；
// 调用方按 nil/空区分「没有拒绝」。
func (s *Server) RecentRejections() []HandshakeRejection {
	s.rejectMu.Lock()
	defer s.rejectMu.Unlock()
	return append([]HandshakeRejection(nil), s.rejections...)
}

// rejectionMaps 把记录转成 JSON-ready 的 map 切片（daemon 端点直接用），
// At 序列化为 RFC3339。
func (s *Server) RejectionMaps() []map[string]any {
	recs := s.RecentRejections()
	out := make([]map[string]any, 0, len(recs))
	for _, r := range recs {
		m := map[string]any{
			"at":     r.At.Format(time.RFC3339),
			"reason": r.Reason,
		}
		if r.PeerVersion != "" {
			m["peer_version"] = r.PeerVersion
		}
		if r.Expected != "" {
			m["expected"] = r.Expected
		}
		if r.SessionID != "" {
			m["session_id"] = r.SessionID
		}
		if r.EditorPID > 0 {
			m["editor_pid"] = r.EditorPID
		}
		if r.ProjectPath != "" {
			m["project_path"] = r.ProjectPath
		}
		if r.GodotVersion != "" {
			m["godot_version"] = r.GodotVersion
		}
		out = append(out, m)
	}
	return out
}

// handshakeRejectionFrom 用 auth_response 里已披露的对端身份填充拒绝记录
// （帧 3 之后所有拒绝路径都能带出 pid/工程/版本；早退路径没有这些字段，
// 记稀疏记录即可）。
func handshakeRejectionFrom(resp *authResponse, reason, expected string) HandshakeRejection {
	return HandshakeRejection{
		Reason:       reason,
		PeerVersion:  resp.PluginVersion,
		Expected:     expected,
		SessionID:    resp.SessionID,
		EditorPID:    resp.EditorPID,
		ProjectPath:  resp.ProjectPath,
		GodotVersion: resp.GodotVersion,
	}
}

// legacyRejectionFrom 处理首帧解析失败：v3 插件（旧 fork 线 3.2.x）的首帧
// 是 type:"handshake"——被拒时在记录里 best-effort 拆出对端身份（peer
// 版本/pid/工程），让「旧插件连新 daemon」的现场在 CLI 侧同样可见（需求
// handshake-rejection-visibility 的原始事故形态）。其它畸形帧记
// malformed_hello。
func legacyRejectionFrom(raw []byte) HandshakeRejection {
	rej := HandshakeRejection{Reason: "malformed_hello"}
	var frame map[string]json.RawMessage
	if err := json.Unmarshal(raw, &frame); err != nil {
		return rej
	}
	var typeStr string
	if err := json.Unmarshal(frame["type"], &typeStr); err != nil || typeStr != "handshake" {
		return rej
	}
	rej.Reason = "legacy_v3_handshake"
	rej.PeerVersion = rawString(frame["plugin_version"])
	rej.SessionID = rawString(frame["session_id"])
	rej.ProjectPath = rawString(frame["project_path"])
	rej.GodotVersion = rawString(frame["godot_version"])
	rej.EditorPID = rawInt(frame["editor_pid"])
	return rej
}

// rawString best-effort 从 RawMessage 里取字符串。
func rawString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// rawInt best-effort 从 RawMessage 里取整数（兼容 JSON number 与数字字符串）。
func rawInt(raw json.RawMessage) int {
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return n
	}
	if s := rawString(raw); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			return v
		}
	}
	return 0
}

// handleConn runs the lifecycle of one plugin connection: the v4
// authenticated handshake (auth_hello → auth_challenge → auth_response →
// handshake_ack, mirrored from upstream transport/websocket.py), then the
// command/response read loop. Any handshake failure closes the connection
// with the mirrored close code — there is no legacy fallback parser.
func (s *Server) handleConn(c *websocket.Conn) {
	// 预认证帧上限 8KB（上游 DEFAULT_MAX_HANDSHAKE_FRAME_BYTES）；
	// 认证通过后放宽到 4MB 截图负载上限。
	c.SetReadLimit(maxHandshakeFrameBytes)

	if !validWSCapability(s.WSCapability) {
		// 上游语义：无 capability 是"锁定"而非"免认证"——fail closed。
		slog.Warn("bridge: rejecting editor connection: no v4 capability configured")
		s.recordRejection(HandshakeRejection{Reason: "server_locked_no_capability"})
		_ = c.Close(closeCodeAuthFailed, "server has no v4 editor capability; restart it from Godot")
		return
	}

	// 整个握手（两帧读 + 一帧写）共用 HandshakeTimeout 窗口。
	deadline := time.Now().Add(s.HandshakeTimeout)
	readFrame := func() ([]byte, error) {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		_, raw, err := c.Read(ctx)
		return raw, err
	}

	// 帧 1：auth_hello（只含 client_nonce，编辑器元数据在服务端证明
	// 自己的 capability 之后才披露——上游设计）。
	rawHello, err := readFrame()
	if err != nil {
		slog.Warn("bridge: auth_hello read failed", "err", err)
		s.recordRejection(HandshakeRejection{Reason: "hello_read_failed"})
		_ = c.Close(closeCodeHandshakePolicy, "v4 editor handshake timed out")
		return
	}
	hello, code, reason := parseAuthHello(rawHello)
	if hello == nil {
		slog.Warn("bridge: rejecting first frame", "code", code, "reason", reason)
		s.recordRejection(legacyRejectionFrom(rawHello))
		_ = c.Close(websocket.StatusCode(code), reason)
		return
	}

	// 帧 2（出站）：auth_challenge——随机 server_nonce + HMAC 服务端证明。
	serverNonce := newServerNonce()
	if serverNonce == "" {
		_ = c.Close(closeCodeAuthFailed, "could not generate v4 server nonce")
		return
	}
	challenge := map[string]any{
		"type":             "auth_challenge",
		"protocol_version": WSProtocolVersion,
		"client_nonce":     hello.ClientNonce,
		"server_nonce":     serverNonce,
		"server_version":   s.version,
		"server_proof":     serverProof(s.WSCapability, hello.ClientNonce, serverNonce, s.version),
	}
	wc := &wsConn{conn: c}
	writeCtx, writeCancel := context.WithDeadline(context.Background(), deadline)
	err = wc.writeJSON(writeCtx, challenge)
	writeCancel()
	if err != nil {
		slog.Warn("bridge: auth_challenge send failed", "err", err)
		s.recordRejection(HandshakeRejection{Reason: "challenge_send_failed"})
		_ = c.CloseNow()
		return
	}

	// 帧 3（入站）：auth_response——编辑器元数据 + 客户端证明。
	rawResp, err := readFrame()
	if err != nil {
		slog.Warn("bridge: auth_response read failed", "err", err)
		s.recordRejection(HandshakeRejection{Reason: "auth_response_read_failed"})
		_ = c.Close(closeCodeHandshakePolicy, "v4 editor handshake timed out")
		return
	}
	resp, code, reason := parseAuthResponse(rawResp)
	if resp == nil {
		slog.Warn("bridge: rejecting auth_response", "code", code, "reason", reason)
		s.recordRejection(HandshakeRejection{Reason: "malformed_auth_response"})
		_ = c.Close(websocket.StatusCode(code), reason)
		return
	}
	if resp.ClientNonce != hello.ClientNonce || resp.ServerNonce != serverNonce {
		s.recordRejection(handshakeRejectionFrom(resp, "nonce_mismatch", ""))
		_ = c.Close(closeCodeAuthFailed, "v4 handshake nonce mismatch")
		return
	}
	// proof 校验：插件带 launched_by（fork 扩展）时按扩展转录验；
	// 纯上游插件无此字段，按上游原样转录验，origin 归一为 "user"。
	expected := clientProofExpectation(s.WSCapability, s.version, resp, resp.HasLaunchedBy)
	if !hmac.Equal([]byte(resp.ClientProof), []byte(expected)) {
		s.recordRejection(handshakeRejectionFrom(resp, "proof_failed", ""))
		_ = c.Close(closeCodeAuthFailed, "v4 editor proof failed")
		return
	}
	if !supportsV4Editor(resp.GodotVersion) {
		s.recordRejection(handshakeRejectionFrom(resp, "godot_version_unsupported", ""))
		_ = c.Close(closeCodeProtocolMismatch,
			"Godot 4.7 or newer within the 4.x line is required by the v4 editor protocol")
		return
	}

	// 认证通过：放宽帧上限到正常负载预算（上游在证明通过后提升
	// max_message_size）。
	c.SetReadLimit(maxFrameBytes)

	// 版本门禁：插件本应在 HTTP 探针阶段就完成 major.minor 评估（fork
	// 放宽），这里只是纵深防御——畸形 plugin_version 拒绝，patch 漂移
	// 接受并标记 stale。拒绝必须留痕（需求 handshake-rejection-visibility）：
	// 「编辑器面板显示 Incompatible server、CLI 侧永远 sessions:[]」的现场
	// 只有拒绝记录能解释。
	stale, rejectReason := s.checkPluginVersion(resp.PluginVersion)
	if rejectReason != "" {
		slog.Warn("bridge: rejecting handshake", "session", resp.SessionID, "reason", rejectReason)
		s.recordRejection(handshakeRejectionFrom(resp, "plugin_version_mismatch", s.version))
		_ = c.Close(websocket.StatusPolicyViolation, rejectReason)
		return
	}

	if !s.registerSession(resp, wc, stale) {
		s.recordRejection(handshakeRejectionFrom(resp, "duplicate_session", ""))
		_ = c.Close(closeCodeDuplicateSession, "session id already registered")
		return
	}

	// handshake_ack：上游形状 + fork 扩展的 stale 提示键。
	ack := handshakeAck{
		Type:            "handshake_ack",
		ProtocolVersion: WSProtocolVersion,
		ServerVersion:   s.version,
	}
	if stale {
		ack.PluginStale = true
		ack.BundledPluginVersion = s.version
	}
	if err := wc.writeJSON(context.Background(), ack); err != nil {
		slog.Warn("bridge: handshake_ack send failed", "session", resp.SessionID, "err", err)
		s.unregisterSession(resp.SessionID)
		_ = c.CloseNow()
		return
	}

	slog.Info("bridge: session connected",
		"session", resp.SessionID, "pid", resp.EditorPID,
		"godot", resp.GodotVersion, "project", resp.ProjectPath)

	// Read loop: no read deadlines and no ping/pong enforcement — during an
	// exclusive test run the editor cannot answer pings, and loopback TCP
	// liveness is enough.
	for {
		_, frame, err := c.Read(context.Background())
		if err != nil {
			break
		}
		s.handleFrame(resp.SessionID, frame)
	}

	slog.Info("bridge: session disconnected", "session", resp.SessionID)
	s.unregisterSession(resp.SessionID)
	_ = c.CloseNow()
}

// checkPluginVersion applies the handshake version gate. It returns
// stale=true for an accepted patch-level drift and a non-empty rejection
// reason when the handshake must be refused (empty reason = accept).
//
// 在 v4 里插件本应在 HTTP 探针阶段就完成版本评估（server_version_check.gd
// 的 fork 补丁把上游严格相等放宽为 major.minor），这道门禁是纵深防御。
// The gate keys on the server's OWN version only when it parses as
// semver: production daemons always run with the vendored plugin.cfg
// version, while tests/dev builds may pass a placeholder — a server that
// cannot name its version accepts any well-formed plugin version.
func (s *Server) checkPluginVersion(pluginVersion string) (stale bool, rejectReason string) {
	pv, err := pluginmeta.ParseSemver(pluginVersion)
	if err != nil {
		return false, fmt.Sprintf("malformed plugin_version %q: the plugin must report a major.minor.patch version", pluginVersion)
	}
	sv, err := pluginmeta.ParseSemver(s.version)
	if err != nil {
		return false, ""
	}
	if !pluginmeta.Compatible(pv, sv) {
		// The close reason rides a WebSocket close frame: 123 bytes max.
		return false, fmt.Sprintf(
			"incompatible plugin version %s (server %s): major.minor must match",
			pluginVersion, s.version)
	}
	return pv != sv, ""
}

// normalizeOrigin maps the handshake's launched_by provenance onto the
// session origin. Only "cli" is trusted; a missing (pure-upstream 4.x
// plugin without the fork extension) or unrecognized value normalizes to
// "user", so a session we cannot identify is never mistaken for a
// CLI-spawned editor.
func normalizeOrigin(launchedBy string) string {
	if launchedBy == "cli" {
		return "cli"
	}
	return "user"
}

// registerSession adds the session and connection to the registry. It
// returns false when the session_id is already registered (duplicate).
// stale carries the version gate's patch-drift verdict onto the session.
func (s *Server) registerSession(resp *authResponse, wc *wsConn, stale bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if _, exists := s.sessions[resp.SessionID]; exists {
		return false
	}
	sess := &Session{
		ID:              resp.SessionID,
		GodotVersion:    resp.GodotVersion,
		ProjectPath:     resp.ProjectPath,
		PluginVersion:   resp.PluginVersion,
		ProtocolVersion: WSProtocolVersion,
		EditorPID:       resp.EditorPID,
		PluginStale:     stale,
		Origin:          normalizeOrigin(resp.LaunchedBy),
		readiness:       resp.Readiness,
	}
	s.sessions[resp.SessionID] = sess
	s.order = append(s.order, resp.SessionID)
	if s.activeID == "" {
		s.activeID = resp.SessionID
	}
	s.conns[resp.SessionID] = wc
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
			data := map[string]any{"retryable": true, "reason": "no_active_session"}
			// 有握手拒绝记录时一并带出（需求 handshake-rejection-visibility
			// §4.1 第 3 点）：「没有已连接编辑器」不再是无信息结论，调用方
			// 能看到「有编辑器在试、但被拒绝及拒绝原因」。
			if rj := s.RejectionMaps(); len(rj) > 0 {
				data["recent_rejections"] = rj
				data["hint"] = "有编辑器尝试连接但被拒绝：见 recent_rejections；磁盘插件版本已对齐时，修法通常是【完全退出并重启编辑器】（reload-plugin 不会替换已加载的插件代码）"
			}
			return nil, &CommandError{
				Code:    "PLUGIN_DISCONNECTED",
				Message: "No Godot editor is connected to this server",
				Data:    data,
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
