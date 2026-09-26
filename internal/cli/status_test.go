package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
	"github.com/mimajiushi/godot-ai-cli/internal/godot"
	"github.com/mimajiushi/godot-ai-cli/internal/testutil/mockplugin"
)

// TestStatusReportsGodotCompatibility drives the status command against a
// real daemon. v4 起版本底线由 WS 认证握手强制执行（<4.7 / 5.x / 不可解析
// 的引擎版本在握手处 4002 拒绝，根本注册不了会话），所以这里钉住两件
// 事：不合底线的版本连不上；合法的 4.7 会话在 status 里干净无警告。
func TestStatusReportsGodotCompatibility(t *testing.T) {
	d, err := daemon.Start(context.Background(), daemon.Config{HTTPPort: 0, WSPort: 0, Version: "test"})
	if err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = d.Shutdown(ctx)
	})

	addr := fmt.Sprintf("127.0.0.1:%d", d.WSPort())
	cap := d.Bridge().WSCapability

	// 底线之外的引擎版本在握手处被拒绝（v4 协议门禁）。
	for _, v := range []string{"4.4.stable.official", "5.1.stable.official", "garbage"} {
		code, _ := mockplugin.DialRejected(t, addr, cap, map[string]any{"godot_version": v})
		if code != 4002 {
			t.Errorf("godot %q: close code = %d, want 4002", v, code)
		}
	}

	mockplugin.Dial(t, addr, cap, map[string]any{"session_id": "ok@0002", "godot_version": "4.7.stable.official"})

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"status", "--http-port", strconv.Itoa(d.HTTPPort())})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status: %v\n%s", err, buf.String())
	}

	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("status output is not JSON: %v\n%s", err, buf.String())
	}
	if out["status"] != "ok" {
		t.Fatalf("out = %v", out)
	}
	sessions, ok := out["sessions"].([]any)
	if !ok || len(sessions) != 1 {
		t.Fatalf("sessions = %v（只有 4.7 会话应注册成功）", out["sessions"])
	}
	sess := sessions[0].(map[string]any)
	if sess["godot_compatible"] != true {
		t.Errorf("4.7 session godot_compatible = %v", sess["godot_compatible"])
	}
	if _, warned := sess["warning"]; warned {
		t.Errorf("4.7 session unexpectedly warns: %v", sess["warning"])
	}
	if _, present := out["warnings"]; present {
		t.Errorf("top-level warnings present for a clean 4.7 session: %v", out["warnings"])
	}
}

// TestStatusReportsOrigin: status surfaces each session's provenance — the
// daemon-reported origin plus, for user-opened editors (including legacy
// plugins without launched_by), a note that a full stop keeps them.
func TestStatusReportsOrigin(t *testing.T) {
	d, err := daemon.Start(context.Background(), daemon.Config{HTTPPort: 0, WSPort: 0, Version: "test"})
	if err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = d.Shutdown(ctx)
	})

	addr := fmt.Sprintf("127.0.0.1:%d", d.WSPort())
	mockplugin.Dial(t, addr, d.Bridge().WSCapability, map[string]any{"session_id": "cli@0001", "launched_by": "cli"})
	mockplugin.Dial(t, addr, d.Bridge().WSCapability, map[string]any{"session_id": "user@0002", "launched_by": "user"})
	mockplugin.Dial(t, addr, d.Bridge().WSCapability, map[string]any{"session_id": "legacy@0003"}) // no launched_by

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"status", "--http-port", strconv.Itoa(d.HTTPPort())})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status: %v\n%s", err, buf.String())
	}

	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("status output is not JSON: %v\n%s", err, buf.String())
	}
	sessions, ok := out["sessions"].([]any)
	if !ok || len(sessions) != 3 {
		t.Fatalf("sessions = %v", out["sessions"])
	}
	byID := make(map[string]map[string]any, len(sessions))
	for _, entry := range sessions {
		s := entry.(map[string]any)
		byID[s["session_id"].(string)] = s
	}

	cli := byID["cli@0001"]
	if cli["origin"] != "cli" {
		t.Errorf("cli session origin = %v, want cli", cli["origin"])
	}
	if _, noted := cli["note"]; noted {
		t.Errorf("cli session unexpectedly noted: %v", cli["note"])
	}
	for _, id := range []string{"user@0002", "legacy@0003"} {
		s := byID[id]
		if s["origin"] != "user" {
			t.Errorf("%s origin = %v, want user", id, s["origin"])
		}
		if note, _ := s["note"].(string); !strings.Contains(note, "stop keeps it") {
			t.Errorf("%s note = %v, want a stop-keeps-it hint", id, s["note"])
		}
	}
}

