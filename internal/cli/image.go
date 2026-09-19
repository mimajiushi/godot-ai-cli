// The `image` command group: local image analysis (palette extraction and
// pixel probes) that needs no editor, daemon, or third-party runtime.
// Covers the "analyze texture colors / verify rendered pixels" workflow that
// previously forced agents to write ad-hoc Python+PIL scripts.
package cli

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	imganalysis "github.com/mimajiushi/godot-ai-cli/internal/image"
)

// newImageCommand groups the local image-analysis subcommands.
func newImageCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "image",
		Short: "Local image analysis (palette, pixel probes, grid detect, cell occupancy) — no editor required",
	}
	cmd.AddCommand(newImagePaletteCommand())
	cmd.AddCommand(newImageProbeCommand())
	cmd.AddCommand(newImageGridDetectCommand())
	cmd.AddCommand(newImageCellsCommand())
	cmd.AddCommand(newImageViewCommand())
	return cmd
}

// parseCellSpec 解析 "16x16" / "16,16" 形式的格子尺寸。
func parseCellSpec(raw string) (int, int, error) {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == 'x' || r == 'X' || r == ',' })
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("want WxH, e.g. 16x16, got %q", raw)
	}
	w, err1 := strconv.Atoi(parts[0])
	h, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("want integer WxH, e.g. 16x16, got %q", raw)
	}
	if w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("cell must be positive, got %dx%d", w, h)
	}
	return w, h, nil
}

// parseOffsetSpec 解析 "x,y" 形式的偏移。
func parseOffsetSpec(raw string) (int, int, error) {
	parts := strings.Split(raw, ",")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("want x,y, e.g. 0,0, got %q", raw)
	}
	x, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	y, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("want integer x,y, got %q", raw)
	}
	return x, y, nil
}

// newImageCellsCommand 输出逐格 alpha 占用与包围盒（像素画图集切格校验）。
func newImageCellsCommand() *cobra.Command {
	var (
		path     string
		project  string
		cell     string
		offset   string
		region   string
		alphaThr float64
	)
	cmd := &cobra.Command{
		Use:   "cells --path <file> [--cell 16x16]",
		Short: "Per-cell alpha occupancy and bounding boxes for a sprite sheet grid",
		Long: `image cells slices the sheet (or --region) into a fixed grid and reports,
per cell: opaque pixel count, the cell-local bounding box, and whether the bbox
touches a cell edge (a hint that the art spans cells or got clipped).

--cell 缺省时回退到 grid-detect 的最佳候选；候选为空则报错并提示显式给 --cell。
与 image view 联动：cells 报出可疑格子后 image view --region <该格> --scale 8 目视确认。

Examples:
  godot-ai-cli image cells --path res://resources/texture/items.png --project . --cell 16x16
  godot-ai-cli image cells --path sheet.png --cell 32x32 --offset 1,1 --region 0,0,128,64`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := resolveImagePath(path, project)
			if err != nil {
				return jsonError(cmd, "INVALID_PARAMS", err.Error(), nil)
			}
			img, err := imganalysis.Load(resolved)
			if err != nil {
				return jsonError(cmd, "IMAGE_LOAD_FAILED", err.Error(), nil)
			}
			if region != "" {
				x, y, w, h, rerr := parseRect(region)
				if rerr != nil {
					return jsonError(cmd, "INVALID_PARAMS",
						fmt.Sprintf("--region %q: %v (want x,y,w,h)", region, rerr), nil)
				}
				img, err = imganalysis.Crop(img, x, y, w, h)
				if err != nil {
					return jsonError(cmd, "INVALID_PARAMS", fmt.Sprintf("--region: %v", err), nil)
				}
			}
			ox, oy := 0, 0
			if offset != "" {
				ox, oy, err = parseOffsetSpec(offset)
				if err != nil {
					return jsonError(cmd, "INVALID_PARAMS", fmt.Sprintf("--offset: %v", err), nil)
				}
			}
			var cw, ch int
			if cell != "" {
				cw, ch, err = parseCellSpec(cell)
				if err != nil {
					return jsonError(cmd, "INVALID_PARAMS", fmt.Sprintf("--cell: %v", err), nil)
				}
			} else {
				// 回退：复用 grid-detect 的最佳候选。
				detect := imganalysis.GridDetect(img, alphaThr)
				if len(detect.SuggestedGrids) == 0 {
					return jsonError(cmd, "GRID_UNDETECTED",
						"no grid candidate — pass --cell WxH explicitly (see `image grid-detect` for clusters)", nil)
				}
				cw = detect.SuggestedGrids[0].Cell[0]
				ch = detect.SuggestedGrids[0].Cell[1]
				if offset == "" {
					ox = detect.SuggestedGrids[0].Offset[0]
					oy = detect.SuggestedGrids[0].Offset[1]
				}
			}
			res := imganalysis.Cells(img, cw, ch, ox, oy, alphaThr)
			return printJSON(cmd.OutOrStdout(), map[string]any{
				"path":           path,
				"size":           res.Size,
				"cell":           res.Cell,
				"offset":         res.Offset,
				"cols":           res.Cols,
				"rows":           res.Rows,
				"cells":          res.Cells,
				"empty_cells":    res.EmptyCells,
				"touching_cells": res.TouchingCells,
			}, prettyOutput)
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "image file (disk path or res://)")
	cmd.Flags().StringVar(&project, "project", "", "Godot project dir for res:// paths (default: project of the last launch)")
	cmd.Flags().StringVar(&cell, "cell", "", "forced cell size WxH, e.g. 16x16 (default: best grid-detect candidate)")
	cmd.Flags().StringVar(&offset, "offset", "", "grid origin x,y in pixels (default 0,0)")
	cmd.Flags().StringVar(&region, "region", "", "only inspect a sub-region: x,y,w,h in source-image pixels")
	cmd.Flags().Float64Var(&alphaThr, "alpha-threshold", 0.03, "alpha above this counts as opaque (0..1; 0.03 ≈ alpha>8/255)")
	_ = cmd.MarkFlagRequired("path")
	return cmd
}

