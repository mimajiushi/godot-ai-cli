package bridge_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/mimajiushi/godot-ai-cli/internal/testutil/mockplugin"
)

// TestHandshakeRejectionsRecorded：daemon 必须把每次被拒绝的握手留进
// 环形缓冲（需求 handshake-rejection-visibility）——「编辑器面板显示
// Incompatible server、CLI 侧永远 sessions:[]」的现场，CLI 侧此前只有
// 一句 no_active_session；记录必须带对端身份（插件版本/期望版本/pid/
// 工程）与拒绝原因。
func TestHandshakeRejectionsRecorded(t *testing.T) {
	s := startServer(t)

	// minor 不匹配：版本类拒绝必须带 peer/expected 与对端身份。
	mockplugin.DialRejected(t, s.Addr(), s.WSCapability, map[string]any{
		"session_id": "rejrec-minor@0001", "plugin_version": "4.3.0",
		"editor_pid": 5020, "project_path": "D:/games/rpg", "godot_version": "4.7.2.stable.official",
	})
	// major 不匹配（旧 fork 线的 3.2.x）。
	mockplugin.DialRejected(t, s.Addr(), s.WSCapability, map[string]any{
		"session_id": "rejrec-major@0001", "plugin_version": "3.2.13"})
	// 畸形版本。
	mockplugin.DialRejected(t, s.Addr(), s.WSCapability, map[string]any{
		"session_id": "rejrec-garbage@0001", "plugin_version": "garbage"})

	recs := s.RecentRejections()
	if len(recs) != 3 {
		t.Fatalf("rejections = %d, want 3 (newest first): %+v", len(recs), recs)
	}
	// 新→旧顺序。
	if recs[0].Reason != "plugin_version_mismatch" || recs[0].SessionID != "rejrec-garbage@0001" {
		t.Errorf("newest = %+v, want the malformed-version rejection", recs[0])
	}
	// minor 不匹配那条必须带齐身份字段（它在最旧）。
	minor := recs[2]
	if minor.Reason != "plugin_version_mismatch" || minor.PeerVersion != "4.3.0" || minor.Expected != testVersion {
		t.Errorf("minor-mismatch record = %+v", minor)
	}
	if minor.EditorPID != 5020 || minor.ProjectPath != "D:/games/rpg" {
		t.Errorf("minor-mismatch peer identity = %+v", minor)
	}
	// accepted 的握手不产生拒绝记录：成功 dialing 后计数不变。
	p := mockplugin.Dial(t, s.Addr(), s.WSCapability, map[string]any{"plugin_version": testVersion})
	_ = p
	if got := len(s.RecentRejections()); got != 3 {
		t.Errorf("accepted handshake added a rejection record: %d", got)
	}
}

// TestHandshakeRejectionRingCap：缓冲容量是 maxHandshakeRejections（8），
// 第 9 条挤掉最旧的一条。
func TestHandshakeRejectionRingCap(t *testing.T) {
	s := startServer(t)
	for i := 0; i < 12; i++ {
		mockplugin.DialRejected(t, s.Addr(), s.WSCapability, map[string]any{
			"session_id": "ring@0001", "plugin_version": "4.9.0"})
	}
	recs := s.RecentRejections()
	if len(recs) != 8 {
		t.Fatalf("ring cap = %d, want 8", len(recs))
	}
	for _, r := range recs {
		if r.PeerVersion != "4.9.0" || r.Reason != "plugin_version_mismatch" {
			t.Errorf("ring entry = %+v", r)
		}
	}
}

// TestNoActiveSessionCarriesRejections：没有已连接编辑器时，SendCommand
// 的 PLUGIN_DISCONNECTED 必须带上拒绝记录与可操作 hint（需求 §4.1 第 3
// 点：status / session list / scene get-hierarchy 的 no_active_session
// 都应该带这条线索）。
func TestNoActiveSessionCarriesRejections(t *testing.T) {
	s := startServer(t)

	// 无拒绝时保持原形状（data 只有 reason/retryable）。
	_, cmdErr := s.SendCommand(t.Context(), "", "editor_state", map[string]any{}, 0)
	if cmdErr == nil || cmdErr.Code != "PLUGIN_DISCONNECTED" {
		t.Fatalf("err = %v", cmdErr)
	}
	if _, present := cmdErr.Data["recent_rejections"]; present {
		t.Errorf("rejections leaked into a clean no-session error: %v", cmdErr.Data)
	}

	// 一次拒绝后，no_active_session 必须带出记录与 hint。
	mockplugin.DialRejected(t, s.Addr(), s.WSCapability, map[string]any{
		"session_id": "clue@0001", "plugin_version": "4.1.0", "editor_pid": 5020})
	_, cmdErr = s.SendCommand(t.Context(), "", "editor_state", map[string]any{}, 0)
	if cmdErr == nil {
		t.Fatal("expected PLUGIN_DISCONNECTED")
	}
	rj, present := cmdErr.Data["recent_rejections"].([]map[string]any)
	if !present || len(rj) != 1 {
		t.Fatalf("recent_rejections = %v", cmdErr.Data["recent_rejections"])
	}
	if rj[0]["peer_version"] != "4.1.0" || rj[0]["expected"] != testVersion {
		t.Errorf("rejection payload = %v", rj[0])
	}
	hint, _ := cmdErr.Data["hint"].(string)
	if !strings.Contains(hint, "完全退出并重启编辑器") {
		t.Errorf("hint = %q", hint)
	}
}

// TestLegacyV3HandshakeRejectionRecorded：旧 fork 线插件（3.2.x）的首帧是
// type:"handshake"——被拒时记录必须拆出对端身份（peer 版本/pid/工程），
// 这是需求文档 handshake-rejection-visibility 的原始事故形态。
func TestLegacyV3HandshakeRejectionRecorded(t *testing.T) {
	s := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+s.Addr(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	v3 := map[string]any{
		"type": "handshake", "session_id": "v3@0001", "godot_version": "4.7.2.stable.official",
		"plugin_version": "3.2.13", "editor_pid": 5020, "project_path": "D:/games/rpg",
	}
	if err := conn.Write(ctx, websocket.MessageText, mustJSON(t, v3)); err != nil {
		t.Fatalf("write v3 hello: %v", err)
	}
	_, _, err = conn.Read(ctx) // 关闭帧
	if err == nil {
		t.Fatal("expected the v3 peer to be refused")
	}

	recs := s.RecentRejections()
	if len(recs) != 1 {
		t.Fatalf("rejections = %+v", recs)
	}
	r := recs[0]
	if r.Reason != "legacy_v3_handshake" || r.PeerVersion != "3.2.13" {
		t.Errorf("rejection = %+v", r)
	}
	if r.EditorPID != 5020 || r.ProjectPath != "D:/games/rpg" {
		t.Errorf("rejection identity = %+v", r)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
