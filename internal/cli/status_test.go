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
