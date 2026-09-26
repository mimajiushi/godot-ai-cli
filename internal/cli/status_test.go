package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
	"github.com/mimajiushi/godot-ai-cli/internal/godot"
	"github.com/mimajiushi/godot-ai-cli/internal/testutil/mockplugin"
	"github.com/mimajiushi/godot-ai-cli/internal/version"
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
	// 插件比 daemon 旧 → 陈旧侧是插件，建议命令为「装插件 + 完全重启编辑器」
	// （需求 R-5 的 stale_side / suggested_commands）；daemon 内置版本与
	// plugin_version 并列结构化输出（F-5），消费方不必从 note 文本反推。
	if stale["stale_side"] != "plugin" {
		t.Errorf("stale_side = %v, want plugin", stale["stale_side"])
	}
	if stale["daemon_bundled_version"] != "3.2.8" {
		t.Errorf("daemon_bundled_version = %v, want the daemon-reported 3.2.8", stale["daemon_bundled_version"])
	}
	cmds, _ := stale["suggested_commands"].([]any)
	if len(cmds) != 2 || !strings.Contains(cmds[0].(string), "plugin install --project <dir>") ||
		!strings.Contains(cmds[1].(string), "完全退出") {
		t.Errorf("stale session suggested_commands = %v, want install + editor restart", stale["suggested_commands"])
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

// TestStatusReportsStaleSideDaemon：需求 R-5 现场方向——插件比 daemon 的
// 内置版本新（用户刚升级 CLI/插件，daemon 还是升级前那个进程）时，陈旧侧
// 是 daemon：note 与 suggested_commands 必须指向 `launch --upgrade-daemon`
// （`plugin install` 此刻装无可装，按它执行是空转 + 白重开一次编辑器）。
func TestStatusReportsStaleSideDaemon(t *testing.T) {
	d, err := daemon.Start(context.Background(), daemon.Config{HTTPPort: 0, WSPort: 0, Version: "3.2.6"})
	if err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = d.Shutdown(ctx)
	})

	addr := fmt.Sprintf("127.0.0.1:%d", d.WSPort())
	mockplugin.Dial(t, addr, d.Bridge().WSCapability, map[string]any{
		"session_id": "daemonstale@0001", "plugin_version": "3.2.8", "launched_by": "cli",
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
	sessions, _ := out["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %v", out["sessions"])
	}
	sess := sessions[0].(map[string]any)
	if sess["plugin_stale"] != true {
		t.Fatalf("plugin_stale = %v, want the patch drift to be accepted+flagged", sess["plugin_stale"])
	}
	if sess["stale_side"] != "daemon" {
		t.Errorf("stale_side = %v, want daemon", sess["stale_side"])
	}
	note, _ := sess["note"].(string)
	if !strings.Contains(note, "plugin v3.2.8 > bundled v3.2.6") ||
		!strings.Contains(note, "launch --project <dir> --upgrade-daemon") {
		t.Errorf("note = %q, want the newer-plugin sign + the --upgrade-daemon remedy", note)
	}
	cmds, _ := sess["suggested_commands"].([]any)
	if len(cmds) != 1 || !strings.Contains(cmds[0].(string), "launch --project <dir> --upgrade-daemon") {
		t.Errorf("suggested_commands = %v, want the daemon swap", sess["suggested_commands"])
	}
	// F-5：daemon 侧陈旧时，daemon 内置版本必须与 plugin_version 并列结构化
	// 输出——否则消费方只能去 note 文本或 status.daemon.version（「本端口
	// 解析到的 daemon」语义）里反推。
	if sess["daemon_bundled_version"] != "3.2.6" {
		t.Errorf("daemon_bundled_version = %v, want the daemon-reported 3.2.6", sess["daemon_bundled_version"])
	}
	if sess["plugin_version"] != "3.2.8" {
		t.Errorf("plugin_version = %v, want the session-reported 3.2.8", sess["plugin_version"])
	}
}