// newImageViewCommand renders a zoomed (nearest-neighbor) view of a PNG —
// the "eyeball this 16x16 pixel-art frame" step after a screenshot, where
// the source is too small to inspect at 1x. Optional --region crops in
// source-image pixels before the upscale.
func newImageViewCommand() *cobra.Command {
	var (
		path    string
		project string
		scale   int
		region  string
		out     string
	)
	cmd := &cobra.Command{
		Use:   "view --path <file> --out <file.png>",
		Short: "Write an upscaled (nearest-neighbor) view of a PNG/JPEG for visual inspection",
		Long: `image view crops an optional --region (source-image pixels) and then
upscales by an integer --scale with nearest-neighbor sampling, so pixel-art
frames stay crisp at inspection size. The result is written to --out as PNG.

Examples:
  godot-ai-cli image view --path shots/frame.png --out shots/frame_big.png
  godot-ai-cli image view --path sheet.png --region 48,0,16,16 --scale 8 --out tile.png`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := resolveImagePath(path, project)
			if err != nil {
				return jsonError(cmd, "INVALID_PARAMS", err.Error(), nil)
			}
			img, err := imganalysis.Load(resolved)
			if err != nil {
				return jsonError(cmd, "IMAGE_LOAD_FAILED", err.Error(), nil)
			}
			if region != "" {
				x, y, w, h, err := parseRect(region)
				if err != nil {
					return jsonError(cmd, "INVALID_PARAMS",
						fmt.Sprintf("--region %q: %v (want x,y,w,h, e.g. 48,0,16,16)", region, err), nil)
				}
				img, err = imganalysis.Crop(img, x, y, w, h)
				if err != nil {
					return jsonError(cmd, "INVALID_PARAMS", fmt.Sprintf("--region: %v", err), nil)
				}
			}
			if scale < 1 {
				return jsonError(cmd, "INVALID_PARAMS",
					fmt.Sprintf("--scale must be >= 1, got %d", scale), nil)
			}
			view := imganalysis.UpscaleNearest(img, scale)
			if err := writePNG(out, view); err != nil {
				return jsonError(cmd, "IMAGE_WRITE_FAILED", err.Error(), nil)
			}
			b := view.Bounds()
			return printJSON(cmd.OutOrStdout(), map[string]any{
				"path":   path,
				"out":    out,
				"scale":  scale,
				"region": region,
				"size":   [2]int{b.Dx(), b.Dy()},
			}, prettyOutput)
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "image file (disk path or res://)")
	cmd.Flags().StringVar(&project, "project", "", "Godot project dir for res:// paths (default: project of the last launch)")
	cmd.Flags().IntVar(&scale, "scale", 4, "integer magnification factor (nearest-neighbor)")
	cmd.Flags().StringVar(&region, "region", "", "crop first: x,y,w,h in source-image pixels")
	cmd.Flags().StringVar(&out, "out", "", "output PNG path")
	_ = cmd.MarkFlagRequired("path")
	_ = cmd.MarkFlagRequired("out")
	return cmd
}

