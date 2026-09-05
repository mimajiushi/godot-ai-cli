package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/gif"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
	"github.com/mimajiushi/godot-ai-cli/internal/testutil/mockplugin"
)

// startRecordDaemon brings up a daemon plus a mock plugin answering
// get_editor_state (playing) and game_command (a two-frame record payload).
func startRecordDaemon(t *testing.T) (*daemon.Daemon, *mockplugin.Plugin) {
	t.Helper()
	d, err := daemon.Start(context.Background(), daemon.Config{HTTPPort: 0, WSPort: 0, Version: "test"})
	if err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = d.Shutdown(ctx)
	})

	frameB64 := "data:image/png;base64," + base64Std(screenshotFixturePNG(t))
	plugin := mockplugin.Dial(t, d.Bridge().Addr(), nil)
	plugin.SetResponder(func(command string, params map[string]any) *mockplugin.Response {
		switch command {
		case "get_editor_state":
			return &mockplugin.Response{Data: map[string]any{"is_playing": true}}
		case "game_command":
			if op, _ := params["op"].(string); op != "record_frames" {
				return &mockplugin.Response{Status: "error",
					Error: map[string]any{"code": "UNKNOWN_OP", "message": op, "data": map[string]any{}}}
			}
			return &mockplugin.Response{Data: map[string]any{
				"op": "record_frames", "source": "game",
				"captured":        2,
				"frames":          []any{frameB64, frameB64},
				"frame_deltas_ms": []any{16.0, 16.0},
				"width":           2,
				"height":          1,
			}}
		default:
			return &mockplugin.Response{Status: "error",
				Error: map[string]any{"code": "UNKNOWN_COMMAND", "message": command, "data": map[string]any{}}}
		}
	})
	return d, plugin
}

func base64Std(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

// runRecordArgs executes `editor record` with the given extra args.
func runRecordArgs(t *testing.T, httpPort int, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"editor", "record", "--http-port", itoa(httpPort)}, args...))
	err := cmd.Execute()
	return buf.String(), err
}

// TestRecordPngOutDir: --frames N --out-dir saves one PNG per frame and the
// stdout payload carries files + deltas but never the base64 frames.
func TestRecordPngOutDir(t *testing.T) {
	d, plugin := startRecordDaemon(t)
	dir := filepath.Join(t.TempDir(), "burst")

	out, err := runRecordArgs(t, d.HTTPPort(), "--frames", "2", "--out-dir", dir)
	if err != nil {
		t.Fatalf("record: %v\n%s", err, out)
	}
	var parsed map[string]any
	if err := json.Unmarshal(bytes.TrimSpace([]byte(out)), &parsed); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if parsed["captured"].(float64) != 2 {
		t.Errorf("captured = %v", parsed["captured"])
	}
	if _, leaked := parsed["frames"]; leaked {
		t.Errorf("frames payload leaked to stdout: %s", out)
	}
	files, _ := parsed["files"].([]any)
	if len(files) != 2 {
		t.Fatalf("files = %v", parsed["files"])
	}
	for _, f := range files {
		if _, err := os.Stat(f.(string)); err != nil {
			t.Errorf("frame file missing: %v", err)
		}
	}

	// wire: game_command with op record_frames and the requested frame count
	got := plugin.Received()
	var wire map[string]any
	for _, rec := range got {
		if rec.Command == "game_command" {
			wire = rec.Params
		}
	}
	if wire == nil || wire["op"] != "record_frames" {
		t.Fatalf("wire = %v", got)
	}
	nested, _ := wire["params"].(map[string]any)
	if nested["frames"].(float64) != 2 {
		t.Errorf("wire frames = %v", nested["frames"])
	}
}

// TestRecordDurationFpsAndGif: --duration×--fps resolves the frame count and
// --format gif --out writes a decodable animated GIF.
func TestRecordDurationFpsAndGif(t *testing.T) {
	d, plugin := startRecordDaemon(t)
	outFile := filepath.Join(t.TempDir(), "run.gif")

	out, err := runRecordArgs(t, d.HTTPPort(), "--duration", "0.5", "--fps", "30",
		"--format", "gif", "--out", outFile)
	if err != nil {
		t.Fatalf("record gif: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"saved":`) {
		t.Errorf("gif payload missing saved: %s", out)
	}
	raw, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	g, err := gif.DecodeAll(bytes.NewReader(raw))
	if err != nil || len(g.Image) != 2 {
		t.Fatalf("saved GIF not decodable: %v (frames %d)", err, len(g.Image))
	}

	got := plugin.Received()
	var wire map[string]any
	for _, rec := range got {
		if rec.Command == "game_command" {
			wire = rec.Params
		}
	}
	nested, _ := wire["params"].(map[string]any)
	if nested["frames"].(float64) != 15 { // 0.5s × 30fps
		t.Errorf("wire frames = %v, want 15", nested["frames"])
	}
}

// TestRecordValidation: missing frame spec / missing output flags / unknown
// format are INVALID_PARAMS before anything hits the wire.
func TestRecordValidation(t *testing.T) {
	d, _ := startRecordDaemon(t)
	if _, err := runRecordArgs(t, d.HTTPPort(), "--out-dir", t.TempDir()); err == nil {
		t.Error("no --frames/--duration accepted")
	}
	if _, err := runRecordArgs(t, d.HTTPPort(), "--frames", "2"); err == nil {
		t.Error("png without --out-dir accepted")
	}
	if _, err := runRecordArgs(t, d.HTTPPort(), "--frames", "2", "--format", "mp4", "--out", "x.mp4"); err == nil {
		t.Error("unknown format accepted")
	}
}

// TestRecordNotRunning: the preflight fails GAME_NOT_RUNNING without reaching
// game_command.
func TestRecordNotRunning(t *testing.T) {
	d, err := daemon.Start(context.Background(), daemon.Config{HTTPPort: 0, WSPort: 0, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })
	plugin := mockplugin.Dial(t, d.Bridge().Addr(), nil)
	plugin.SetResponder(func(command string, _ map[string]any) *mockplugin.Response {
		return &mockplugin.Response{Data: map[string]any{"is_playing": false}}
	})
	out, err := runRecordArgs(t, d.HTTPPort(), "--frames", "2", "--out-dir", t.TempDir())
	if err == nil {
		t.Fatal("expected GAME_NOT_RUNNING to exit non-zero")
	}
	if !strings.Contains(out, "GAME_NOT_RUNNING") {
		t.Errorf("output = %s", out)
	}
	if got := plugin.Count("game_command"); got != 0 {
		t.Errorf("game_command reached the plugin %d times despite the preflight", got)
	}
}
