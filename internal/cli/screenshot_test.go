package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
	"github.com/mimajiushi/godot-ai-cli/internal/testutil/mockplugin"
)

// screenshotFixturePNG is a 2x1 PNG: (0,0)=#212327, (1,0)=#FF0000.
func screenshotFixturePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.SetRGBA(0, 0, color.RGBA{0x21, 0x23, 0x27, 0xFF})
	img.SetRGBA(1, 0, color.RGBA{0xFF, 0, 0, 0xFF})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// startScreenshotDaemon brings up a real daemon plus a mock plugin that
// answers get_editor_state (is_playing per the argument) and
// take_screenshot (the fixture PNG as a data URI).
func startScreenshotDaemon(t *testing.T, playing bool) (*daemon.Daemon, *mockplugin.Plugin) {
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

	dataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshotFixturePNG(t))
	plugin := mockplugin.Dial(t, d.Bridge().Addr(), nil)
	plugin.SetResponder(func(command string, _ map[string]any) *mockplugin.Response {
		switch command {
		case "get_editor_state":
			return &mockplugin.Response{Data: map[string]any{"is_playing": playing}}
		case "take_screenshot":
			return &mockplugin.Response{Data: map[string]any{
				"format": "png", "width": 2, "height": 1, "frames_drawn": 1,
				"image_base64": dataURI,
			}}
		default:
			return &mockplugin.Response{Status: "error",
				Error: map[string]any{"code": "UNKNOWN_COMMAND", "message": command, "data": map[string]any{}}}
		}
	})
	return d, plugin
}

// runScreenshotArgs executes `editor screenshot` with the given extra args
// and captures stdout.
func runScreenshotArgs(t *testing.T, httpPort int, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"editor", "screenshot", "--http-port", itoa(httpPort)}, args...))
	err := cmd.Execute()
	return buf.String(), err
}

// TestScreenshotGamePreflight: --source game with the game stopped fails
// with GAME_NOT_RUNNING and never reaches the plugin's take_screenshot.
func TestScreenshotGamePreflight(t *testing.T) {
	d, plugin := startScreenshotDaemon(t, false)

	out, err := runScreenshotArgs(t, d.HTTPPort(), "--source", "game")
	if err == nil {
		t.Fatal("expected GAME_NOT_RUNNING to exit non-zero")
	}
	if !strings.Contains(out, "GAME_NOT_RUNNING") || !strings.Contains(out, "project run") {
		t.Errorf("output = %s", out)
	}
	if got := plugin.Count("take_screenshot"); got != 0 {
		t.Errorf("take_screenshot reached the plugin %d times despite the preflight", got)
	}
}

// TestScreenshotOutSavesFile: --out writes the decoded PNG, strips
// image_base64 from the output, and reports saved/bytes.
func TestScreenshotOutSavesFile(t *testing.T) {
	d, _ := startScreenshotDaemon(t, true)
	outFile := filepath.Join(t.TempDir(), "nested", "shot.png")

	out, err := runScreenshotArgs(t, d.HTTPPort(), "--source", "game", "--out", outFile)
	if err != nil {
		t.Fatalf("screenshot --out: %v\n%s", err, out)
	}
	written, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("--out file not written: %v", err)
	}
	if !bytes.Equal(written, screenshotFixturePNG(t)) {
		t.Errorf("--out file content differs from the captured PNG")
	}
	var parsed map[string]any
	if err := json.Unmarshal(bytes.TrimSpace([]byte(out)), &parsed); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if _, leaked := parsed["image_base64"]; leaked {
		t.Errorf("image_base64 must be omitted with --out: %s", out)
	}
	if parsed["bytes"].(float64) != float64(len(written)) || parsed["saved"] == nil {
		t.Errorf("missing saved/bytes in %s", out)
	}
}