// TestPluginStaleHintDirections 钉住 pluginStaleHint 的方向判定与建议命令
// （status 会话 note 与 launch 警告共用它，方向错了就会出现「装无可装」的
// 空转修法，需求 R-5 现场）。
func TestPluginStaleHintDirections(t *testing.T) {
	note, side, cmds := pluginStaleHint("3.2.8", "3.2.6")
	if side != "daemon" {
		t.Errorf("newer plugin: side = %q, want daemon", side)
	}
	if !strings.Contains(note, "v3.2.8 > bundled v3.2.6") ||
		!strings.Contains(note, "launch --project <dir> --upgrade-daemon") {
		t.Errorf("newer plugin: note = %q", note)
	}
	if len(cmds) != 1 || cmds[0] != "godot-ai-cli launch --project <dir> --upgrade-daemon" {
		t.Errorf("newer plugin: commands = %v", cmds)
	}

	note, side, cmds = pluginStaleHint("3.2.6", "3.2.8")
	if side != "plugin" {
		t.Errorf("older plugin: side = %q, want plugin", side)
	}
	if !strings.Contains(note, "v3.2.6 < bundled v3.2.8") ||
		!strings.Contains(note, "plugin install --project <dir>") {
		t.Errorf("older plugin: note = %q", note)
	}
	if len(cmds) != 2 || cmds[0] != "godot-ai-cli plugin install --project <dir>" ||
		cmds[1] != "完全退出并重新打开编辑器" {
		t.Errorf("older plugin: commands = %v", cmds)
	}

	// 任一侧无法解析 → 不给方向、不给建议（未知方向绝不猜），note 保持
	// 中性文案。
	for _, c := range [][2]string{{"garbage", "3.2.8"}, {"3.2.8", "garbage"}, {"4.2.5-dev", "3.2.8"}} {
		note, side, cmds := pluginStaleHint(c[0], c[1])
		if side != "" || cmds != nil {
			t.Errorf("unparseable %v: side = %q, commands = %v, want none", c, side, cmds)
		}
		if !strings.Contains(note, "≠ bundled v") || !strings.Contains(note, "plugin install --project <dir>") {
			t.Errorf("unparseable %v: note = %q, want the neutral wording", c, note)
		}
	}
}

// TestEnrichSessionsStaleSideOmittedWhenUncomparable：两侧版本无法比较时
// enrichSessions 只出 note，stale_side / suggested_commands /
// daemon_bundled_version 三个键必须缺席（而不是给出可能误导的方向或半截
// 版本信息），且不能报错（需求 R-5 的降级要求 + F-5 的同策略省略）。
func TestEnrichSessionsStaleSideOmittedWhenUncomparable(t *testing.T) {
	raw := []any{
		map[string]any{"session_id": "x@0001", "plugin_stale": true, "plugin_version": "garbage",
			"godot_version": "4.7.stable.official"},
		map[string]any{"session_id": "x@0002", "plugin_stale": true, "plugin_version": "3.2.6",
			"godot_version": "4.7.stable.official"},
	}
	// 第二个会话的 bundled 版本缺失（旧 daemon 不上报）：同样无法比较。
	out, warnings := enrichSessions(raw, "garbage")
	sessions, _ := out.([]any)
	if len(sessions) != 2 {
		t.Fatalf("sessions = %v", sessions)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none (degradation must not be an error)", warnings)
	}
	for _, entry := range sessions {
		sess := entry.(map[string]any)
		note, _ := sess["note"].(string)
		if !strings.Contains(note, "bundled v") {
			t.Errorf("session %v note = %q, want the neutral note", sess["session_id"], note)
		}
		if _, present := sess["stale_side"]; present {
			t.Errorf("session %v carries stale_side %v despite uncomparable versions", sess["session_id"], sess["stale_side"])
		}
		if _, present := sess["suggested_commands"]; present {
			t.Errorf("session %v carries suggested_commands %v despite uncomparable versions", sess["session_id"], sess["suggested_commands"])
		}
		// F-5：daemon_bundled_version 与 stale_side 同一策略——无法比较就
		// 一并省略，不输出无法解释的版本号。
		if _, present := sess["daemon_bundled_version"]; present {
			t.Errorf("session %v carries daemon_bundled_version %v despite uncomparable versions", sess["session_id"], sess["daemon_bundled_version"])
		}
	}
}