// TestStatusReportsPluginStale: a session accepted with a patch-level
// plugin version drift gets a per-session note naming both versions and
// the align path; an aligned session gets none.
func TestStatusReportsPluginStale(t *testing.T) {
	d, err := daemon.Start(context.Background(), daemon.Config{HTTPPort: 0, WSPort: 0, Version: "3.2.8"})
	if err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = d.Shutdown(ctx)
	})

	addr := fmt.Sprintf("127.0.0.1:%d", d.WSPort())
	mockplugin.Dial(t, addr, d.Bridge().WSCapability, map[string]any{"session_id": "aligned@0001", "plugin_version": "3.2.8", "launched_by": "cli"})
	mockplugin.Dial(t, addr, d.Bridge().WSCapability, map[string]any{"session_id": "stale@0002", "plugin_version": "3.2.6", "launched_by": "cli"})

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"status", "--http-port", strconv.Itoa(d.HTTPPort())})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status: %v\n%s", err, buf.String())
	}

	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("status output is not JSON: %v\n%s", err, buf.String())
	}
	byID := map[string]map[string]any{}
	for _, entry := range out["sessions"].([]any) {
		s := entry.(map[string]any)
		byID[s["session_id"].(string)] = s
	}

	stale := byID["stale@0002"]
	if stale["plugin_stale"] != true {
		t.Errorf("stale session plugin_stale = %v, want true", stale["plugin_stale"])
	}
	note, _ := stale["note"].(string)
	if !strings.Contains(note, "plugin v3.2.6 < bundled v3.2.8") ||
		!strings.Contains(note, "plugin install --project <dir>") {
		t.Errorf("stale session note = %q, want version drift + align hint", note)
	}

	aligned := byID["aligned@0001"]
	if _, noted := aligned["note"]; noted {
		t.Errorf("aligned session unexpectedly noted: %v", aligned["note"])
	}
}

// TestPluginStaleNote pins the note wording, including the direction sign
// for a plugin NEWER than the daemon's bundled build (a patch-older
// daemon), which must never render as "<".
func TestPluginStaleNote(t *testing.T) {
	if got := pluginStaleNote("3.2.6", "3.2.8"); !strings.Contains(got, "v3.2.6 < bundled v3.2.8") {
		t.Errorf("older plugin note = %q", got)
	}
	if got := pluginStaleNote("3.2.8", "3.2.6"); !strings.Contains(got, "v3.2.8 > bundled v3.2.6") {
		t.Errorf("newer plugin note = %q", got)
	}
	// An unparseable side degrades to a plain inequality sign, never "<".
	if got := pluginStaleNote("garbage", "3.2.8"); !strings.Contains(got, "vgarbage ≠ bundled v3.2.8") {
		t.Errorf("unparseable plugin note = %q", got)
	}
}

// TestStatusNoWarningsWithoutSessions: without sessions the top-level
// warnings field stays absent.
func TestStatusNoWarningsWithoutSessions(t *testing.T) {
	d, err := daemon.Start(context.Background(), daemon.Config{HTTPPort: 0, WSPort: 0, Version: "test"})
	if err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = d.Shutdown(ctx)
	})

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"status", "--http-port", strconv.Itoa(d.HTTPPort())})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status: %v\n%s", err, buf.String())
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("status output is not JSON: %v\n%s", err, buf.String())
	}
	if _, present := out["warnings"]; present {
		t.Errorf("warnings present without any session: %v", out["warnings"])
	}
}

// TestGodotVersionCompatibility pins the classification table without a
// daemon: versions, warning presence, and compatible flag.
func TestGodotVersionCompatibility(t *testing.T) {
	cases := []struct {
		raw         string
		compatible  bool
		wantWarning bool
	}{
		{"4.4.stable.official", false, true},
		{"3.6.stable.official", false, true},
		{"4.5.stable.official", false, true},
		{"4.6.2.stable.mono.official", false, true},
		{"4.7.stable.official", true, false},
		{"5.1.stable.official", false, true},
		{"garbage", true, true},
		{"", true, true},
	}
	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			warning, compatible := godotVersionCompatibility(c.raw)
			if compatible != c.compatible {
				t.Errorf("compatible = %v, want %v (warning %q)", compatible, c.compatible, warning)
			}
			if (warning != "") != c.wantWarning {
				t.Errorf("warning = %q, wantWarning %v", warning, c.wantWarning)
			}
		})
	}
}