// newImageGridDetectCommand infers a sprite sheet's real frame grid from its
// alpha silhouette — the "how was this sheet really cut" question (a wrong
// arithmetic assumption like 384÷32=12 breaks SpriteFrames slicing when the
// art is actually 8 paired columns at a 48 px pitch).
func newImageGridDetectCommand() *cobra.Command {
	var (
		path           string
		project        string
		alphaThreshold float64
		cell           string
		cols           int
		rows           int
	)
	cmd := &cobra.Command{
		Use:   "grid-detect --path <file>",
		Short: "Detect a sprite sheet's frame grid from alpha clusters",
		Long: `image grid-detect scans a sheet for columns/rows carrying any pixel
above the alpha threshold, clusters them, and suggests plausible grids
(cell size + frame count + offset) under which every cluster fits in one
cell and every cell holds art.

满行/满列排布的像素 UI 图集（长进度条、边框、跨格连续图元）没有全空分隔
行/列，聚簇切不开 → suggested_grids 为空。此时输出会给 reason/hints 与
factor_candidates（整除 W×H 的常见格尺寸），并可用强制栅格校验：
  --cell 16x16          按给定格子逐格体检（等价 image cells）
  --cols 4 --rows 5     按列×行数反推格子尺寸（宽高须整除）
两者同时给时校验自洽。

Examples:
  godot-ai-cli image grid-detect --path res://resources/texture/drone_pair.png
  godot-ai-cli image grid-detect --path sheet.png --cell 16x16
  godot-ai-cli image grid-detect --path sheet.png --cols 4 --rows 5`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := resolveImagePath(path, project)
			if err != nil {
				return jsonError(cmd, "INVALID_PARAMS", err.Error(), nil)
			}
			img, err := imganalysis.Load(resolved)
			if err != nil {
				return jsonError(cmd, "IMAGE_LOAD_FAILED", err.Error(), nil)
			}
			b := img.Bounds()
			w, h := b.Dx(), b.Dy()

			// 强制栅格模式：--cell 与 --cols/--rows 同时给时先校验自洽。
			var cw, ch int
			forced := cell != "" || cols > 0 || rows > 0
			if cell != "" {
				cw, ch, err = parseCellSpec(cell)
				if err != nil {
					return jsonError(cmd, "INVALID_PARAMS", fmt.Sprintf("--cell: %v", err), nil)
				}
			}
			if cols > 0 || rows > 0 {
				if cols <= 0 || rows <= 0 {
					return jsonError(cmd, "INVALID_PARAMS",
						"--cols and --rows must be given together and both > 0", nil)
				}
				if w%cols != 0 || h%rows != 0 {
					return jsonError(cmd, "GRID_MISMATCH",
						fmt.Sprintf("%dx%d is not divisible by %d cols x %d rows", w, h, cols, rows), nil)
				}
				if cw == 0 {
					cw, ch = w/cols, h/rows
				} else if cw != w/cols || ch != h/rows {
					return jsonError(cmd, "GRID_MISMATCH",
						fmt.Sprintf("--cell %dx%d inconsistent with --cols %d --rows %d (%dx%d implies %dx%d)",
							cw, ch, cols, rows, w, h, w/cols, h/rows), nil)
				}
			}
			if forced {
				res := imganalysis.Cells(img, cw, ch, 0, 0, alphaThreshold)
				return printJSON(cmd.OutOrStdout(), map[string]any{
					"path":           path,
					"size":           res.Size,
					"cell":           res.Cell,
					"cols":           res.Cols,
					"rows":           res.Rows,
					"cells":          res.Cells,
					"empty_cells":    res.EmptyCells,
					"touching_cells": res.TouchingCells,
					"forced":         true,
				}, prettyOutput)
			}

			out := imganalysis.GridDetect(img, alphaThreshold)
			payload := map[string]any{
				"path":            path,
				"size":            out.Size,
				"column_clusters": out.ColumnClusters,
				"row_clusters":    out.RowClusters,
				"suggested_grids": out.SuggestedGrids,
			}
			if len(out.SuggestedGrids) == 0 {
				// 空候选时给出原因与线索，避免被误读成"工具坏了"。
				payload["reason"] = "no fully empty row/column separators — clusters merged (full-width or full-height elements), so no consistent grid could be enumerated"
				hints := []string{
					fmt.Sprintf("try: godot-ai-cli image grid-detect --path %s --cell WxH (forced-grid verification)", path),
					fmt.Sprintf("or:  godot-ai-cli image cells --path %s --cell WxH for per-cell occupancy", path),
				}
				if len(out.ColumnClusters) == 1 {
					hints = append(hints, fmt.Sprintf("column_clusters merged into %v (a full-width row element spans every column)", out.ColumnClusters[0]))
				}
				if len(out.RowClusters) == 1 {
					hints = append(hints, fmt.Sprintf("row_clusters merged into %v (a full-height column element spans every row)", out.RowClusters[0]))
				}
				payload["hints"] = hints
				// 因子候选：整除 W×H 的常见像素格尺寸（未经验证，仅给排查起点）。
				var factors []any
				for _, s := range []int{8, 16, 24, 32, 48, 64} {
					for _, s2 := range []int{8, 16, 24, 32, 48, 64} {
						if w%s == 0 && h%s2 == 0 && w/s >= 2 && h/s2 >= 2 {
							factors = append(factors, map[string]any{
								"cell": [2]int{s, s2}, "cols": w / s, "rows": h / s2,
							})
						}
					}
				}
				payload["factor_candidates"] = factors
			}
			return printJSON(cmd.OutOrStdout(), payload, prettyOutput)
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "image file (disk path or res://)")
	cmd.Flags().StringVar(&project, "project", "", "Godot project dir for res:// paths (default: project of the last launch)")
	cmd.Flags().Float64Var(&alphaThreshold, "alpha-threshold", 0.0, "pixels with alpha at or below this are ignored (0-1)")
	cmd.Flags().StringVar(&cell, "cell", "", "forced grid: verify against cell size WxH, e.g. 16x16")
	cmd.Flags().IntVar(&cols, "cols", 0, "forced grid: column count (requires --rows; sheet must divide exactly)")
	cmd.Flags().IntVar(&rows, "rows", 0, "forced grid: row count (requires --cols; sheet must divide exactly)")
	_ = cmd.MarkFlagRequired("path")
	return cmd
}

