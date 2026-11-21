package bridge

import (
	"context"
	"time"
)

// Readiness gating for write operations. Mirrors the upstream Python
// semantics in src/godot_ai/handlers/_readiness.py: a cached "ready" /
// "no_scene" passes without a probe, a cached "importing" / "playing" is
// re-probed live (the cache may be stale from a lost event), and only a
// live-confirmed "importing" holds the write for a bounded window before
// rejecting with a retryable EDITOR_NOT_READY.

const (
	// defaultImportingHoldCap mirrors _IMPORTING_HOLD_CAP_SECONDS: fleet
	// telemetry showed nearly every winnable import collision clears within
	// ~8s; past that the wait plateaus and failing fast is correct.
	defaultImportingHoldCap = 8 * time.Second
	// defaultImportingProbeInterval mirrors
	// _IMPORTING_HOLD_PROBE_INTERVAL_SECONDS.
	defaultImportingProbeInterval = 500 * time.Millisecond
	// defaultProbeTimeout bounds one get_editor_state probe round trip.
	defaultProbeTimeout = 2 * time.Second
)

// readinessInfo carries the (message, retryable, hint) tuple for one
// blocking readiness state, mirroring upstream _READINESS_INFO. The hint
// is a one-line, action-oriented sentence surfaced to AI callers so they
// know exactly which tool call (or wait) breaks the stall instead of
// looping the failing write.
type readinessInfo struct {
	message   string
	retryable bool
	hint      string
	subCode   string
}

// blockingReadiness mirrors upstream _READINESS_INFO + _READINESS_SUB_CODE.
var blockingReadiness = map[string]readinessInfo{
	"importing": {
		message:   "Editor is importing resources — try again shortly",
		retryable: true,
		hint: "Editor is importing assets. Wait briefly and retry — " +
			"readiness will update via the response envelope.",
		subCode: "EDITOR_IMPORTING",
	},
	"playing": {
		message:   `Editor is in play mode — run "godot-ai-cli project stop" to stop the game, then retry`,
		retryable: false,
		hint: `Editor is playing the scene. Run "godot-ai-cli project stop" ` +
			"(or wait for the user to stop the game) before retrying writes.",
		subCode: "EDITOR_PLAYING",
	},
}

// knownReadiness mirrors upstream KNOWN_READINESS: every readiness value
// the plugin can emit. Response-envelope stamps outside this set are
// ignored (forward compatibility), never propagated to the cache.
var knownReadiness = map[string]bool{
	"ready":     true,
	"no_scene":  true,
	"importing": true,
	"playing":   true,
}

// RequireWritable checks that a session is in a writable state, with a
// live readiness probe to defeat a stale cache and a bounded hold when the
// editor is mid-import. An empty sessionID targets the active session.
//
// Fast path (cache says "ready" / "no_scene"): no probe, no network. The
// "no_scene" state is allowed through, mirroring upstream — individual
// handlers reject when no scene is open.
//
// Slow path (cache says "importing" / "playing"): fire one
// get_editor_state round trip. If the editor really is busy, the probe
// confirms the cache and the state is enforced; a failed probe enforces
// against the cached value (the caller gets a clean EDITOR_NOT_READY
// instead of a connection error — the write would have failed anyway).
//
// Bounded hold: when the probe confirms "importing", re-probe every
// probeInterval for up to holdCap before rejecting. Only "importing"
// holds — "playing" fails fast because waiting provably does not clear it.
//
// If no session exists this is a no-op; the downstream SendCommand fails
// on its own.
func (s *Server) RequireWritable(ctx context.Context, sessionID string, allowPlayingNoop bool) *CommandError {
	sess := s.resolveSession(sessionID)
	if sess == nil {
		return nil
	}
	if _, blocking := blockingReadiness[sess.Readiness()]; !blocking {
		return nil // cache says writable — fast path, no probe
	}

	probe, probeOK := s.probeReadiness(ctx, sess)
	if probeOK && sess.Readiness() == "importing" {
		// Live-confirmed import in flight — hold instead of bouncing the
		// write back for the caller to blind-retry. A failed initial probe
		// skips the hold entirely: waiting cannot fix a dead link.
		deadline := time.Now().Add(s.importingHoldCap())
		for remaining := time.Until(deadline); remaining > 0; remaining = time.Until(deadline) {
			// Clamp the final sleep so the hold never overshoots the cap;
			// the last re-probe lands exactly at the deadline.
			timer := time.NewTimer(minDuration(s.importingProbeInterval(), remaining))
			select {
			case <-ctx.Done():
				timer.Stop()
				return &CommandError{
					Code:    "TRANSPORT_TIMEOUT",
					Message: "Readiness gate cancelled while holding for the editor import to finish: " + ctx.Err().Error(),
					Data:    map[string]any{"retryable": true},
				}
			case <-timer.C:
			}
			probe, probeOK = s.probeReadiness(ctx, sess)
			if !probeOK {
				break
			}
			if sess.Readiness() != "importing" {
				break
			}
		}
	}

	// 仅 project_run 获得窄例外：必须由最近一次成功实时 probe 明确确认 playing，
	// 缓存中的 playing 或失败 probe 都不能放行；importing 仍走上面的有界等待与拒绝。
	if allowPlayingNoop && probe != nil && probe["readiness"] == "playing" && sess.Readiness() == "playing" {
		return nil
	}
	return enforceBlockingState(sess, probe)
}