// TestStatusKnownDaemons: status lists EVERY recorded daemon, probed live —
// a second running daemon shows running:true with its live version/ports and
// session projects; a dead record shows running:false with the recorded
// identity. The resolved daemon is marked current:true.
func TestStatusKnownDaemons(t *testing.T) {
	dir := stubCacheDir(t)

	// The daemon status resolves to (explicit --http-port).
	d1, err := daemon.Start(context.Background(), daemon.Config{HTTPPort: 0, WSPort: 0, Version: "3.2.9"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d1.Shutdown(context.Background()) })
	writeDaemonRecord(t, dir, d1.HTTPPort(), d1.WSPort(), "3.2.9")

	// A second, older daemon hosting one session for another project.
	d2, err := daemon.Start(context.Background(), daemon.Config{HTTPPort: 0, WSPort: 0, Version: "3.2.8"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d2.Shutdown(context.Background()) })
	writeDaemonRecord(t, dir, d2.HTTPPort(), d2.WSPort(), "3.2.8")
	mockplugin.Dial(t, fmt.Sprintf("127.0.0.1:%d", d2.WSPort()), d2.Bridge().WSCapability, map[string]any{
		"session_id": "other@0001", "project_path": "/other/project/", "plugin_version": "3.2.8",
	})

	// A dead record (crashed daemon leftover).
	writeDaemonRecord(t, dir, 1, 1, "3.2.7")

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"status", "--http-port", strconv.Itoa(d1.HTTPPort())})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status: %v\n%s", err, buf.String())
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("status output is not JSON: %v\n%s", err, buf.String())
	}

	known, ok := out["known_daemons"].([]any)
	if !ok || len(known) != 3 {
		t.Fatalf("known_daemons = %v", out["known_daemons"])
	}
	byPort := map[int]map[string]any{}
	for _, entry := range known {
		e := entry.(map[string]any)
		byPort[int(e["http_port"].(float64))] = e
	}

	cur := byPort[d1.HTTPPort()]
	if cur["running"] != true || cur["current"] != true || cur["version"] != "3.2.9" {
		t.Errorf("current daemon entry = %v", cur)
	}
	if projects, _ := cur["projects"].([]any); len(projects) != 0 {
		t.Errorf("current daemon projects = %v, want empty", cur["projects"])
	}

	old := byPort[d2.HTTPPort()]
	if old["running"] != true || old["version"] != "3.2.8" {
		t.Errorf("second daemon entry = %v", old)
	}
	if _, isCurrent := old["current"]; isCurrent {
		t.Errorf("second daemon must not be marked current: %v", old)
	}
	projects, _ := old["projects"].([]any)
	if len(projects) != 1 || projects[0] != "/other/project/" {
		t.Errorf("second daemon projects = %v", old["projects"])
	}

	dead := byPort[1]
	if dead["running"] != false || dead["version"] != "3.2.7" {
		t.Errorf("dead daemon entry = %v, want running:false with recorded version", dead)
	}
}

// TestStatusPortsOverrideActiveViaProjectFile: with per-project pinning the
// override signal comes from .godot/godot_ai_ports.json on a connected
// session's project — no legacy global backup involved.
func TestStatusPortsOverrideActiveViaProjectFile(t *testing.T) {
	stubCacheDir(t)
	projectDir := t.TempDir()

	d, err := daemon.Start(context.Background(), daemon.Config{HTTPPort: 0, WSPort: 0, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	runStatus := func() map[string]any {
		cmd := NewRootCommand()
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"status", "--http-port", strconv.Itoa(d.HTTPPort())})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("status: %v\n%s", err, buf.String())
		}
		var out map[string]any
		if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
			t.Fatalf("status output is not JSON: %v\n%s", err, buf.String())
		}
		return out
	}

	if out := runStatus(); out["ports_override_active"] != false {
		t.Errorf("ports_override_active = %v without any pin", out["ports_override_active"])
	}

	mockplugin.Dial(t, fmt.Sprintf("127.0.0.1:%d", d.WSPort()), d.Bridge().WSCapability, map[string]any{
		"session_id": "pinned@0001", "project_path": projectDir + "/",
	})
	if err := godot.WriteProjectPorts(projectDir, d.HTTPPort(), d.WSPort()); err != nil {
		t.Fatal(err)
	}
	if out := runStatus(); out["ports_override_active"] != true {
		t.Errorf("ports_override_active = %v with a project port file", out["ports_override_active"])
	}
}