// newImagePaletteCommand extracts the dominant colors of a texture,
// optionally split per tile of a WxH grid (TileSet atlas analysis).
func newImagePaletteCommand() *cobra.Command {
	var (
		path           string
		project        string
		top            int
		alphaThreshold int
		grid           string
	)
	cmd := &cobra.Command{
		Use:   "palette --path <file>",
		Short: "Extract the dominant colors of a PNG/JPEG image",
		Long: `image palette counts the opaque pixels of a PNG/JPEG and prints the
top colors with their counts. --grid WxH additionally reports the top 3
colors of each tile, for TileSet atlas analysis.

Paths may be absolute/relative disk paths or res:// (resolved against
--project, or the project recorded by the last launch).

Examples:
  godot-ai-cli image palette --path res://resources/texture/tiles.png --project .
  godot-ai-cli image palette --path tiles.png --top 12 --grid 16x16`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := resolveImagePath(path, project)
			if err != nil {
				return jsonError(cmd, "INVALID_PARAMS", err.Error(), nil)
			}
			img, err := imganalysis.Load(resolved)
			if err != nil {
				return jsonError(cmd, "IMAGE_LOAD_FAILED", err.Error(), nil)
			}
			out := map[string]any{
				"path":    path,
				"size":    [2]int{img.Bounds().Dx(), img.Bounds().Dy()},
				"palette": imganalysis.Palette(img, top, alphaThreshold),
			}
			if grid != "" {
				w, h, err := parsePair(grid, "x")
				if err != nil {
					return jsonError(cmd, "INVALID_PARAMS",
						fmt.Sprintf("--grid: %v (want WxH, e.g. 16x16)", err), nil)
				}
				tiles, err := imganalysis.GridPalette(img, w, h, alphaThreshold)
				if err != nil {
					return jsonError(cmd, "INVALID_PARAMS", fmt.Sprintf("--grid: %v", err), nil)
				}
				out["tiles"] = tiles
			}
			return printJSON(cmd.OutOrStdout(), out, prettyOutput)
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "image file (disk path or res://)")
	cmd.Flags().StringVar(&project, "project", "", "Godot project dir for res:// paths (default: project of the last launch)")
	cmd.Flags().IntVar(&top, "top", 12, "number of dominant colors to report")
	cmd.Flags().IntVar(&alphaThreshold, "alpha-threshold", 200, "pixels with alpha below this are ignored")
	cmd.Flags().StringVar(&grid, "grid", "", "also report per-tile palettes, split as WxH cells (e.g. 16x16)")
	_ = cmd.MarkFlagRequired("path")
	return cmd
}

