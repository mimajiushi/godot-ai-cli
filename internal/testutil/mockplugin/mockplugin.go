// Package mockplugin implements the plugin side of the godot_ai v4
// WebSocket protocol for tests: it dials a bridge.Server, runs the
// authenticated handshake (auth_hello → auth_challenge → auth_response,
// HMAC proofs computed with the server's WS capability), answers commands
// from a programmable responder (with optional delays to model
// deferred/out-of-order replies), and can push events.
package mockplugin

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// nextID generates unique default session ids (Windows timers are too
// coarse for timestamp-based uniqueness).
var nextID atomic.Int64

// Response describes how the mock answers one command. A nil Response
// means "never reply" (timeout testing).
type Response struct {
	Status    string         // "ok" (default when empty) or "error"
	Data      map[string]any // response payload
	Error     map[string]any // error detail: {"code","message","data"}
	Readiness string         // envelope readiness stamp; empty = omit
	Delay     time.Duration  // reply latency, modeling deferred responses
}

// Responder builds the reply for one incoming command.
type Responder func(command string, params map[string]any) *Response

// ReceivedCommand records one command frame the mock got from the server.
type ReceivedCommand struct {
	Command string
	Params  map[string]any
}

// Plugin is one connected mock editor.
type Plugin struct {
	t         *testing.T
	Conn      *websocket.Conn
	Ack       map[string]any // the handshake_ack frame from the server
	SessionID string         // the id this mock registered with

	mu        sync.Mutex
	responder Responder
	stamp     string
	received  []ReceivedCommand
	writeMu   sync.Mutex
	closed    bool
	loopDone  chan struct{}
}

// --- v4 握手证明的客户端镜像（与 internal/bridge/auth.go 同算法） ---

// proofMessage 与 bridge.proofMessage 同格式：domain + "\n<len>:<值>"。
func proofMessage(domain string, values ...string) []byte {
	msg := []byte(domain)
	for _, v := range values {
		enc := []byte(v)
		msg = append(msg, '\n')
		msg = append(msg, strconv.Itoa(len(enc))...)
		msg = append(msg, ':')
		msg = append(msg, enc...)
	}
	return msg
}

func hmacProof(capability, domain string, values ...string) string {
	mac := hmac.New(sha256.New, []byte(capability))
	mac.Write(proofMessage(domain, values...))
	return hex.EncodeToString(mac.Sum(nil))
}

// Dial connects to ws://<addr> and runs the v4 authenticated handshake
// using the server's WS capability. fields overrides auth_response fields;
// nil yields sane defaults with a unique session id. It fails the test on
// any error (including a failed server-proof check — the mock verifies the
// server exactly like the real plugin does).
//
// 默认不携带 launched_by（纯上游插件形状，origin 归一为 "user"）；
// 传 "launched_by": "cli" 模拟 CLI spawn 的编辑器（走 fork 扩展转录）。
// 传 "client_proof": "<64hex>" 可注入伪造证明（认证失败路径测试）。
func Dial(t *testing.T, addr, capability string, fields map[string]any) *Plugin {
	t.Helper()

	conn, resp := dialAndAuth(t, addr, capability, fields)

	_, ackFrame, err := conn.Read(context.Background())
	if err != nil {
		t.Fatalf("mockplugin: read handshake_ack: %v", err)
	}
	var ack map[string]any
	if err := json.Unmarshal(ackFrame, &ack); err != nil {
		t.Fatalf("mockplugin: parse handshake_ack: %v", err)
	}
	p := &Plugin{
		t:         t,
		Conn:      conn,
		SessionID: resp["session_id"].(string),
		stamp:     resp["readiness"].(string),
		Ack:       ack,
		loopDone:  make(chan struct{}),
	}

	go p.readLoop()
	t.Cleanup(p.Close)
	return p
}