// TestStopClearsProjectPortPins: a full stop deletes the per-project port
// file of each connected session's project (only while it still points at
// the stopped daemon's port) and reports the cleared projects.
func TestStopClearsProjectPortPins(t *testing.T) {
	stubCacheDir(t)
	projectDir := t.TempDir()

	d, err := daemon.Start(context.Background(), daemon.Config{HTTPPort: 0, WSPort: 0, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	// No cleanup shutdown: stop IS the shutdown under test.

	mockplugin.Dial(t, fmt.Sprintf("127.0.0.1:%d", d.WSPort()), d.Bridge().WSCapability, map[string]any{
		"session_id": "pinned@0001", "project_path": projectDir + "/", "launched_by": "user",
	})
	if err := godot.WriteProjectPorts(projectDir, d.HTTPPort(), d.WSPort()); err != nil {
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
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("stop output is not JSON: %v\n%s", err, buf.String())
	}
	cleared, _ := out["project_ports_cleared"].([]any)
	if len(cleared) != 1 || cleared[0] != projectDir+"/" {
		t.Errorf("project_ports_cleared = %v, want [%s/]", cleared, projectDir)
	}
	if godot.HasProjectPorts(projectDir) {
		t.Error("port pin still present after stop")
	}
}

// TestStopKeepsRepinnedPortFile: a port file rewritten by a NEWER launch on
// another daemon's ports must survive the old daemon's stop.
func TestStopKeepsRepinnedPortFile(t *testing.T) {
	stubCacheDir(t)
	projectDir := t.TempDir()

	d, err := daemon.Start(context.Background(), daemon.Config{HTTPPort: 0, WSPort: 0, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	mockplugin.Dial(t, fmt.Sprintf("127.0.0.1:%d", d.WSPort()), d.Bridge().WSCapability, map[string]any{
		"session_id": "pinned@0001", "project_path": projectDir + "/", "launched_by": "user",
	})
	// The pin points at a DIFFERENT daemon now (newer launch elsewhere).
	if err := godot.WriteProjectPorts(projectDir, 29999, 29998); err != nil {
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
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("stop output is not JSON: %v\n%s", err, buf.String())
	}
	if _, present := out["project_ports_cleared"]; present {
		t.Errorf("repinned file must not be reported cleared: %v", out["project_ports_cleared"])
	}
	ports, ok := godot.ReadProjectPorts(projectDir)
	if !ok || ports.HTTPPort != 29999 {
		t.Errorf("repinned port file lost: %+v, %v", ports, ok)
	}
}

// TestStatusFailureShowsLiveDaemons：目标端口无 daemon 时（最常见的裸调用
// 形态）失败载荷也带 known_daemons + live_daemons，且 hint 指向活着的
// 端口而不是 launch——launch 在工程编辑器已开时会双开（需求
// editor-attach-and-daemon-discovery §3.1）。
func TestStatusFailureShowsLiveDaemons(t *testing.T) {
	dir := stubCacheDir(t)
	d := startRecordedDaemon(t, dir, "4.2.5")

	dead := listenFree(t)
	deadPort := dead.Addr().(*net.TCPAddr).Port
	_ = dead.Close()

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"status", "--http-port", strconv.Itoa(deadPort)})
	if err := cmd.Execute(); err == nil {
		t.Fatal("status against a dead port must exit non-zero")
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("status output is not JSON: %v\n%s", err, buf.String())
	}
	if out["status"] != "daemon_not_running" {
		t.Fatalf("status = %v", out["status"])
	}
	known, ok := out["known_daemons"].([]any)
	if !ok || len(known) == 0 {
		t.Fatalf("known_daemons missing on the failure path: %v", out)
	}
	live, ok := out["live_daemons"].([]any)
	if !ok || len(live) != 1 {
		t.Fatalf("live_daemons = %v, want the one live daemon", out["live_daemons"])
	}
	entry := live[0].(map[string]any)
	if int(entry["http_port"].(float64)) != d.HTTPPort() {
		t.Errorf("live daemon port = %v, want %d", entry["http_port"], d.HTTPPort())
	}
	hint, _ := out["hint"].(string)
	if !strings.Contains(hint, strconv.Itoa(d.HTTPPort())) {
		t.Errorf("hint must name the live port: %q", hint)
	}
	if strings.Contains(hint, "launch --project") {
		t.Errorf("hint must NOT suggest launch while a daemon is alive: %q", hint)
	}
	// known_daemons 的每条都要带 version_relation（same/newer/older/unknown）。
	for _, e := range known {
		m := e.(map[string]any)
		if _, ok := m["version_relation"].(string); !ok {
			t.Errorf("known_daemons entry missing version_relation: %v", m)
		}
	}
}

// TestStatusPruneDeletesDeadRecords：--prune 删掉探测死的 daemon-*.json，
// 活记录与 last-daemon.json 保留。
func TestStatusPruneDeletesDeadRecords(t *testing.T) {
	dir := stubCacheDir(t)
	d := startRecordedDaemon(t, dir, "4.2.5")

	dead := listenFree(t)
	deadPort := dead.Addr().(*net.TCPAddr).Port
	_ = dead.Close()
	writeDaemonRecord(t, dir, deadPort, deadPort+1, "3.2.9")

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"status", "--prune", "--http-port", strconv.Itoa(d.HTTPPort())})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --prune: %v\n%s", err, buf.String())
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("status output is not JSON: %v\n%s", err, buf.String())
	}
	pruned, ok := out["pruned"].([]any)
	if !ok || len(pruned) != 1 || int(pruned[0].(float64)) != deadPort {
		t.Errorf("pruned = %v, want [%d]", out["pruned"], deadPort)
	}
	if _, err := os.Stat(filepath.Join(dir, "godot-ai-cli", fmt.Sprintf("daemon-%d.json", deadPort))); !os.IsNotExist(err) {
		t.Errorf("dead record file still present (stat err = %v)", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "godot-ai-cli", fmt.Sprintf("daemon-%d.json", d.HTTPPort()))); err != nil {
		t.Errorf("live record file removed: %v", err)
	}
}