// newImageProbeCommand samples the pixels at the given coordinates — render
// verification on a saved screenshot (see editor screenshot --out).
func newImageProbeCommand() *cobra.Command {
	var (
		path    string
		project string
		ats     []string
	)
	cmd := &cobra.Command{
		Use:   "probe --path <file> --at x,y [--at x,y ...]",
		Short: "Sample the pixels at given coordinates of a PNG/JPEG image",
		Long: `image probe prints the color at each --at coordinate. Pair it with
"editor screenshot --out" to verify rendered colors without any scripting.

Examples:
  godot-ai-cli image probe --path shots/bg_verify.png --at 60,60 --at 1220,660`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(ats) == 0 {
				return jsonError(cmd, "INVALID_PARAMS", "at least one --at <x>,<y> is required", nil)
			}
			points := make([][2]int, 0, len(ats))
			for _, raw := range ats {
				x, y, err := parsePair(raw, ",")
				if err != nil {
					return jsonError(cmd, "INVALID_PARAMS",
						fmt.Sprintf("--at %q: %v (want x,y, e.g. 60,60)", raw, err), nil)
				}
				points = append(points, [2]int{x, y})
			}
			resolved, err := resolveImagePath(path, project)
			if err != nil {
				return jsonError(cmd, "INVALID_PARAMS", err.Error(), nil)
			}
			img, err := imganalysis.Load(resolved)
			if err != nil {
				return jsonError(cmd, "IMAGE_LOAD_FAILED", err.Error(), nil)
			}
			samples, err := imganalysis.Probe(img, points)
			if err != nil {
				return jsonError(cmd, "INVALID_PARAMS", err.Error(), nil)
			}
			return printJSON(cmd.OutOrStdout(), map[string]any{
				"path":    path,
				"size":    [2]int{img.Bounds().Dx(), img.Bounds().Dy()},
				"samples": samples,
			}, prettyOutput)
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "image file (disk path or res://)")
	cmd.Flags().StringVar(&project, "project", "", "Godot project dir for res:// paths (default: project of the last launch)")
	cmd.Flags().StringArrayVar(&ats, "at", nil, "pixel coordinate x,y (repeatable)")
	_ = cmd.MarkFlagRequired("path")
	return cmd
}

// resolveImagePath turns a res:// path into a disk path using the explicit
// --project flag or the project recorded by the last launch; plain disk
// paths pass through untouched.
func resolveImagePath(path, project string) (string, error) {
	if !strings.HasPrefix(path, "res://") {
		return path, nil
	}
	root := project
	if root == "" {
		if rec, ok := readLastDaemon(); ok && rec.Project != "" {
			root = rec.Project
		}
	}
	if root == "" {
		return "", fmt.Errorf("res:// paths need --project (or a prior launch whose project is remembered)")
	}
	return filepath.Join(root, strings.TrimPrefix(path, "res://")), nil
}

// parsePair splits "a<sep>b" into two ints.
func parsePair(raw, sep string) (int, int, error) {
	parts := strings.Split(raw, sep)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("want two numbers joined by %q, got %q", sep, raw)
	}
	a, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("%q is not an integer", parts[0])
	}
	b, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("%q is not an integer", parts[1])
	}
	return a, b, nil
}

// parseRect splits "x,y,w,h" into four ints.
func parseRect(raw string) (x, y, w, h int, err error) {
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return 0, 0, 0, 0, fmt.Errorf("want four comma-separated numbers, got %q", raw)
	}
	vals := make([]int, 4)
	for i, p := range parts {
		v, cerr := strconv.Atoi(strings.TrimSpace(p))
		if cerr != nil {
			return 0, 0, 0, 0, fmt.Errorf("%q is not an integer", p)
		}
		vals[i] = v
	}
	return vals[0], vals[1], vals[2], vals[3], nil
}

// writePNG encodes img as PNG to path, creating the parent directory.
func writePNG(path string, img image.Image) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
