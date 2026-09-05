// editor screenshot's CLI-side extras: a GAME_NOT_RUNNING preflight for
// --source game, --out file saving, and --assert pixel verification. The
// wire contract (take_screenshot params) is untouched — everything here
// post-processes the daemon's response locally.
package cli

import (
	"encoding/base64"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	imganalysis "github.com/mimajiushi/godot-ai-cli/internal/image"
	"github.com/mimajiushi/godot-ai-cli/internal/ops"
)

// assertSample is one checked pixel of an --assert verification.
type assertSample struct {
	At       [2]int `json:"at"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	OK       bool   `json:"ok"`
}

// runScreenshot is the RunE of `editor screenshot`: collectParams plus the
// CLI-side extras the plain op path does not have.
func runScreenshot(cmd *cobra.Command, op ops.OpSpec) error {
	params, err := collectParams(cmd, op)
	if err != nil {
		return jsonError(cmd, "INVALID_PARAMS", err.Error(), nil)
	}

	// --full-res overrides the CLI's default 640 cap; plugin-side 0 means no cap.
	if fullRes, _ := cmd.Flags().GetBool("full-res"); fullRes {
		params["max_resolution"] = 0
	}

	// source=game against a non-running game used to surface the editor
	// viewport's error (misleading — the viewport state is irrelevant).
	// Preflight with get_editor_state and report the real cause; the
	// plugin's own guard stays as backstop for older skews.
	if params["source"] == "game" {
		state, err := executeOpRaw(cmd, "get_editor_state", map[string]any{}, ops.DefaultTimeout, false)
		if err != nil {
			return err // daemon unreachable; already enveloped
		}
		if status, _ := state["status"].(string); status == "ok" {
			if data, ok := state["data"].(map[string]any); ok {
				if playing, _ := data["is_playing"].(bool); !playing {
					return jsonError(cmd, "GAME_NOT_RUNNING",
						"the game is not running - start it first with `godot-ai-cli project run`, then retry (use --source viewport/viewport_2d for the editor viewport)",
						map[string]any{"retryable": true})
				}
			}
		}
		// A non-ok editor state: let take_screenshot report its own error.
	}

	resp, err := executeOpRaw(cmd, op.PluginCommand, params, op.Timeout, op.Write)
	if err != nil {
		return err
	}
	outPath, _ := cmd.Flags().GetString("out")
	assertions, _ := cmd.Flags().GetStringArray("assert")
	tolerance, _ := cmd.Flags().GetInt("tolerance")
	if status, _ := resp["status"].(string); status != "ok" || (outPath == "" && len(assertions) == 0) {
		return printExecuteResponse(cmd, resp) // legacy path, unchanged
	}

	data, _ := resp["data"].(map[string]any)
	b64, _ := data["image_base64"].(string)
	raw, err := decodeDataURI(b64)
	if err != nil {
		return jsonError(cmd, "SCREENSHOT_DECODE_FAILED", err.Error(), nil)
	}
	// --out/--assert consume the image locally; the bulky base64 never
	// reaches stdout in that mode.
	delete(data, "image_base64")

	if outPath != "" {
		if dir := filepath.Dir(outPath); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return jsonError(cmd, "SCREENSHOT_SAVE_FAILED", err.Error(), nil)
			}
		}
		if err := os.WriteFile(outPath, raw, 0o644); err != nil {
			return jsonError(cmd, "SCREENSHOT_SAVE_FAILED", err.Error(), nil)
		}
		abs, err := filepath.Abs(outPath)
		if err != nil {
			abs = outPath
		}
		data["saved"] = abs
		data["bytes"] = len(raw)
	}

	if len(assertions) > 0 {
		img, err := imganalysis.DecodeBytes(raw)
		if err != nil {
			return jsonError(cmd, "SCREENSHOT_DECODE_FAILED", err.Error(), nil)
		}
		samples, passed, err := checkAssertions(img, assertions, tolerance)
		if err != nil {
			return jsonError(cmd, "INVALID_PARAMS", err.Error(), nil)
		}
		data["passed"] = passed
		data["samples"] = samples
		if !passed {
			return jsonError(cmd, "PIXEL_ASSERT_FAILED",
				fmt.Sprintf("%d of %d pixel assertions failed (tolerance %d)", countFailed(samples), len(samples), tolerance),
				map[string]any{"samples": samples})
		}
	}
	return printJSON(cmd.OutOrStdout(), data, prettyOutput)
}

// decodeDataURI decodes a base64 payload, stripping an optional data-URI
// prefix ("data:image/png;base64,").
func decodeDataURI(s string) ([]byte, error) {
	if i := strings.Index(s, ","); i >= 0 && strings.HasPrefix(s, "data:") {
		s = s[i+1:]
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("image_base64 is not valid base64: %v", err)
	}
	return raw, nil
}

// checkAssertions parses each "#RRGGBB@x,y" assertion and compares the
// probed pixel per channel against the tolerance.
func checkAssertions(img image.Image, assertions []string, tolerance int) ([]assertSample, bool, error) {
	var samples []assertSample
	for _, raw := range assertions {
		hexPart, xyPart, found := strings.Cut(raw, "@")
		if !found {
			return nil, false, fmt.Errorf("--assert %q: want '#RRGGBB@x,y'", raw)
		}
		er, eg, eb, err := imganalysis.ParseHexColor(hexPart)
		if err != nil {
			return nil, false, fmt.Errorf("--assert %q: %v", raw, err)
		}
		x, y, err := parsePair(xyPart, ",")
		if err != nil {
			return nil, false, fmt.Errorf("--assert %q: %v", raw, err)
		}
		probed, err := imganalysis.Probe(img, [][2]int{{x, y}})
		if err != nil {
			return nil, false, fmt.Errorf("--assert %q: %v", raw, err)
		}
		got := probed[0].RGBA
		ok := absDiff(got[0], er) <= tolerance &&
			absDiff(got[1], eg) <= tolerance &&
			absDiff(got[2], eb) <= tolerance
		samples = append(samples, assertSample{
			At:       [2]int{x, y},
			Expected: fmt.Sprintf("#%02X%02X%02X", er, eg, eb),
			Actual:   probed[0].Hex,
			OK:       ok,
		})
	}
	passed := countFailed(samples) == 0
	return samples, passed, nil
}

// absDiff is the per-channel distance used by --tolerance.
func absDiff(a, b int) int {
	if a < b {
		return b - a
	}
	return a - b
}

// countFailed tallies the failed assertion samples.
func countFailed(samples []assertSample) int {
	n := 0
	for _, s := range samples {
		if !s.OK {
			n++
		}
	}
	return n
}