// TestStatusProjectDetectsUnconnectedEditor：status --project 在工程无已连接
// session 但扫描发现其编辑器进程活着时，报 EDITOR_OPEN_UNCONNECTED 并给出
// launch --attach 建议（需求 §3.2 的验收形状）。
func TestStatusProjectDetectsUnconnectedEditor(t *testing.T) {
	dir := stubCacheDir(t)
	d := startRecordedDaemon(t, dir, "4.2.5")

	restore := godot.SetEditorScannerForTest(func() ([]godot.EditorProcess, error) {
		return []godot.EditorProcess{
			{PID: 5020, Project: "D:/games/rpg", GameRunning: &godot.EditorGameProcess{PID: 38004, Scene: "res://test.tscn", Editor: 5020}},
			{PID: 25548, Project: "D:/other/demo"}, // 别的工程：不得误报
		}, nil
	})
	defer restore()

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"status", "--project", "D:\\games\\rpg", "--http-port", strconv.Itoa(d.HTTPPort())})
	if err := cmd.Execute(); err == nil {
		t.Fatal("status --project with an unconnected editor must fail")
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, buf.String())
	}
	if out["status"] != "error" {
		t.Fatalf("status = %v", out["status"])
	}
	env := out["error"].(map[string]any)
	if env["code"] != "EDITOR_OPEN_UNCONNECTED" {
		t.Fatalf("error code = %v", env["code"])
	}
	data := env["data"].(map[string]any)
	editors, ok := data["editors"].([]any)
	if !ok || len(editors) != 1 {
		t.Fatalf("editors = %v", data["editors"])
	}
	ed := editors[0].(map[string]any)
	if int(ed["pid"].(float64)) != 5020 {
		t.Errorf("editor pid = %v", ed["pid"])
	}
	game := ed["game_running"].(map[string]any)
	if int(game["pid"].(float64)) != 38004 {
		t.Errorf("game pid = %v", game["pid"])
	}
	suggest, ok := data["suggest"].([]any)
	if !ok || len(suggest) == 0 || !strings.Contains(suggest[0].(string), "--attach") {
		t.Errorf("suggest = %v", data["suggest"])
	}
}

