package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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

// screenshotCoordsFixturePNG is a 4x2 PNG whose left 2 columns are #212327 and
// right 2 columns are #FF0000. Paired with canvas_size 2x1 (canvas_scale 2.0)
// the canvas point (1,0) lands on image pixel (2,0) — so probing the wrong
// coordinate space returns the wrong color, which is what these tests pin.
func screenshotCoordsFixturePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			c := color.RGBA{0x21, 0x23, 0x27, 0xFF}
			if x >= 2 {
				c = color.RGBA{0xFF, 0, 0, 0xFF}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// screenshotCoordsHalfFixturePNG is an 8x4 PNG whose left 4 columns are
// #212327 and right 4 columns are #FF0000. Reported as a 4x2 canvas
// (canvas_scale 2.0) it makes an origin-shift bug visible: within a crop that
// starts at frame x=2, canvas (1,0) resolves to #212327 only when the crop
// origin is subtracted — forgetting it probes frame (4,0) = #FF0000 instead.
func screenshotCoordsHalfFixturePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			c := color.RGBA{0x21, 0x23, 0x27, 0xFF}
			if x >= 4 {
				c = color.RGBA{0xFF, 0, 0, 0xFF}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// pngFile writes img as a PNG under dir and returns its path.
func pngFile(t *testing.T, dir, name string, img image.Image) string {
	t.Helper()
	path := filepath.Join(dir, name)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// solidPNG builds a w x h image filled with c.
func solidPNG(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// startScreenshotDaemon brings up a real daemon plus a mock plugin that
// answers get_editor_state (is_playing per the argument) and
// take_screenshot (the fixture PNG as a data URI).
func startScreenshotDaemon(t *testing.T, playing bool) (*daemon.Daemon, *mockplugin.Plugin) {
	t.Helper()
	return startScreenshotDaemonWith(t, playing, nil)
}

// startScreenshotDaemonWith is startScreenshotDaemon with extra take_screenshot
// response keys (e.g. canvas_size/canvas_scale) and an optional replacement
// fixture PNG (nil = the 2x1 default fixture).
func startScreenshotDaemonWith(t *testing.T, playing bool, extra map[string]any) (*daemon.Daemon, *mockplugin.Plugin) {
	t.Helper()
	return startScreenshotDaemonFixture(t, playing, extra, nil)
}

// startScreenshotDaemonFixture is the full form: the responder merges `extra`
// into the fixture response so tests can pin the plugin's coordinate metadata.
func startScreenshotDaemonFixture(t *testing.T, playing bool, extra map[string]any, fixture []byte) (*daemon.Daemon, *mockplugin.Plugin) {
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

	if fixture == nil {
		fixture = screenshotFixturePNG(t)
	}
	dataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(fixture)
	plugin := mockplugin.Dial(t, d.Bridge().Addr(), d.Bridge().WSCapability, nil)
	plugin.SetResponder(func(command string, _ map[string]any) *mockplugin.Response {
		switch command {
		case "get_editor_state":
			return &mockplugin.Response{Data: map[string]any{"is_playing": playing}}
		case "take_screenshot":
			data := map[string]any{
				"format": "png", "width": 2, "height": 1, "frames_drawn": 1,
				"image_base64": dataURI,
			}
			for k, v := range extra {
				data[k] = v
			}
			return &mockplugin.Response{Data: data}
		default:
			return &mockplugin.Response{Status: "error",
				Error: map[string]any{"code": "UNKNOWN_COMMAND", "message": command, "data": map[string]any{}}}
		}
	})
	return d, plugin
}

// decodeScreenshotOutput parses one `editor screenshot` stdout payload.
func decodeScreenshotOutput(t *testing.T, out string) map[string]any {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal(bytes.TrimSpace([]byte(out)), &parsed); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	return parsed
}

// lastScreenshotWire returns the params of the last take_screenshot call that
// reached the mock plugin.
func lastScreenshotWire(t *testing.T, plugin *mockplugin.Plugin) map[string]any {
	t.Helper()
	var wire map[string]any
	for _, rec := range plugin.Received() {
		if rec.Command == "take_screenshot" {
			wire = rec.Params
		}
	}
	if wire == nil {
		t.Fatal("take_screenshot never reached the plugin")
	}
	return wire
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

// TestScreenshotCoordsCanvasConvertsRegionAndAssert: with --coords canvas the
// --region/--assert are absolute game-canvas coordinates, multiplied by the
// response's canvas_scale before cropping, the wire asks for the uncapped
// frame, and the response echoes coord_space/image_region/scale.
func TestScreenshotCoordsCanvasConvertsRegionAndAssert(t *testing.T) {
	// canvas 2x1, image 4x2, scale 2.0 — canvas (1,0,1,1) → image (2,0,2,2).
	d, plugin := startScreenshotDaemonFixture(t, true, map[string]any{
		"width": 4, "height": 2,
		"canvas_size": []any{2.0, 1.0}, "canvas_scale": []any{2.0, 2.0},
	}, screenshotCoordsFixturePNG(t))

	// Absolute canvas (1,0) → frame (2,0) → minus the crop origin (2,0) → the
	// cropped image's (0,0), which is the fixture's red half. The flip side of
	// this mapper is pinned by TestScreenshotCoordsCanvasAssertSubtractsCropOrigin.
	out, err := runScreenshotArgs(t, d.HTTPPort(), "--source", "game",
		"--coords", "canvas", "--region", "1,0,1,1", "--assert", "#FF0000@1,0",
		"--max-resolution", "1")
	if err != nil {
		t.Fatalf("canvas-space region+assert: %v\n%s", err, out)
	}
	parsed := decodeScreenshotOutput(t, out)
	if parsed["coord_space"] != "canvas" {
		t.Errorf("coord_space = %v, want canvas", parsed["coord_space"])
	}
	if parsed["scale"].(float64) != 2.0 {
		t.Errorf("scale = %v, want 2", parsed["scale"])
	}
	if got := parsed["image_region"]; got == nil || fmt.Sprint(got) != "[2 0 2 2]" {
		t.Errorf("image_region = %v, want [2 0 2 2]", got)
	}
	// The canvas region rides along so the conversion stays traceable.
	if got := parsed["canvas_region"]; fmt.Sprint(got) != "[1 0 1 1]" {
		t.Errorf("canvas_region = %v, want [1 0 1 1]", got)
	}
	if parsed["width"].(float64) != 2 || parsed["height"].(float64) != 2 {
		t.Errorf("cropped size = %vx%v, want 2x2 (--coords canvas keeps the crop at source resolution, --max-resolution must not shrink it)", parsed["width"], parsed["height"])
	}
	if parsed["passed"] != true {
		t.Errorf("canvas-space assert must be probed in image pixels: %s", out)
	}
	// samples[].at reports the resolved pixel inside the cropped image.
	if samples, ok := parsed["samples"].([]any); !ok || len(samples) != 1 ||
		fmt.Sprint(samples[0].(map[string]any)["at"]) != "[0 0]" {
		t.Errorf("samples = %v, want one sample at the crop-relative (0,0)", parsed["samples"])
	}
	if v, ok := lastScreenshotWire(t, plugin)["max_resolution"].(float64); !ok || v != 0 {
		t.Errorf("--coords canvas wire max_resolution = %v, want 0 (whole frame)", v)
	}

	// The same canvas region read as image pixels lands on the dark half —
	// proving the two coordinate spaces are genuinely different.
	out, err = runScreenshotArgs(t, d.HTTPPort(), "--source", "game",
		"--region", "1,0,1,1", "--assert", "#FF0000@0,0")
	if err == nil || !strings.Contains(out, "PIXEL_ASSERT_FAILED") {
		t.Fatalf("image-space region must probe the dark pixel, got: %v\n%s", err, out)
	}
	out, err = runScreenshotArgs(t, d.HTTPPort(), "--source", "game",
		"--region", "1,0,1,1", "--assert", "#212327@0,0")
	if err != nil {
		t.Fatalf("image-space assert on the dark pixel: %v\n%s", err, out)
	}
}

// TestScreenshotCoordsCanvasAssertSubtractsCropOrigin: canvas-mode --assert takes
// ABSOLUTE canvas coordinates — multiply by the scale to get a whole-frame pixel,
// then subtract the crop origin, because the probe happens on the cropped image.
// Without that subtraction the probe silently lands on a different pixel whenever
// the region origin is non-zero (F-2).
//
// Fixture: 8x4 PNG, left 4 columns #212327 / right 4 columns #FF0000; reported as
// a 4x2 canvas (scale 2.0). Canvas region (1,0,2,2) → frame (2,0,4,4): the crop is
// the dark left half plus the red right half.
func TestScreenshotCoordsCanvasAssertSubtractsCropOrigin(t *testing.T) {
	d, _ := startScreenshotDaemonFixture(t, true, map[string]any{
		"width": 8, "height": 4,
		"canvas_size": []any{4.0, 2.0}, "canvas_scale": []any{2.0, 2.0},
	}, screenshotCoordsHalfFixturePNG(t))

	// canvas (1,0) → frame (2,0) → crop-relative (0,0) = #212327. A mapper that
	// forgot the origin would probe the crop's (2,0) = frame (4,0) = #FF0000.
	out, err := runScreenshotArgs(t, d.HTTPPort(), "--source", "game",
		"--coords", "canvas", "--region", "1,0,2,2", "--assert", "#212327@1,0")
	if err != nil {
		t.Fatalf("non-zero-origin canvas assert must probe the crop-relative pixel: %v\n%s", err, out)
	}
	parsed := decodeScreenshotOutput(t, out)
	if got := fmt.Sprint(parsed["image_region"]); got != "[2 0 4 4]" {
		t.Errorf("image_region = %v, want [2 0 4 4]", parsed["image_region"])
	}
	samples, ok := parsed["samples"].([]any)
	if !ok || len(samples) != 1 || fmt.Sprint(samples[0].(map[string]any)["at"]) != "[0 0]" {
		t.Errorf("sample = %v, want the resolved crop-relative point [0 0]", parsed["samples"])
	}

	// The other end of the crop: canvas (2,0) → frame (4,0) → crop-relative (2,0) = red.
	out, err = runScreenshotArgs(t, d.HTTPPort(), "--source", "game",
		"--coords", "canvas", "--region", "1,0,2,2", "--assert", "#FF0000@2,0")
	if err != nil {
		t.Fatalf("canvas assert on the red half of the crop: %v\n%s", err, out)
	}

	// Origin 0 must behave exactly as before the fix (no offset).
	out, err = runScreenshotArgs(t, d.HTTPPort(), "--source", "game",
		"--coords", "canvas", "--region", "0,0,2,1", "--assert", "#212327@1,0")
	if err != nil {
		t.Fatalf("zero-origin canvas region+assert must not regress: %v\n%s", err, out)
	}

	// No region at all: canvas coordinates map straight onto whole-frame pixels.
	out, err = runScreenshotArgs(t, d.HTTPPort(), "--source", "game",
		"--coords", "canvas", "--assert", "#FF0000@2,0")
	if err != nil {
		t.Fatalf("region-less canvas assert must probe the whole frame: %v\n%s", err, out)
	}
	if _, ok := decodeScreenshotOutput(t, out)["image_region"]; ok {
		t.Errorf("no --region must not report image_region: %s", out)
	}
}

// TestScreenshotCoordsCanvasWithoutMetadata: an older plugin omits
// canvas_scale → the conversion cannot be done, so --coords canvas fails
// loudly instead of silently probing the wrong pixel.
func TestScreenshotCoordsCanvasWithoutMetadata(t *testing.T) {
	d, _ := startScreenshotDaemon(t, true)
	out, err := runScreenshotArgs(t, d.HTTPPort(), "--source", "game",
		"--coords", "canvas", "--assert", "#FF0000@0,0")
	if err == nil {
		t.Fatal("--coords canvas without canvas_scale must fail")
	}
	if !strings.Contains(out, "COORD_SPACE_UNAVAILABLE") {
		t.Errorf("output = %s", out)
	}
}

// TestScreenshotCoordsCanvasBareKeepsImage: a bare --coords canvas has nothing
// to convert, so the capture still flows through the legacy shape (base64
// included) with the coordinate space echoed additively.
func TestScreenshotCoordsCanvasBareKeepsImage(t *testing.T) {
	d, _ := startScreenshotDaemonFixture(t, true, map[string]any{
		"canvas_size": []any{1.0, 0.5}, "canvas_scale": []any{2.0, 2.0},
	}, nil)

	out, err := runScreenshotArgs(t, d.HTTPPort(), "--source", "game", "--coords", "canvas")
	if err != nil {
		t.Fatalf("bare --coords canvas: %v\n%s", err, out)
	}
	parsed := decodeScreenshotOutput(t, out)
	if parsed["coord_space"] != "canvas" || parsed["scale"].(float64) != 2.0 {
		t.Errorf("echo = %v/%v, want canvas/2", parsed["coord_space"], parsed["scale"])
	}
	if _, ok := parsed["image_base64"]; !ok {
		t.Errorf("bare --coords canvas must keep the image: %s", out)
	}
}

// TestScreenshotCoordsInvalidValue: --coords only accepts image|canvas.
func TestScreenshotCoordsInvalidValue(t *testing.T) {
	d, plugin := startScreenshotDaemon(t, true)
	out, err := runScreenshotArgs(t, d.HTTPPort(), "--source", "game", "--coords", "pixel")
	if err == nil {
		t.Fatal("--coords pixel must fail")
	}
	if !strings.Contains(out, "INVALID_PARAMS") || !strings.Contains(out, "pixel") {
		t.Errorf("output = %s", out)
	}
	if got := plugin.Count("take_screenshot"); got != 0 {
		t.Errorf("an invalid --coords must not reach the plugin (%d calls)", got)
	}
}

// TestScreenshotImageModeWireUnchanged: without the new flags the request and
// the response keep their historical shape byte-for-byte (backward
// compatibility is the one red line of R-3).
func TestScreenshotImageModeWireUnchanged(t *testing.T) {
	d, plugin := startScreenshotDaemon(t, true)
	out, err := runScreenshotArgs(t, d.HTTPPort(), "--source", "game")
	if err != nil {
		t.Fatalf("plain screenshot: %v\n%s", err, out)
	}
	wire := lastScreenshotWire(t, plugin)
	if len(wire) != 3 {
		t.Errorf("default wire params = %v, want exactly source+include_image+max_resolution", wire)
	}
	if wire["source"] != "game" || wire["max_resolution"].(float64) != 640 {
		t.Errorf("default wire params = %v", wire)
	}
	parsed := decodeScreenshotOutput(t, out)
	if len(parsed) != 5 {
		t.Errorf("default data keys = %v, want the plugin's five untouched", parsed)
	}
	for _, forbidden := range []string{"coord_space", "scale", "diff_ratio", "baseline", "image_region"} {
		if _, ok := parsed[forbidden]; ok {
			t.Errorf("default output gained %q: %s", forbidden, out)
		}
	}
}

// TestScreenshotBaselineIdenticalPasses: an identical baseline reports a zero
// ratio and passes.
func TestScreenshotBaselineIdenticalPasses(t *testing.T) {
	d, _ := startScreenshotDaemon(t, true)
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.SetRGBA(0, 0, color.RGBA{0x21, 0x23, 0x27, 0xFF})
	img.SetRGBA(1, 0, color.RGBA{0xFF, 0, 0, 0xFF})
	basePath := pngFile(t, t.TempDir(), "base.png", img)

	out, err := runScreenshotArgs(t, d.HTTPPort(), "--source", "game", "--baseline", basePath)
	if err != nil {
		t.Fatalf("identical baseline: %v\n%s", err, out)
	}
	parsed := decodeScreenshotOutput(t, out)
	if parsed["diff_ratio"].(float64) != 0 || parsed["diff_pixels"].(float64) != 0 {
		t.Errorf("identical baseline = %v/%v, want 0/0", parsed["diff_ratio"], parsed["diff_pixels"])
	}
	if parsed["diff_passed"] != true {
		t.Errorf("diff_passed missing: %s", out)
	}
}

// TestScreenshotBaselineDiffFailsWithRatioAndSamples: a differing baseline
// exceeding the threshold exits non-zero with BASELINE_DIFF_FAILED carrying
// diff_ratio, the threshold, and the differing sample points.
func TestScreenshotBaselineDiffFailsWithRatioAndSamples(t *testing.T) {
	d, _ := startScreenshotDaemon(t, true)
	basePath := pngFile(t, t.TempDir(), "black.png", solidPNG(2, 1, color.RGBA{0, 0, 0, 0xFF}))

	out, err := runScreenshotArgs(t, d.HTTPPort(), "--source", "game", "--baseline", basePath)
	if err == nil {
		t.Fatal("a fully differing baseline must exit non-zero")
	}
	if !strings.Contains(out, "BASELINE_DIFF_FAILED") {
		t.Errorf("output = %s", out)
	}
	parsed := decodeScreenshotOutput(t, out)
	fail := parsed["error"].(map[string]any)
	data := fail["data"].(map[string]any)
	if data["diff_ratio"].(float64) != 1.0 {
		t.Errorf("diff_ratio = %v, want 1", data["diff_ratio"])
	}
	if data["threshold"].(float64) != 0 {
		t.Errorf("threshold = %v, want the default 0", data["threshold"])
	}
	if got := data["diff_pixels"].(float64); got != 2 {
		t.Errorf("diff_pixels = %v, want 2", got)
	}
	if samples, ok := data["samples"].([]any); !ok || len(samples) != 2 {
		t.Errorf("samples = %v, want both differing pixels", data["samples"])
	}

	// Boundary: diff_ratio == threshold does NOT fail (the contract is ">").
	out, err = runScreenshotArgs(t, d.HTTPPort(), "--source", "game",
		"--baseline", basePath, "--diff-threshold", "1")
	if err != nil {
		t.Fatalf("diff_ratio == threshold must pass: %v\n%s", err, out)
	}
	if decodeScreenshotOutput(t, out)["diff_passed"] != true {
		t.Errorf("boundary pass missing diff_passed: %s", out)
	}
}

// TestScreenshotBaselineSizeMismatchNamesBothSizes: a dimension mismatch is
// BASELINE_DIFF_FAILED with both sizes spelled out.
func TestScreenshotBaselineSizeMismatchNamesBothSizes(t *testing.T) {
	d, _ := startScreenshotDaemon(t, true)
	basePath := pngFile(t, t.TempDir(), "wide.png", solidPNG(3, 1, color.RGBA{0, 0, 0, 0xFF}))

	out, err := runScreenshotArgs(t, d.HTTPPort(), "--source", "game", "--baseline", basePath)
	if err == nil {
		t.Fatal("a size mismatch must exit non-zero")
	}
	if !strings.Contains(out, "BASELINE_DIFF_FAILED") || !strings.Contains(out, "2x1") || !strings.Contains(out, "3x1") {
		t.Errorf("output = %s", out)
	}
	data := decodeScreenshotOutput(t, out)["error"].(map[string]any)["data"].(map[string]any)
	if fmt.Sprint(data["baseline_size"]) != "[3 1]" || fmt.Sprint(data["image_size"]) != "[2 1]" {
		t.Errorf("sizes = %v / %v", data["baseline_size"], data["image_size"])
	}
}

// TestScreenshotBaselineDiffOutAnnotates: --diff-out writes the annotated PNG
// (differing pixels marked #FF00FF) even on the failing path.
func TestScreenshotBaselineDiffOutAnnotates(t *testing.T) {
	d, _ := startScreenshotDaemon(t, true)
	dir := t.TempDir()
	basePath := pngFile(t, dir, "black.png", solidPNG(2, 1, color.RGBA{0, 0, 0, 0xFF}))
	diffPath := filepath.Join(dir, "nested", "diff.png")

	out, err := runScreenshotArgs(t, d.HTTPPort(), "--source", "game",
		"--baseline", basePath, "--diff-out", diffPath)
	if err == nil {
		t.Fatalf("expected BASELINE_DIFF_FAILED, got: %s", out)
	}
	if !strings.Contains(out, "diff.png") {
		t.Errorf("failure payload must name the diff artifact: %s", out)
	}
	f, err := os.Open(diffPath)
	if err != nil {
		t.Fatalf("--diff-out file missing: %v", err)
	}
	defer f.Close()
	annotated, err := png.Decode(f)
	if err != nil {
		t.Fatalf("--diff-out is not a PNG: %v", err)
	}
	r, g, b, _ := annotated.At(0, 0).RGBA()
	if int(r>>8) != 0xFF || int(g>>8) != 0 || int(b>>8) != 0xFF {
		t.Errorf("differing pixel = #%02X%02X%02X, want #FF00FF", r>>8, g>>8, b>>8)
	}
}

// TestScreenshotBaselineUsesFinalImage: the diff runs on the image after
// --region (and --max-resolution) are applied, not on the raw frame.
func TestScreenshotBaselineUsesFinalImage(t *testing.T) {
	d, _ := startScreenshotDaemon(t, true)
	cropped := solidPNG(1, 1, color.RGBA{0xFF, 0, 0, 0xFF})
	basePath := pngFile(t, t.TempDir(), "crop.png", cropped)

	out, err := runScreenshotArgs(t, d.HTTPPort(), "--source", "game",
		"--region", "1,0,1,1", "--baseline", basePath)
	if err != nil {
		t.Fatalf("baseline against the cropped frame: %v\n%s", err, out)
	}
	if decodeScreenshotOutput(t, out)["diff_ratio"].(float64) != 0 {
		t.Errorf("the cropped pixel is identical to the baseline: %s", out)
	}
}

// TestScreenshotBaselineParamErrors: the flag matrix fails fast with
// INVALID_PARAMS / BASELINE_READ_FAILED before any capture.
func TestScreenshotBaselineParamErrors(t *testing.T) {
	d, plugin := startScreenshotDaemon(t, true)

	// --diff-out without --baseline
	out, err := runScreenshotArgs(t, d.HTTPPort(), "--source", "game", "--diff-out", "x.png")
	if err == nil || !strings.Contains(out, "INVALID_PARAMS") {
		t.Errorf("--diff-out without --baseline = %v\n%s", err, out)
	}
	// threshold outside [0,1]
	out, err = runScreenshotArgs(t, d.HTTPPort(), "--source", "game",
		"--baseline", pngFile(t, t.TempDir(), "b.png", solidPNG(2, 1, color.RGBA{})),
		"--diff-threshold", "1.5")
	if err == nil || !strings.Contains(out, "[0,1]") {
		t.Errorf("--diff-threshold 1.5 = %v\n%s", err, out)
	}
	// unreadable baseline
	out, err = runScreenshotArgs(t, d.HTTPPort(), "--source", "game",
		"--baseline", filepath.Join(t.TempDir(), "missing.png"))
	if err == nil || !strings.Contains(out, "BASELINE_READ_FAILED") {
		t.Errorf("missing baseline = %v\n%s", err, out)
	}
	if got := plugin.Count("take_screenshot"); got != 0 {
		t.Errorf("param errors must not capture anything (%d calls)", got)
	}
}