// TestScreenshotAssert: pixel assertions pass within tolerance and fail with
// the PIXEL_ASSERT_FAILED envelope carrying the samples.
func TestScreenshotAssert(t *testing.T) {
	d, _ := startScreenshotDaemon(t, true)

	// exact match + one within tolerance 1
	out, err := runScreenshotArgs(t, d.HTTPPort(), "--source", "game",
		"--assert", "#212327@0,0", "--assert", "#FF0001@1,0", "--tolerance", "1")
	if err != nil {
		t.Fatalf("assertions should pass: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"passed":true`) {
		t.Errorf("passed:true missing in %s", out)
	}

	// mismatch: exit non-zero, envelope names the code and the samples
	out, err = runScreenshotArgs(t, d.HTTPPort(), "--source", "game", "--assert", "#000000@0,0")
	if err == nil {
		t.Fatal("expected PIXEL_ASSERT_FAILED to exit non-zero")
	}
	if !strings.Contains(out, "PIXEL_ASSERT_FAILED") || !strings.Contains(out, "#212327") {
		t.Errorf("failure envelope = %s", out)
	}

	// malformed assertion is a params error
	if _, err = runScreenshotArgs(t, d.HTTPPort(), "--source", "game", "--assert", "bogus"); err == nil {
		t.Fatal("malformed --assert must fail")
	}
}

// TestScreenshotLegacyShapeUnchanged: without --out/--assert the response is
// printed exactly as before (base64 included).
func TestScreenshotLegacyShapeUnchanged(t *testing.T) {
	d, _ := startScreenshotDaemon(t, true)
	out, err := runScreenshotArgs(t, d.HTTPPort(), "--source", "game")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "image_base64") || !strings.Contains(out, "data:image/png;base64,") {
		t.Errorf("legacy response lost image_base64: %s", out)
	}
}

// TestScreenshotFullRes: --full-res sends max_resolution=0 (no cap) instead
// of the CLI's default 640; without it the wire keeps 640.
func TestScreenshotFullRes(t *testing.T) {
	d, plugin := startScreenshotDaemon(t, true)

	out, err := runScreenshotArgs(t, d.HTTPPort(), "--source", "game", "--full-res")
	if err != nil {
		t.Fatalf("screenshot --full-res: %v\n%s", err, out)
	}
	got := plugin.Received()
	var wire map[string]any
	for _, rec := range got {
		if rec.Command == "take_screenshot" {
			wire = rec.Params
		}
	}
	if wire == nil {
		t.Fatal("take_screenshot never reached the plugin")
	}
	if v, ok := wire["max_resolution"].(float64); !ok || v != 0 {
		t.Errorf("--full-res wire max_resolution = %v, want 0", wire["max_resolution"])
	}

	out, err = runScreenshotArgs(t, d.HTTPPort(), "--source", "game")
	if err != nil {
		t.Fatalf("plain screenshot: %v\n%s", err, out)
	}
	got = plugin.Received()
	wire = nil
	for _, rec := range got {
		if rec.Command == "take_screenshot" {
			wire = rec.Params
		}
	}
	if v, ok := wire["max_resolution"].(float64); !ok || v != 640 {
		t.Errorf("default wire max_resolution = %v, want 640", wire["max_resolution"])
	}
}

// TestScreenshotRegion: --region crops locally at source resolution — the
// wire asks for an uncapped capture, the output is the crop, and --assert
// coordinates then refer to the cropped image.
func TestScreenshotRegion(t *testing.T) {
	d, plugin := startScreenshotDaemon(t, true)

	// 2x1 fixture: (0,0)=#212327, (1,0)=#FF0000. Crop the right half.
	out, err := runScreenshotArgs(t, d.HTTPPort(), "--source", "game", "--region", "1,0,1,1")
	if err != nil {
		t.Fatalf("screenshot --region: %v\n%s", err, out)
	}
	var parsed map[string]any
	if err := json.Unmarshal(bytes.TrimSpace([]byte(out)), &parsed); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if parsed["width"].(float64) != 1 || parsed["height"].(float64) != 1 {
		t.Errorf("cropped size = %vx%v", parsed["width"], parsed["height"])
	}
	if parsed["region"] == nil {
		t.Errorf("region not echoed: %s", out)
	}
	// The crop keeps the red pixel: assert against the cropped coordinates.
	out, err = runScreenshotArgs(t, d.HTTPPort(), "--source", "game", "--region", "1,0,1,1",
		"--assert", "#FF0000@0,0")
	if err != nil {
		t.Fatalf("--assert on cropped image: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"passed":true`) {
		t.Errorf("assert on crop failed: %s", out)
	}

	// The wire asked for the uncapped frame so the crop happens at source res.
	got := plugin.Received()
	var wire map[string]any
	for _, rec := range got {
		if rec.Command == "take_screenshot" {
			wire = rec.Params
		}
	}
	if v, ok := wire["max_resolution"].(float64); !ok || v != 0 {
		t.Errorf("--region wire max_resolution = %v, want 0 (uncapped source)", wire["max_resolution"])
	}

	// Out-of-bounds region → INVALID_PARAMS, nothing saved.
	if _, err = runScreenshotArgs(t, d.HTTPPort(), "--source", "game", "--region", "5,5,9,9"); err == nil {
		t.Fatal("out-of-bounds --region must fail")
	}
}