// DialRejected 跑完整握手（可被 "client_proof" 注入伪造）并断言服务端
// 不发 handshake_ack 直接关闭（认证/协议拒绝路径）。返回关闭码与原因。
func DialRejected(t *testing.T, addr, capability string, fields map[string]any) (int, string) {
	t.Helper()

	conn, _ := dialAndAuth(t, addr, capability, fields)
	t.Cleanup(func() { _ = conn.CloseNow() })

	_, _, err := conn.Read(context.Background())
	if err == nil {
		t.Fatal("mockplugin: expected rejection, got handshake_ack")
	}
	return closeDetails(t, err)
}

// DialRaw 发送任意第一帧并返回服务端关闭码与原因（v3 插件/畸形帧测试）。
func DialRaw(t *testing.T, addr string, payload []byte) (int, string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+addr, nil)
	if err != nil {
		t.Fatalf("mockplugin: dial %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatalf("mockplugin: send raw frame: %v", err)
	}
	_, _, err = conn.Read(context.Background())
	if err == nil {
		t.Fatal("mockplugin: expected rejection, got a frame")
	}
	return closeDetails(t, err)
}

// closeDetails 提取 websocket 关闭码与原因。
func closeDetails(t *testing.T, err error) (int, string) {
	t.Helper()
	var cerr websocket.CloseError
	if errors.As(err, &cerr) {
		return int(cerr.Code), cerr.Reason
	}
	t.Fatalf("mockplugin: rejection read error is not a close frame: %v", err)
	return 0, ""
}

// dialAndAuth 拨号并完成到 auth_response 发送为止的握手段，返回连接与
// 生效的 auth_response 字段表（默认值已填充）。
func dialAndAuth(t *testing.T, addr, capability string, fields map[string]any) (*websocket.Conn, map[string]any) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+addr, nil)
	if err != nil {
		t.Fatalf("mockplugin: dial %s: %v", addr, err)
	}

	// 帧 1：auth_hello（64hex 随机 client_nonce）。
	var nonceBuf [32]byte
	if _, err := rand.Read(nonceBuf[:]); err != nil {
		t.Fatalf("mockplugin: nonce: %v", err)
	}
	clientNonce := hex.EncodeToString(nonceBuf[:])
	hello, _ := json.Marshal(map[string]any{
		"type":             "auth_hello",
		"protocol_version": 2,
		"client_nonce":     clientNonce,
	})
	if err := conn.Write(ctx, websocket.MessageText, hello); err != nil {
		t.Fatalf("mockplugin: send auth_hello: %v", err)
	}

	// 帧 2（入站）：auth_challenge——像真插件一样验证服务端证明。
	_, chalFrame, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("mockplugin: read auth_challenge: %v", err)
	}
	var chal map[string]any
	if err := json.Unmarshal(chalFrame, &chal); err != nil {
		t.Fatalf("mockplugin: parse auth_challenge: %v", err)
	}
	if chal["type"] != "auth_challenge" {
		t.Fatalf("mockplugin: expected auth_challenge, got %s", chalFrame)
	}
	serverNonce, _ := chal["server_nonce"].(string)
	serverVersion, _ := chal["server_version"].(string)
	serverProof, _ := chal["server_proof"].(string)
	// "skip_server_proof_check": true 时跳过服务端证明校验——测试用
	// 错误 capability 走到服务端的客户端证明拒绝（真实插件会自己
	// fail-closed，这里是要验服务端的 4003）。
	skipServerCheck := fields != nil && fields["skip_server_proof_check"] == true
	expectedServer := hmacProof(capability, "godot-ai-ws-v2/server-proof",
		"2", clientNonce, serverNonce, serverVersion)
	if !skipServerCheck && !hmac.Equal([]byte(serverProof), []byte(expectedServer)) {
		t.Fatalf("mockplugin: server proof mismatch (wrong capability?)")
	}

	// 帧 3：auth_response（默认纯上游形状；launched_by 触发 fork 转录）。
	if fields == nil {
		fields = map[string]any{}
	}
	resp := map[string]any{
		"type":               "auth_response",
		"protocol_version":   2,
		"client_nonce":       clientNonce,
		"server_nonce":       serverNonce,
		"session_id":         fmt.Sprintf("mock-%d", nextID.Add(1)),
		"godot_version":      "4.7-stable (official)",
		"project_path":       "C:/projects/mock",
		"plugin_version":     "4.1.0",
		"readiness":          "ready",
		"editor_pid":         4321,
		"server_launch_mode": "manual",
	}
	for k, v := range fields {
		if k == "skip_server_proof_check" {
			continue // 控制键，不上线
		}
		resp[k] = v
	}
	if _, forged := resp["client_proof"]; !forged {
		values := []string{
			"2", clientNonce, serverNonce, serverVersion,
			resp["session_id"].(string), resp["godot_version"].(string),
			resp["project_path"].(string), resp["plugin_version"].(string),
			resp["readiness"].(string),
			strconv.Itoa(resp["editor_pid"].(int)),
			resp["server_launch_mode"].(string),
		}
		if lb, ok := resp["launched_by"].(string); ok {
			values = append(values, lb)
		}
		resp["client_proof"] = hmacProof(capability, "godot-ai-ws-v2/client-proof", values...)
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("mockplugin: marshal auth_response: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatalf("mockplugin: send auth_response: %v", err)
	}
	return conn, resp
}

