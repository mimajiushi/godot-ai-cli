// Package mockplugin implements the plugin side of the godot_ai WebSocket
// protocol for tests: it dials a bridge.Server, performs the handshake,
// answers commands from a programmable responder (with optional delays to
// model deferred/out-of-order replies), and can push events.
package mockplugin

import (
	"context"
	"encoding/json"
	"fmt"
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

// Dial connects to ws://<addr>, sends the handshake, and reads the
// handshake_ack. Passing nil handshake fields yields sane defaults with a
// unique session id. It fails the test on any error.
func Dial(t *testing.T, addr string, handshake map[string]any) *Plugin {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+addr, nil)
	if err != nil {
		t.Fatalf("mockplugin: dial %s: %v", addr, err)
	}

	if handshake == nil {
		handshake = map[string]any{}
	}
	defaults := map[string]any{
		"type":             "handshake",
		"session_id":       fmt.Sprintf("mock-%d", nextID.Add(1)),
		"godot_version":    "4.7.0",
		"project_path":     "C:/projects/mock",
		"plugin_version":   "3.2.4",
		"protocol_version": 1,
		"readiness":        "ready",
		"editor_pid":       4321,
	}
	for k, v := range defaults {
		if _, ok := handshake[k]; !ok {
			handshake[k] = v
		}
	}
	payload, err := json.Marshal(handshake)
	if err != nil {
		t.Fatalf("mockplugin: marshal handshake: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatalf("mockplugin: send handshake: %v", err)
	}

	_, ackFrame, err := conn.Read(ctx)
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
		Ack:       ack,
		SessionID: handshake["session_id"].(string),
		stamp:     handshake["readiness"].(string),
		loopDone:  make(chan struct{}),
	}
	go p.readLoop()
	t.Cleanup(p.Close)
	return p
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