// TestKnownDaemonsCLIVersion：daemon 记录里的 cli_version 在两种路径上都
// 透出（活 daemon 与死记录都以记录为准——「这个 daemon 是哪个 CLI 起的、
// 要不要重启」只有记录知道，需求 R-5 附带）；旧记录无该字段时省略该键，
// 而不是报错或缺字段即崩。
func TestKnownDaemonsCLIVersion(t *testing.T) {
	dir := stubCacheDir(t)

	d, err := daemon.Start(context.Background(), daemon.Config{HTTPPort: 0, WSPort: 0, Version: "4.2.4"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })
	writeDaemonRecord(t, dir, d.HTTPPort(), d.WSPort(), "4.2.4", "0.2.0-beta.1")

	// 死记录：一个有 cli_version，一个是旧形态（无该字段）。
	deadWithCLI := listenFree(t)
	deadWithCLIPort := deadWithCLI.Addr().(*net.TCPAddr).Port
	_ = deadWithCLI.Close()
	writeDaemonRecord(t, dir, deadWithCLIPort, deadWithCLIPort+1, "4.2.3", "0.1.0")

	deadOld := listenFree(t)
	deadOldPort := deadOld.Addr().(*net.TCPAddr).Port
	_ = deadOld.Close()
	writeDaemonRecord(t, dir, deadOldPort, deadOldPort+1, "4.2.2")

	byPort := map[int]map[string]any{}
	for _, entry := range knownDaemonsReport(d.HTTPPort()) {
		e := entry.(map[string]any)
		// knownDaemonsReport 的 map 里 http_port 是 int（未经 JSON 往返）。
		port, _ := e["http_port"].(int)
		byPort[port] = e
	}
	if len(byPort) != 3 {
		t.Fatalf("known daemons = %v", byPort)
	}
	if live := byPort[d.HTTPPort()]; live["running"] != true || live["cli_version"] != "0.2.0-beta.1" {
		t.Errorf("live entry = %v, want running with the recorded cli_version", live)
	}
	if dead := byPort[deadWithCLIPort]; dead["running"] != false || dead["cli_version"] != "0.1.0" {
		t.Errorf("dead entry = %v, want the recorded cli_version next to running:false", dead)
	}
	if old := byPort[deadOldPort]; old["running"] != false {
		t.Errorf("dead old-form entry = %v", old)
	} else if _, present := old["cli_version"]; present {
		t.Errorf("a record without cli_version must omit the key, got %v", old["cli_version"])
	}
}

// TestStatusDaemonBlockCLIVersion：status.daemon 区块在记录可查时补
// cli_version；旧记录（无该字段）或没有记录时省略该键（需求 R-5 附带）。
func TestStatusDaemonBlockCLIVersion(t *testing.T) {
	dir := stubCacheDir(t)
	d, err := daemon.Start(context.Background(), daemon.Config{HTTPPort: 0, WSPort: 0, Version: "4.2.5"})
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

	// 旧形态记录：daemon 区块不得凭空补一个 cli_version。
	writeDaemonRecord(t, dir, d.HTTPPort(), d.WSPort(), "4.2.5")
	daemonInfo, _ := runStatus()["daemon"].(map[string]any)
	if _, present := daemonInfo["cli_version"]; present {
		t.Errorf("old-form record: daemon.cli_version = %v, want the key absent", daemonInfo["cli_version"])
	}

	// 记录里有：区块照实透出（值以记录为准）。
	writeDaemonRecord(t, dir, d.HTTPPort(), d.WSPort(), "4.2.5", "0.2.0-beta.1")
	daemonInfo, _ = runStatus()["daemon"].(map[string]any)
	if daemonInfo["cli_version"] != "0.2.0-beta.1" {
		t.Errorf("daemon.cli_version = %v, want the recorded CLI version", daemonInfo["cli_version"])
	}
	if daemonInfo["version"] != "4.2.5" {
		t.Errorf("daemon.version = %v, want the daemon-reported plugin version", daemonInfo["version"])
	}
}

// TestRecordDaemonCLIVersion：daemon 记录里的 cli_version 由**启动它的 CLI**
// 补写（daemon 自己只知道内置插件版本，说不出「是哪个 CLI 起的」，需求 R-5
// 附带）。补写必须保留记录里 daemon 自己写的全部字段；记录缺失/畸形时静默
// 跳过——记录是提示而不是错误源，旧记录因此保持「无该字段」形态。
func TestRecordDaemonCLIVersion(t *testing.T) {
	dir := stubCacheDir(t)
	httpPort, wsPort := 18301, 19301
	writeDaemonRecord(t, dir, httpPort, wsPort, "4.2.4") // 旧形态：无 cli_version

	recordDaemonCLIVersion(httpPort)

	path := filepath.Join(dir, "godot-ai-cli", fmt.Sprintf("daemon-%d.json", httpPort))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("record file missing after the stamp: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("record is not JSON: %v\n%s", err, raw)
	}
	if rec["cli_version"] != version.Version {
		t.Errorf("cli_version = %v, want this binary's %q", rec["cli_version"], version.Version)
	}
	if rec["version"] != "4.2.4" || rec["pid"] != float64(1234) ||
		int(rec["http_port"].(float64)) != httpPort || int(rec["ws_port"].(float64)) != wsPort ||
		rec["started_at"] != "2026-09-07T00:00:00Z" {
		t.Errorf("the stamp must preserve every field the daemon wrote, got %v", rec)
	}

	// 幂等：同样的值不再回写。
	recordDaemonCLIVersion(httpPort)
	if again, _ := os.ReadFile(path); string(again) != string(raw) {
		t.Errorf("second stamp changed the record:\n%s\n%s", raw, again)
	}

	// 没有记录：绝不凭空造文件。
	recordDaemonCLIVersion(httpPort + 1)
	if _, err := os.Stat(filepath.Join(dir, "godot-ai-cli", fmt.Sprintf("daemon-%d.json", httpPort+1))); !os.IsNotExist(err) {
		t.Errorf("a missing record must not be created (stat err = %v)", err)
	}

	// 畸形记录：原样保留，不报错。
	bad := filepath.Join(dir, "godot-ai-cli", fmt.Sprintf("daemon-%d.json", httpPort+2))
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	recordDaemonCLIVersion(httpPort + 2)
	if got, _ := os.ReadFile(bad); string(got) != "{not json" {
		t.Errorf("corrupt record must be left alone, got %s", got)
	}
}

