package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
	"github.com/mimajiushi/godot-ai-cli/internal/godot"
	"github.com/mimajiushi/godot-ai-cli/internal/testutil/mockplugin"
)

// TestStatusReportsGodotCompatibility drives the status command against a
// real daemon with mock plugin sessions on Godot 4.4 / 4.7 / 5.1 / an
// unparseable version: <4.5 is flagged incompatible, 5.x and garbage stay
// compatible but carry a warning, 4.7 stays silent.
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
	mockplugin.Dial(t, addr, map[string]any{"session_id": "old@0001", "godot_version": "4.4.stable.official"})
	mockplugin.Dial(t, addr, map[string]any{"session_id": "ok@0002", "godot_version": "4.7.stable.official"})
	mockplugin.Dial(t, addr, map[string]any{"session_id": "new@0003", "godot_version": "5.1.stable.official"})
	mockplugin.Dial(t, addr, map[string]any{"session_id": "weird@0004", "godot_version": "garbage"})

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
	if !ok || len(sessions) != 4 {
		t.Fatalf("sessions = %v", out["sessions"])
	}
	byID := make(map[string]map[string]any, len(sessions))
	for _, entry := range sessions {
		s, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("session entry = %v", entry)
		}
		byID[s["session_id"].(string)] = s
	}

	// Godot 4.4: below the support floor → incompatible with the wording
	// of the CheckCompatibility error.
	old := byID["old@0001"]
	if old["godot_compatible"] != false {
		t.Errorf("4.4 session godot_compatible = %v", old["godot_compatible"])
	}
	if w, _ := old["warning"].(string); !strings.Contains(w, "not supported") {
		t.Errorf("4.4 session warning = %v", old["warning"])
	}

	// Godot 4.7: fully supported → compatible and no warning at all.
	okSess := byID["ok@0002"]
	if okSess["godot_compatible"] != true {
		t.Errorf("4.7 session godot_compatible = %v", okSess["godot_compatible"])
	}
	if _, warned := okSess["warning"]; warned {
		t.Errorf("4.7 session unexpectedly warns: %v", okSess["warning"])
	}

	// Godot 5.1: untested major → compatible but warns.
	newSess := byID["new@0003"]
	if newSess["godot_compatible"] != true {
		t.Errorf("5.1 session godot_compatible = %v", newSess["godot_compatible"])
	}
	if w, _ := newSess["warning"].(string); !strings.Contains(w, "untested major version") {
		t.Errorf("5.1 session warning = %v", newSess["warning"])
	}

	// Unparseable: compatible, but the warning says the version is unknown.
	weird := byID["weird@0004"]
	if weird["godot_compatible"] != true {
		t.Errorf("garbage session godot_compatible = %v", weird["godot_compatible"])
	}
	if w, _ := weird["warning"].(string); !strings.Contains(w, "could not be parsed") {
		t.Errorf("garbage session warning = %v", weird["warning"])
	}

	// The top-level roll-up collects exactly the three warning sessions.
	warnings, ok := out["warnings"].([]any)
	if !ok || len(warnings) != 3 {
		t.Fatalf("warnings = %v", out["warnings"])
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
	mockplugin.Dial(t, addr, map[string]any{"session_id": "cli@0001", "launched_by": "cli"})
	mockplugin.Dial(t, addr, map[string]any{"session_id": "user@0002", "launched_by": "user"})
	mockplugin.Dial(t, addr, map[string]any{"session_id": "legacy@0003"}) // no launched_by

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
	mockplugin.Dial(t, addr, map[string]any{"session_id": "aligned@0001", "plugin_version": "3.2.8", "launched_by": "cli"})
	mockplugin.Dial(t, addr, map[string]any{"session_id": "stale@0002", "plugin_version": "3.2.6", "launched_by": "cli"})

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
		{"4.5.stable.official", true, false},
		{"4.6.2.stable.mono.official", true, false},
		{"4.7.stable.official", true, false},
		{"5.1.stable.official", true, true},
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
	mockplugin.Dial(t, fmt.Sprintf("127.0.0.1:%d", d2.WSPort()), map[string]any{
		"session_id": "other@0001", "project_path": "/other/project/",
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

	mockplugin.Dial(t, fmt.Sprintf("127.0.0.1:%d", d.WSPort()), map[string]any{
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

	mockplugin.Dial(t, fmt.Sprintf("127.0.0.1:%d", d.WSPort()), map[string]any{
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
	mockplugin.Dial(t, fmt.Sprintf("127.0.0.1:%d", d.WSPort()), map[string]any{
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
