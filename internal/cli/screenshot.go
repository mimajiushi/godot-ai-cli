// editor screenshot's CLI-side extras: a GAME_NOT_RUNNING preflight for
// --source game, --out file saving, and --assert pixel verification. The
// wire contract (take_screenshot params) is untouched — everything here
// post-processes the daemon's response locally.
package cli

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	imganalysis "github.com/mimajiushi/godot-ai-cli/internal/image"
	"github.com/mimajiushi/godot-ai-cli/internal/ops"
)

// --coords 的两个取值：image = 源图像像素（历史行为，默认），
// canvas = 游戏画布坐标（按回包的 canvas_scale 换算成图像像素）。
const (
	coordSpaceImage  = "image"
	coordSpaceCanvas = "canvas"
)

// baselineSampleLimit 是基线对比失败时在 data.samples 里回显的差异像素上限。
const baselineSampleLimit = 8

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

	// --coords canvas：--region/--assert 的坐标是画布坐标，需要按回包的
	// canvas_scale 换算成源图像像素（换算在拿到响应后做，见下文）。
	coords, _ := cmd.Flags().GetString("coords")
	if coords != coordSpaceImage && coords != coordSpaceCanvas {
		return jsonError(cmd, "INVALID_PARAMS",
			fmt.Sprintf("--coords: want %q or %q, got %q", coordSpaceImage, coordSpaceCanvas, coords), nil)
	}

	// --baseline/--diff-out/--diff-threshold：本地逐像素基线对比（R-2）。
	baselinePath, _ := cmd.Flags().GetString("baseline")
	diffOutPath, _ := cmd.Flags().GetString("diff-out")
	diffThreshold, _ := cmd.Flags().GetFloat64("diff-threshold")
	baselineSet := baselinePath != ""
	if diffOutPath != "" && !baselineSet {
		return jsonError(cmd, "INVALID_PARAMS", "--diff-out requires --baseline <png>", nil)
	}
	if diffThreshold < 0 || diffThreshold > 1 {
		return jsonError(cmd, "INVALID_PARAMS",
			fmt.Sprintf("--diff-threshold must be within [0,1], got %v", diffThreshold), nil)
	}
	var baseline image.Image
	if baselineSet {
		// 基线先读先解码：文件缺失/损坏在拍照与断言之前就报，不浪费一次截图。
		img, err := imganalysis.Load(baselinePath)
		if err != nil {
			return jsonError(cmd, "BASELINE_READ_FAILED",
				fmt.Sprintf("--baseline %s: %v", baselinePath, err), nil)
		}
		baseline = img
	}

	// --full-res overrides the CLI's default 640 cap; plugin-side 0 means no cap.
	if fullRes, _ := cmd.Flags().GetBool("full-res"); fullRes {
		params["max_resolution"] = 0
	}

	// --region crops in SOURCE-image pixels, so the capture must arrive
	// uncapped; the crop runs locally and --max-resolution applies afterwards.
	regionStr, _ := cmd.Flags().GetString("region")
	var region [4]int
	if regionStr != "" {
		r, err := parseRegion(regionStr)
		if err != nil {
			return jsonError(cmd, "INVALID_PARAMS", fmt.Sprintf("--region: %v", err), nil)
		}
		region = r
		params["max_resolution"] = 0
	}

	// --coords canvas 的换算比例是"画布 → 源图像像素"，任何降采样都会让映射
	// 失真，所以强制整帧取回（与 --region 同一条理由）。
	if coords == coordSpaceCanvas {
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
	regionSet := regionStr != ""
	if status, _ := resp["status"].(string); status != "ok" || (outPath == "" && len(assertions) == 0 && !regionSet && !baselineSet) {
		// 纯 --coords canvas（没有本地裁剪/断言要换算）走这条老路径：图像原样
		// 返回，只把坐标空间与比例如实回显，绝不改动既有字段。
		if coords == coordSpaceCanvas {
			if data, ok := resp["data"].(map[string]any); ok {
				if scale, ok := canvasScaleFrom(data); ok {
					data["coord_space"] = coordSpaceCanvas
					data["scale"] = scale[0]
				}
			}
		}
		return printExecuteResponse(cmd, resp) // legacy path, unchanged
	}

	data, _ := resp["data"].(map[string]any)
	b64, _ := data["image_base64"].(string)
	raw, err := decodeDataURI(b64)
	if err != nil {
		return jsonError(cmd, "SCREENSHOT_DECODE_FAILED", err.Error(), nil)
	}

	// --coords canvas：画布模式下 --region 与 --assert 都用**绝对画布坐标**。
	// --region 先乘 canvas_scale 换成整帧像素矩形再本地裁剪；--assert 同样先乘
	// canvas_scale 得到整帧像素点，regionSet 时再减掉裁剪原点——取样发生裁剪后的
	// 图上，不减原点就会静默取到别处的像素（原点非 0 时必错，原点为 0 时看不出）。
	// 比例只存在于回包里（插件 3.1 起），缺失即说明版本不匹配，直接报错而不是猜。
	assertMap := func(x, y int) (int, int) { return x, y }
	if coords == coordSpaceCanvas {
		scale, ok := canvasScaleFrom(data)
		if !ok {
			return jsonError(cmd, "COORD_SPACE_UNAVAILABLE",
				"the capture carries no canvas_scale — --coords canvas needs a plugin that reports it (see `godot-ai-cli -v`); retry with --coords image",
				map[string]any{"hint": "reinstall the bundled plugin with `plugin install --project <dir>` and restart the editor"})
		}
		// 裁剪原点（整帧像素）；无 --region 时为 0，等价于不偏移。
		cropOriginX, cropOriginY := 0, 0
		if regionSet {
			canvasRegion := region
			region = canvasToImageRect(canvasRegion, scale)
			cropOriginX, cropOriginY = region[0], region[1]
			data["canvas_region"] = []int{canvasRegion[0], canvasRegion[1], canvasRegion[2], canvasRegion[3]}
			data["image_region"] = []int{region[0], region[1], region[2], region[3]}
		}
		originX, originY := cropOriginX, cropOriginY
		assertMap = func(x, y int) (int, int) {
			ix, iy := canvasToImagePoint(x, y, scale)
			return ix - originX, iy - originY
		}
		data["coord_space"] = coordSpaceCanvas
		data["scale"] = scale[0]
	}

	if regionSet {
		// 先按原始帧缓冲裁剪，再套用 --max-resolution 上限（本地最近邻缩放，
		// 像素画不糊）；响应里的 width/height 反映最终图，original_* 保持整帧。
		img, err := imganalysis.DecodeBytes(raw)
		if err != nil {
			return jsonError(cmd, "SCREENSHOT_DECODE_FAILED", err.Error(), nil)
		}
		cropped, err := imganalysis.Crop(img, region[0], region[1], region[2], region[3])
		if err != nil {
			return jsonError(cmd, "INVALID_PARAMS", fmt.Sprintf("--region: %v", err), nil)
		}
		maxRes, _ := cmd.Flags().GetInt("max-resolution")
		if fullRes, _ := cmd.Flags().GetBool("full-res"); fullRes {
			maxRes = 0
		}
		// --coords canvas：裁剪后不得再降采样，否则回包的 image_region/scale
		// 与返回图上的像素就不再一一对应（canvas 坐标空间下每次采样都要可复算）。
		if coords == coordSpaceCanvas {
			maxRes = 0
		}
		final := imganalysis.DownscaleNearest(cropped, maxRes)
		var enc bytes.Buffer
		if err := png.Encode(&enc, final); err != nil {
			return jsonError(cmd, "SCREENSHOT_ENCODE_FAILED", err.Error(), nil)
		}
		raw = enc.Bytes()
		data["width"] = final.Bounds().Dx()
		data["height"] = final.Bounds().Dy()
		data["region"] = []int{region[0], region[1], region[2], region[3]}
		data["image_base64"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)
	}

	// R-2 基线对比：拿"最终图"（--region/--max-resolution 都应用之后）与基线
	// 逐像素比对。尺寸不一致与超阈值都报 BASELINE_DIFF_FAILED，但 data 形状
	// 不同（前者给两边尺寸，后者给 diff_ratio/threshold/采样点）。
	if baselineSet {
		cur, err := imganalysis.DecodeBytes(raw)
		if err != nil {
			return jsonError(cmd, "SCREENSHOT_DECODE_FAILED", err.Error(), nil)
		}
		diffImg, stats, err := imganalysis.DiffImages(cur, baseline, baselineSampleLimit)
		if err != nil {
			return jsonError(cmd, "BASELINE_DIFF_FAILED", err.Error(), map[string]any{
				"baseline":      baselinePath,
				"baseline_size": []int{baseline.Bounds().Dx(), baseline.Bounds().Dy()},
				"image_size":    []int{cur.Bounds().Dx(), cur.Bounds().Dy()},
				"hint":          "match the baseline resolution with --max-resolution/--full-res, or regenerate the baseline",
			})
		}
		data["baseline"] = baselinePath
		data["diff_ratio"] = stats.DiffRatio
		data["diff_pixels"] = stats.DiffPixels
		data["total_pixels"] = stats.TotalPixels
		data["diff_threshold"] = diffThreshold
		if diffOutPath != "" {
			abs, n, err := writeImageFile(diffOutPath, diffImg)
			if err != nil {
				return jsonError(cmd, "SCREENSHOT_SAVE_FAILED", err.Error(), nil)
			}
			data["diff_out"] = abs
			data["diff_out_bytes"] = n
		}
		if stats.DiffRatio > diffThreshold {
			failData := map[string]any{
				"baseline":     baselinePath,
				"diff_ratio":   stats.DiffRatio,
				"threshold":    diffThreshold,
				"diff_pixels":  stats.DiffPixels,
				"total_pixels": stats.TotalPixels,
				"samples":      stats.SamplePoints,
			}
			if out, ok := data["diff_out"]; ok {
				failData["diff_out"] = out
			}
			return jsonError(cmd, "BASELINE_DIFF_FAILED",
				fmt.Sprintf("diff_ratio %.6f exceeds --diff-threshold %g (%d of %d pixels differ)",
					stats.DiffRatio, diffThreshold, stats.DiffPixels, stats.TotalPixels), failData)
		}
		data["diff_passed"] = true
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
		samples, passed, err := checkAssertions(img, assertions, tolerance, assertMap)
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

// canvasScaleFrom 从截图回包里读 canvas_scale（"画布坐标 → 截图像素"的比例对）。
// 字段缺失或退化（旧插件、或本来就没有 stretch 关系的源）时 ok=false——调用方
// 必须据此报错，绝不自己猜一个比例。
func canvasScaleFrom(data map[string]any) ([2]float64, bool) {
	raw, ok := data["canvas_scale"].([]any)
	if !ok || len(raw) != 2 {
		return [2]float64{}, false
	}
	sx, okX := raw[0].(float64)
	sy, okY := raw[1].(float64)
	if !okX || !okY || sx <= 0 || sy <= 0 {
		return [2]float64{}, false
	}
	return [2]float64{sx, sy}, true
}

// canvasToImageRect 把 "x,y,w,h" 的画布矩形换算成整帧图像像素：原点与宽高都
// 向下取整（需求钉死的契约——画布 562,262,60,60 在 1.666667 下 → 图像
// 936,436,100,100），宽高至少保留 1 像素，免得小区域被取整吞成空矩形。
func canvasToImageRect(r [4]int, scale [2]float64) [4]int {
	return [4]int{
		int(math.Floor(float64(r[0]) * scale[0])),
		int(math.Floor(float64(r[1]) * scale[1])),
		max(1, int(math.Floor(float64(r[2])*scale[0]))),
		max(1, int(math.Floor(float64(r[3])*scale[1]))),
	}
}

// canvasToImagePoint 把单个画布坐标换算成**整帧**图像像素（向下取整，与
// canvasToImageRect 同一条规则）。注意它不带裁剪偏移：断言取样的是裁剪后的图，
// 所以 canvas 模式的 assertMap 还要再减掉裁剪原点（见 runScreenshot 里的说明）。
func canvasToImagePoint(x, y int, scale [2]float64) (int, int) {
	return int(math.Floor(float64(x) * scale[0])), int(math.Floor(float64(y) * scale[1]))
}

// writeImageFile 把图片编码成 PNG 写入 path（自动创建父目录），返回绝对路径
// 与实际写入的字节数（差异标注图与 --out 落盘共用）。
func writeImageFile(path string, img image.Image) (string, int, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", 0, err
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", 0, err
		}
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return "", 0, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return abs, buf.Len(), nil
}

// parseRegion parses an "x,y,w,h" region string into its four ints.
func parseRegion(raw string) ([4]int, error) {
	var out [4]int
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return out, fmt.Errorf("want \"x,y,w,h\", got %q", raw)
	}
	for i, p := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return out, fmt.Errorf("%q is not an integer", p)
		}
		out[i] = v
	}
	return out, nil
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
// probed pixel per channel against the tolerance. mapPoint (nil = identity)
// converts the assertion coordinate into the pixel coordinate of the image
// being probed — the cropped image when --region is set, which is why the
// canvas-mode mapper subtracts the crop origin; the converted point is what
// gets sampled and reported back in samples[].at.
func checkAssertions(img image.Image, assertions []string, tolerance int, mapPoint func(int, int) (int, int)) ([]assertSample, bool, error) {
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
		if mapPoint != nil {
			x, y = mapPoint(x, y)
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