// probeReadiness 执行一次 get_editor_state 并用 data.readiness 修复会话缓存。
// 返回的完整 data 供 playing 例外与新鲜 game_status 使用；probe 本身失败时
// 返回 (nil, false)，调用方继续按缓存状态拒绝，而不是把错误升级成连接错误。
func (s *Server) probeReadiness(ctx context.Context, sess *Session) (map[string]any, bool) {
	// 空参数也必须显式为 Dictionary；nil 会编码成 null 并被插件以 INVALID_PARAMS 拒绝。
	data, cmdErr := s.SendCommand(ctx, sess.ID, "get_editor_state", map[string]any{}, s.probeTimeout())
	if cmdErr != nil {
		return nil, false
	}
	if value, ok := data["readiness"].(string); ok {
		s.syncReadiness(sess.ID, value)
	}
	return data, true
}

// enforceBlockingState 按上游规则拒绝阻塞状态下的写入：data 保留
// sub_code/editor_state/retryable/hint，playing 时还携带本次成功 probe
// 返回的 game_status，便于调用方区分真实运行现场与过期缓存。
func enforceBlockingState(sess *Session, probe map[string]any) *CommandError {
	info, blocking := blockingReadiness[sess.Readiness()]
	if !blocking {
		return nil
	}
	data := map[string]any{
		"sub_code":     info.subCode,
		"editor_state": sess.Readiness(),
		"retryable":    info.retryable,
		"hint":         info.hint,
	}
	// playing 拒绝携带本次 probe 的现场状态；绝不复用旧缓存或失败 probe 的空值。
	if sess.Readiness() == "playing" {
		if gameStatus, ok := probe["game_status"].(map[string]any); ok {
			data["game_status"] = gameStatus
		}
	}
	return &CommandError{
		Code:    "EDITOR_NOT_READY",
		Message: info.message,
		Data:    data,
	}
}

// resolveSession maps a (possibly empty) session id to a session. An empty
// id resolves to the active session; unknown ids resolve to nil.
func (s *Server) resolveSession(sessionID string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sessionID == "" {
		sessionID = s.activeID
	}
	return s.sessions[sessionID]
}

// importingHoldCap / importingProbeInterval / probeTimeout expose the
// readiness-gate timing constants as fields so tests can shrink them
// without editing production defaults.

// ImportingHoldCap bounds the total hold while the editor imports.
// Zero selects the 8s default.
func (s *Server) importingHoldCap() time.Duration {
	if s.ImportingHoldCap > 0 {
		return s.ImportingHoldCap
	}
	return defaultImportingHoldCap
}

// ImportingProbeInterval is the delay between hold re-probes.
// Zero selects the 500ms default.
func (s *Server) importingProbeInterval() time.Duration {
	if s.ImportingProbeInterval > 0 {
		return s.ImportingProbeInterval
	}
	return defaultImportingProbeInterval
}

// ProbeTimeout bounds one get_editor_state probe round trip.
// Zero selects the 2s default.
func (s *Server) probeTimeout() time.Duration {
	if s.ProbeTimeout > 0 {
		return s.ProbeTimeout
	}
	return defaultProbeTimeout
}

// minDuration returns the smaller of a and b.
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