// SetResponder installs the command responder.
func (p *Plugin) SetResponder(r Responder) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.responder = r
}

// SetReadinessStamp changes the readiness stamped onto later responses.
func (p *Plugin) SetReadinessStamp(value string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stamp = value
}

// PushEvent sends one event frame to the server.
func (p *Plugin) PushEvent(event string, data map[string]any) {
	p.t.Helper()
	frame, err := json.Marshal(map[string]any{
		"type":  "event",
		"event": event,
		"data":  data,
	})
	if err != nil {
		p.t.Fatalf("mockplugin: marshal event: %v", err)
	}
	p.write(frame)
}

// Received returns the commands the mock has seen so far.
func (p *Plugin) Received() []ReceivedCommand {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]ReceivedCommand(nil), p.received...)
}

// Count returns how many frames of the given command arrived.
func (p *Plugin) Count(command string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, c := range p.received {
		if c.Command == command {
			n++
		}
	}
	return n
}

// Close terminates the connection and waits for the read loop to exit.
func (p *Plugin) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()
	_ = p.Conn.Close(websocket.StatusNormalClosure, "test done")
	<-p.loopDone
}

// readLoop answers command frames from the server until disconnect.
func (p *Plugin) readLoop() {
	defer close(p.loopDone)
	for {
		_, frame, err := p.Conn.Read(context.Background())
		if err != nil {
			return
		}
		var req struct {
			RequestID string         `json:"request_id"`
			Command   string         `json:"command"`
			Params    map[string]any `json:"params"`
		}
		if err := json.Unmarshal(frame, &req); err != nil || req.RequestID == "" {
			continue
		}
		p.mu.Lock()
		p.received = append(p.received, ReceivedCommand{Command: req.Command, Params: req.Params})
		responder := p.responder
		stamp := p.stamp
		p.mu.Unlock()

		// Each reply is built on its own goroutine so per-command delays
		// model deferred and out-of-order plugin responses.
		go func() {
			if responder == nil {
				return // no reply: exercises the server-side timeout path
			}
			resp := responder(req.Command, req.Params)
			if resp == nil {
				return
			}
			if resp.Delay > 0 {
				time.Sleep(resp.Delay)
			}
			status := resp.Status
			if status == "" {
				status = "ok"
			}
			out := map[string]any{
				"request_id": req.RequestID,
				"status":     status,
				"data":       resp.Data,
			}
			if resp.Error != nil {
				out["error"] = resp.Error
			}
			if resp.Readiness != "" {
				out["readiness"] = resp.Readiness
			} else if stamp != "" {
				out["readiness"] = stamp
			}
			payload, err := json.Marshal(out)
			if err != nil {
				return
			}
			p.write(payload)
		}()
	}
}

// write sends one frame with serialized access. Errors are ignored: a
// late reply to a disconnected server is expected during teardown.
func (p *Plugin) write(frame []byte) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = p.Conn.Write(ctx, websocket.MessageText, frame)
}
