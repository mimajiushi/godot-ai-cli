// Package bridge implements the WebSocket server side of the godot_ai
// editor-plugin protocol.
//
// The godot_ai GDScript editor plugin is a WebSocket CLIENT that dials
// 127.0.0.1:9500 (default). This package is the server it talks to:
// it accepts the plugin handshake, tracks editor sessions, routes
// request/response command envelopes, and caches per-session readiness.
//
// Wire shapes (JSON) are mirrored from the upstream Python backend
// (src/godot_ai/transport/websocket.py, protocol/envelope.py).
package bridge

// Handshake is the first frame a plugin sends after connecting.
// Mirrors upstream protocol/envelope.py HandshakeMessage.
type Handshake struct {
	Type            string `json:"type"`
	SessionID       string `json:"session_id"`
	GodotVersion    string `json:"godot_version"`
	ProjectPath     string `json:"project_path"`
	PluginVersion   string `json:"plugin_version"`
	ProtocolVersion int    `json:"protocol_version"`
	Readiness       string `json:"readiness"`
	EditorPID       int    `json:"editor_pid"`
	// ServerLaunchMode and AuthToken are accepted for wire compatibility;
	// the token is ignored (loopback-only trust boundary, compat gate).
	ServerLaunchMode string `json:"server_launch_mode"`
	AuthToken        string `json:"auth_token,omitempty"`
}

// handshakeAck is the server's answer to a valid Handshake. ServerVersion
// MUST equal the plugin.cfg version: the plugin rejects the server on a
// strict equality mismatch.
type handshakeAck struct {
	Type          string `json:"type"` // always "handshake_ack"
	ServerVersion string `json:"server_version"`
}

// eventFrame is a state event pushed by the plugin at any time.
type eventFrame struct {
	Type  string         `json:"type"`  // always "event"
	Event string         `json:"event"` // scene_changed | play_state_changed | readiness_changed | ...
	Data  map[string]any `json:"data"`
}

// commandRequest is a command sent from the server to the plugin.
type commandRequest struct {
	RequestID string         `json:"request_id"` // uuid4 hex without dashes
	Command   string         `json:"command"`
	Params    map[string]any `json:"params"`
}

// CommandResponse is the plugin's reply to a commandRequest. Responses may
// arrive out of order or deferred (after the command completes in-editor);
// they are correlated purely by RequestID.
type CommandResponse struct {
	RequestID string         `json:"request_id"`
	Status    string         `json:"status"` // "ok" | "error"
	Data      map[string]any `json:"data"`
	Error     *ErrorDetail   `json:"error"`
	// Readiness is a live snapshot stamped by the plugin's dispatcher onto
	// every response. It heals the cached session readiness even if a
	// readiness_changed event was lost in transit. Older plugins omit it.
	Readiness string `json:"readiness"`
	// ErrorWatermark carries monotonic per-component counters. Tracked for
	// wire compatibility only; the CLI does not consume them yet.
	ErrorWatermark map[string]any `json:"error_watermark"`
}

// ErrorDetail is the structured error payload inside an error response.
type ErrorDetail struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data"`
}

// CommandError is a structured command failure, either produced locally
// (transport timeout, readiness gate, invalid params) or mirrored from a
// plugin error response.
type CommandError struct {
	Code    string
	Message string
	Data    map[string]any
}

// Error implements the error interface.
func (e *CommandError) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

// ToJSON renders the error as the protocol error body:
// {"status":"error","error":{code,message,data}}.
func (e *CommandError) ToJSON() map[string]any {
	data := e.Data
	if data == nil {
		data = map[string]any{}
	}
	return map[string]any{
		"status": "error",
		"error": map[string]any{
			"code":    e.Code,
			"message": e.Message,
			"data":    data,
		},
	}
}