// TestLaunchForegroundStampsCLIVersion：foreground launch 的 daemon 就在本进程
// 内，记录里的 cli_version 由它补写（需求 R-5 附带：记录里的 version 只是内置
// 插件版本）。只给**自己起的** daemon 写——收养别人起的 daemon 回写会把「旧版
// CLI 起的」谎报成本版，恰恰破坏该字段的用途。detached 路径由 spawn 出去的
// serve 自己补写，见 TestServeWritesLastDaemon。
func TestLaunchForegroundStampsCLIVersion(t *testing.T) {
	dir := stubCacheDir(t)
	projectDir := newLaunchTestProject(t)
	stubLaunchSideEffects(t)
	httpPort, wsPort := freeTCPPort(t), freeTCPPort(t)
	// in-process daemon 的记录写在真实 user cache dir；测试用 stub 目录放一份
	// 等价记录（旧形态，无 cli_version），launch 的 CLI 侧补写才有迹可循。
	writeDaemonRecord(t, dir, httpPort, wsPort, "4.2.5")

	// attach 无编辑器会以 EDITOR_NOT_CONNECTED 结束——补写发生在会话等待之前，
	// 与随后是否超时无关。
	_, _ = runLaunchAttachFlow(t, projectDir, func(o *launchOptions) {
		o.foreground = true
		o.httpPort = httpPort
		o.wsPort = wsPort
	})

	path := filepath.Join(dir, "godot-ai-cli", fmt.Sprintf("daemon-%d.json", httpPort))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("daemon record disappeared: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("record is not JSON: %v\n%s", err, raw)
	}
	if rec["cli_version"] != version.Version {
		t.Errorf("cli_version = %v, want this binary's %q", rec["cli_version"], version.Version)
	}
	if rec["version"] != "4.2.5" {
		t.Errorf("the daemon's own fields must survive the stamp: %v", rec)
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

	// 夹具路径必须在本平台是绝对路径：beta.27 起 status --project 会做
	// filepath.Abs，unix 上 Windows 风格的 D:\... 会被拼上 cwd 而永不匹配。
	projArg := `D:\games\rpg`
	projScan := "D:/games/rpg"
	otherScan := "D:/other/demo" // 别的工程：不得误报
	if runtime.GOOS != "windows" {
		projArg = "/tmp/games/rpg"
		projScan = projArg
		otherScan = "/tmp/other/demo"
	}

	restore := godot.SetEditorScannerForTest(func() ([]godot.EditorProcess, error) {
		return []godot.EditorProcess{
			{PID: 5020, Project: projScan, GameRunning: &godot.EditorGameProcess{PID: 38004, Scene: "res://test.tscn", Editor: 5020}},
			{PID: 25548, Project: otherScan},
		}, nil
	})
	defer restore()

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"status", "--project", projArg, "--http-port", strconv.Itoa(d.HTTPPort())})
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
	// 注意 macOS 的 /var 是指向 /private/var 的符号链接：t.TempDir 返回逻辑
	// 路径而 getwd 返回物理路径——扫描器侧必须用 os.Getwd 的物理路径，否则
	// Abs(".") 与扫描结果在 macOS 上永不匹配。
	projectDir := t.TempDir()
	t.Chdir(projectDir)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}

	restore := godot.SetEditorScannerForTest(func() ([]godot.EditorProcess, error) {
		return []godot.EditorProcess{
			{PID: 5020, Project: wd},
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
