// editor record's CLI-side extras: --frames/--duration×--fps resolution, a
// GAME_NOT_RUNNING preflight, and local frame post-processing (PNG files per
// frame via --out-dir, or an animated GIF via --format gif --out). The wire
// contract (record_frames params: frames + max_resolution) stays minimal.
package cli

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	imganalysis "github.com/mimajiushi/godot-ai-cli/internal/image"
	"github.com/mimajiushi/godot-ai-cli/internal/ops"
)

// runRecord is the RunE of `editor record`: collectParams plus the CLI-side
// extras the plain op path does not have.
func runRecord(cmd *cobra.Command, op ops.OpSpec) error {
	params, err := collectParams(cmd, op)
	if err != nil {
		return jsonError(cmd, "INVALID_PARAMS", err.Error(), nil)
	}

	// Frame count: --frames wins; otherwise --duration × --fps. The game side
	// only understands `frames`.
	nested, _ := params["params"].(map[string]any) // WrapOp envelope
	frames, _ := nested["frames"].(int)
	if frames <= 0 {
		duration, _ := cmd.Flags().GetFloat64("duration")
		fps, _ := cmd.Flags().GetInt("fps")
		if duration <= 0 || fps <= 0 {
			return jsonError(cmd, "INVALID_PARAMS",
				"record needs --frames <n>, or --duration <sec> with --fps <n>", nil)
		}
		frames = int(duration*float64(fps) + 0.5)
	}
	nested["frames"] = frames

	if fullRes, _ := cmd.Flags().GetBool("full-res"); fullRes {
		nested["max_resolution"] = 0
	}

	format, _ := cmd.Flags().GetString("format")
	outDir, _ := cmd.Flags().GetString("out-dir")
	outFile, _ := cmd.Flags().GetString("out")
	switch format {
	case "png":
		if outDir == "" {
			return jsonError(cmd, "INVALID_PARAMS", "--format png needs --out-dir <dir>", nil)
		}
	case "gif":
		if outFile == "" {
			return jsonError(cmd, "INVALID_PARAMS", "--format gif needs --out <file.gif>", nil)
		}
	default:
		return jsonError(cmd, "INVALID_PARAMS",
			fmt.Sprintf("unknown --format %q — use png or gif", format), nil)
	}

	// Same preflight as editor screenshot: report the real cause when the
	// game is not running instead of the game_command not-ready error.
	state, err := executeOpRaw(cmd, "get_editor_state", map[string]any{}, ops.DefaultTimeout, false)
	if err != nil {
		return err
	}
	if status, _ := state["status"].(string); status == "ok" {
		if data, ok := state["data"].(map[string]any); ok {
			if playing, _ := data["is_playing"].(bool); !playing {
				return jsonError(cmd, "GAME_NOT_RUNNING",
					"the game is not running - start it first with `godot-ai-cli project run`, then retry",
					map[string]any{"retryable": true})
			}
		}
	}

	resp, err := executeOpRaw(cmd, op.PluginCommand, params, op.Timeout, op.Write)
	if err != nil {
		return err
	}
	if status, _ := resp["status"].(string); status != "ok" {
		return printExecuteResponse(cmd, resp)
	}

	data, _ := resp["data"].(map[string]any)
	framesB64, _ := data["frames"].([]any)
	deltas, _ := data["frame_deltas_ms"].([]any)
	delete(data, "frames") // the bulky payload never reaches stdout

	images, delays, err := decodeRecordFrames(framesB64, deltas)
	if err != nil {
		return jsonError(cmd, "RECORD_DECODE_FAILED", err.Error(), nil)
	}

	switch format {
	case "png":
		files, err := saveRecordFrames(outDir, images)
		if err != nil {
			return jsonError(cmd, "RECORD_SAVE_FAILED", err.Error(), nil)
		}
		data["files"] = files
	case "gif":
		abs, size, err := saveRecordGIF(outFile, images, delays)
		if err != nil {
			return jsonError(cmd, "RECORD_SAVE_FAILED", err.Error(), nil)
		}
		data["saved"] = abs
		data["bytes"] = size
	}
	return printJSON(cmd.OutOrStdout(), data, prettyOutput)
}

// decodeRecordFrames decodes the base64 PNG frames and the millisecond
// deltas into image + delay slices for local post-processing.
func decodeRecordFrames(framesB64 []any, deltas []any) ([]image.Image, []int, error) {
	images := make([]image.Image, 0, len(framesB64))
	delays := make([]int, 0, len(deltas))
	for i, raw := range framesB64 {
		s, _ := raw.(string)
		pngBytes, err := decodeDataURI(s)
		if err != nil {
			return nil, nil, fmt.Errorf("frame %d: %v", i, err)
		}
		img, err := imganalysis.DecodeBytes(pngBytes)
		if err != nil {
			return nil, nil, fmt.Errorf("frame %d: %v", i, err)
		}
		images = append(images, img)
	}
	for _, d := range deltas {
		if f, ok := d.(float64); ok {
			delays = append(delays, int(f))
		}
	}
	return images, delays, nil
}

// saveRecordFrames writes one frame_0001.png … per frame into outDir and
// returns the absolute file list.
func saveRecordFrames(outDir string, images []image.Image) ([]string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	files := make([]string, 0, len(images))
	for i, img := range images {
		path := filepath.Join(outDir, fmt.Sprintf("frame_%04d.png", i+1))
		f, err := os.Create(path)
		if err != nil {
			return nil, err
		}
		if err := png.Encode(f, img); err != nil {
			_ = f.Close()
			return nil, err
		}
		_ = f.Close()
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		files = append(files, abs)
	}
	return files, nil
}

// saveRecordGIF encodes the frames as an animated GIF and returns the
// absolute path + byte size.
func saveRecordGIF(outFile string, images []image.Image, delays []int) (string, int, error) {
	if dir := filepath.Dir(outFile); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", 0, err
		}
	}
	f, err := os.Create(outFile)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	if err := imganalysis.EncodeGIF(f, images, delays); err != nil {
		return "", 0, err
	}
	abs, err := filepath.Abs(outFile)
	if err != nil {
		abs = outFile
	}
	info, err := os.Stat(outFile)
	if err != nil {
		return "", 0, err
	}
	return abs, int(info.Size()), nil
}