// TestStatusMergesRecentRejections：活 daemon 上有握手拒绝记录时，status
// 必须合并展示并给可执行 hint（需求 handshake-rejection-visibility §4.1）。
func TestStatusMergesRecentRejections(t *testing.T) {
	dir := stubCacheDir(t)
	d := startRecordedDaemon(t, dir, "4.2.5")

	// 一个 minor 不匹配的插件被拒握手。
	addr := fmt.Sprintf("127.0.0.1:%d", d.WSPort())
	mockplugin.DialRejected(t, addr, d.Bridge().WSCapability, map[string]any{
		"session_id": "strej@0001", "plugin_version": "4.9.0", "editor_pid": 5020,
		"project_path": "D:/games/rpg",
	})

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"status", "--http-port", strconv.Itoa(d.HTTPPort())})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status: %v\n%s", err, buf.String())
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("status output is not JSON: %v\n%s", err, buf.String())
	}
	rj, ok := out["recent_rejections"].([]any)
	if !ok || len(rj) != 1 {
		t.Fatalf("recent_rejections = %v", out["recent_rejections"])
	}
	entry := rj[0].(map[string]any)
	if entry["reason"] != "plugin_version_mismatch" || entry["peer_version"] != "4.9.0" || entry["expected"] != "4.2.5" {
		t.Errorf("rejection entry = %v", entry)
	}
	hint, _ := out["hint"].(string)
	if !strings.Contains(hint, "recent_rejections") {
		t.Errorf("hint = %q", hint)
	}
}

// TestStatusPruneSkipsLastDaemonOnlyPhantom：死端口仅由 last-daemon.json
// 指认（daemon-<port>.json 已不存在）时，--prune 不得把它报成 pruned——
// last-daemon.json 按设计绝不触碰，旧行为会让 pruned 永远非空
// （需求 status-prune-phantom-lastdaemon）。
func TestStatusPruneSkipsLastDaemonOnlyPhantom(t *testing.T) {
	dir := stubCacheDir(t)
	d := startRecordedDaemon(t, dir, "4.2.5")

	// last-daemon.json 指向一个死端口，且该端口没有 daemon-<port>.json。
	dead := listenFree(t)
	deadPort := dead.Addr().(*net.TCPAddr).Port
	_ = dead.Close()
	if err := writeLastDaemon(lastDaemonRecord{HTTPPort: deadPort, WSPort: deadPort + 1}); err != nil {
		t.Fatalf("writeLastDaemon: %v", err)
	}

	// 连续两次 --prune：两次都必须 pruned 为空（旧行为两次都报 [deadPort]）。
	for round := 1; round <= 2; round++ {
		cmd := NewRootCommand()
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"status", "--prune", "--http-port", strconv.Itoa(d.HTTPPort())})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("status --prune round %d: %v\n%s", round, err, buf.String())
		}
		var out map[string]any
		if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
			t.Fatalf("status output is not JSON: %v\n%s", err, buf.String())
		}
		pruned, ok := out["pruned"].([]any)
		if !ok || len(pruned) != 0 {
			t.Errorf("round %d: pruned = %v, want [] (phantom last-daemon port must not be reported)", round, out["pruned"])
		}
	}
	// last-daemon.json 绝不触碰：文件必须原样还在。
	if _, err := os.Stat(lastDaemonPath()); err != nil {
		t.Errorf("last-daemon.json must survive --prune: %v", err)
	}
}

// TestStatusProjectRelativePathDetectsUnconnectedEditor：status --project
// 接受相对路径（与 launch 对齐做 filepath.Abs）——相对路径下未连接编辑器
// 守卫不得静默漏报（需求 status-project-relative-path）。
func TestStatusProjectRelativePathDetectsUnconnectedEditor(t *testing.T) {
	dir := stubCacheDir(t)
	d := startRecordedDaemon(t, dir, "4.2.5")

	// 在临时工程目录里跑，--project 传相对路径 "."；扫描器返回的是绝对路径。
	projectDir := t.TempDir()
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	t.Chdir(projectDir)

	restore := godot.SetEditorScannerForTest(func() ([]godot.EditorProcess, error) {
		return []godot.EditorProcess{
			{PID: 5020, Project: absProject},
		}, nil
	})
	defer restore()

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"status", "--project", ".", "--http-port", strconv.Itoa(d.HTTPPort())})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("status --project <relative> with an unconnected editor must fail\n%s", buf.String())
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, buf.String())
	}
	env, ok := out["error"].(map[string]any)
	if !ok || env["code"] != "EDITOR_OPEN_UNCONNECTED" {
		t.Fatalf("error = %v, want EDITOR_OPEN_UNCONNECTED", out["error"])
	}
}
