// The `tilemap dump` / `tilemap render` leaves: local .tscn tile data
// inspection that needs no editor, daemon, or even an open scene — the
// TileMapLayer tile_map_data PackedByteArray is decoded straight from the
// scene file (see internal/tilemapfile).
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	imganalysis "github.com/mimajiushi/godot-ai-cli/internal/image"
	"github.com/mimajiushi/godot-ai-cli/internal/tilemapfile"
)

// newTilemapDumpCommand decodes TileMapLayer cells to JSON.
func newTilemapDumpCommand() *cobra.Command {
	var (
		scenePath string
		layer     string
	)
	cmd := &cobra.Command{
		Use:   "dump --scene <file.tscn>",
		Short: "Decode TileMapLayer cells of a .tscn without opening the scene (local, no editor)",
		Long: `tilemap dump parses the .tscn text directly and decodes each
TileMapLayer's tile_map_data PackedByteArray into sorted cell records
(x, y, source, atlas, alternative). --layer selects one layer by node
name; without it every layer is dumped.

Examples:
  godot-ai-cli tilemap dump --scene scenes/level1.tscn
  godot-ai-cli tilemap dump --scene scenes/level1.tscn --layer Ground`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sc, err := tilemapfile.ParseScene(scenePath)
			if err != nil {
				return jsonError(cmd, "SCENE_PARSE_FAILED", err.Error(), nil)
			}
			layers := sc.Layers
			if layer != "" {
				l, err := sc.FindLayer(layer)
				if err != nil {
					return jsonError(cmd, "LAYER_NOT_FOUND", err.Error(), nil)
				}
				layers = []*tilemapfile.Layer{l}
			}
			out := make([]map[string]any, 0, len(layers))
			for _, l := range layers {
				cells := l.Cells
				if cells == nil {
					cells = []tilemapfile.Cell{} // serialize as [], not null
				}
				out = append(out, map[string]any{"layer": l.Name, "cells": cells})
			}
			return printJSON(cmd.OutOrStdout(), map[string]any{
				"scene":  scenePath,
				"layers": out,
			}, prettyOutput)
		},
	}
	cmd.Flags().StringVar(&scenePath, "scene", "", ".tscn file to parse (disk path)")
	cmd.Flags().StringVar(&layer, "layer", "", "only this TileMapLayer node name (default: all layers)")
	_ = cmd.MarkFlagRequired("scene")
	return cmd
}

// newTilemapRenderCommand composites one TileMapLayer into a PNG map view.
func newTilemapRenderCommand() *cobra.Command {
	var (
		scenePath string
		layer     string
		out       string
	)
	cmd := &cobra.Command{
		Use:   "render --scene <file.tscn> --out <map.png>",
		Short: "Render a TileMapLayer of a .tscn to a PNG without opening the scene (local, no editor)",
		Long: `tilemap render decodes a TileMapLayer's tile_map_data, resolves the
layer's TileSet (.tres) and atlas textures through the scene's ext_resource
table, and composites the full map into one PNG (canvas = cell range x
region size, offset to non-negative). Only ONE layer is rendered — layers
are never stacked; --layer picks it (default: the first layer).
alternative != 0 cells are drawn as alternative 0, with a one-line note on
stderr.

Grid mapping (so "which cell is this output pixel" needs no arithmetic guesses):
  origin        [x,y] = the layer-pixel offset of the output's top-left corner
                (minimum cell coordinate x cell_px_size). The cell of output
                pixel (px,py) is ((origin[0]+px)/cell_px_size[0],
                (origin[1]+py)/cell_px_size[1]).
  cell_px_size  [w,h] = the pixel size of one cell (the TileSet's texture_region_size).
  layer_position [x,y] = the layer node's own position: for an unrotated layer
                the world pixel of output pixel (px,py) is
                origin + (px,py) + layer_position. A layer carrying a transform
                (rotation/scale) is not applied and is reported in warnings.

Examples:
  godot-ai-cli tilemap render --scene scenes/level1.tscn --out shots/level1.png
  godot-ai-cli tilemap render --scene scenes/level1.tscn --layer Ground --out shots/ground.png`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sc, err := tilemapfile.ParseScene(scenePath)
			if err != nil {
				return jsonError(cmd, "SCENE_PARSE_FAILED", err.Error(), nil)
			}
			res, err := tilemapfile.RenderLayer(sc, layer)
			if err != nil {
				return jsonError(cmd, "RENDER_FAILED", err.Error(), nil)
			}
			if res.AlternativeIgnored > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "note: %d cell(s) with alternative != 0 were rendered as alternative 0\n",
					res.AlternativeIgnored)
			}
			if err := writePNG(out, res.Image); err != nil {
				return jsonError(cmd, "IMAGE_WRITE_FAILED", err.Error(), nil)
			}
			b := res.Image.Bounds()
			payload := map[string]any{
				"scene":          scenePath,
				"layer":          res.Layer,
				"out":            out,
				"cells":          res.Cells,
				"region_size":    res.RegionSize,
				"cell_px_size":   res.CellSize,
				"origin":         res.Origin,
				"layer_position": res.LayerPosition,
				"size":           [2]int{b.Dx(), b.Dy()},
			}
			if res.HasTransform {
				// 旋转/缩放没有解析：明确说清楚坐标口径，别让调用方按世界坐标用。
				payload["warnings"] = []string{
					"the layer carries a transform (rotation/scale) which is not applied — origin is layer-local, and world = origin + pixel + layer_position only holds for unrotated layers",
				}
			}
			return printJSON(cmd.OutOrStdout(), payload, prettyOutput)
		},
	}
	cmd.Flags().StringVar(&scenePath, "scene", "", ".tscn file to parse (disk path)")
	cmd.Flags().StringVar(&layer, "layer", "", "TileMapLayer node name (default: the first layer)")
	cmd.Flags().StringVar(&out, "out", "", "output PNG path")
	_ = cmd.MarkFlagRequired("scene")
	_ = cmd.MarkFlagRequired("out")
	return cmd
}

// newTilemapFindByColorCommand 按图集像素颜色反查格子（需求 R-5 4.2）：
// "地图上的红点 = 刷怪点"要的是一条命令给出"哪些格子含该颜色"以及
// "这些格子的中心在世界哪里"（后者直接就是 Marker2D.position 的值）。
func newTilemapFindByColorCommand() *cobra.Command {
	var (
		scenePath string
		layer     string
		color     string
		tolerance float64
	)
	cmd := &cobra.Command{
		Use:   "find-by-color --scene <file.tscn> --layer <name> --color R,G,B|#RRGGBB --tolerance N",
		Short: "Find the cells whose atlas tiles contain pixels of a color (local, no editor)",
		Long: `tilemap find-by-color parses the .tscn locally, decodes the
TileMapLayer's cells and the layer TileSet (.tres) atlas textures (the same
decoding tilemap dump / tilemap render use), then reports every cell whose
atlas tile contains at least one pixel inside an RGB euclidean distance of
--color:

  distance = sqrt(dr^2 + dg^2 + db^2)   over the 8-bit R,G,B channels (0..255)
  a pixel matches when distance <= --tolerance   (identical to image find-color)
  pixels whose alpha is 0 are skipped (transparent padding carries no color)

A cell report carries pixel_count (matching pixels in that cell's atlas tile)
and centroid_in_cell (their centroid, in cell-local pixels) — the "red dot is
at (6.67,6.67) inside the tile" answer. world_center[] is the cell CENTER in
world pixels, one entry per cells[] entry, same order: place a Marker2D at it.
world_center = (cell + 0.5) x texture_region_size + the layer's own position;
a layer carrying a transform (rotation/scale) is not applied and is reported in
warnings.

Each distinct (source, atlas) region is scanned once, so the cost scales with
the number of distinct tiles used, not with the cell count.

Response: scene, layer, color (canonical #RRGGBB), tolerance, region_size
(the cell pixel size), cells[] (x, y, source, atlas, pixel_count,
centroid_in_cell), world_center[] ({x,y} cell centers, parallel to cells[]),
scanned_cells, alternative_ignored (matching cells with alternative != 0,
judged as alternative 0 — same convention as tilemap render).

Examples:
  godot-ai-cli tilemap find-by-color --scene scene/main.tscn --layer GroundTileMapLayer --color 255,80,0 --tolerance 60
  godot-ai-cli tilemap find-by-color --scene scenes/level1.tscn --layer Ground --color '#FF5000' --tolerance 20`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if tolerance < 0 {
				return jsonError(cmd, "INVALID_PARAMS",
					fmt.Sprintf("--tolerance must be >= 0, got %v", tolerance), nil)
			}
			target, err := imganalysis.ParseColorSpec(color)
			if err != nil {
				return jsonError(cmd, "INVALID_PARAMS", fmt.Sprintf("--color: %v", err), nil)
			}
			sc, err := tilemapfile.ParseScene(scenePath)
			if err != nil {
				return jsonError(cmd, "SCENE_PARSE_FAILED", err.Error(), nil)
			}
			res, err := tilemapfile.FindCellsByColor(sc, layer, target, tolerance)
			if err != nil {
				return jsonError(cmd, "FIND_FAILED", err.Error(), nil)
			}
			payload := map[string]any{
				"scene":               scenePath,
				"layer":               res.Layer,
				"color":               imganalysis.ColorHex(target),
				"tolerance":           tolerance,
				"region_size":         res.RegionSize,
				"cells":               res.Cells,
				"world_center":        res.WorldCenters,
				"layer_position":      res.LayerPosition,
				"scanned_cells":       res.ScannedCells,
				"alternative_ignored": res.AlternativeIgnored,
			}
			if res.HasTransform {
				// 旋转/缩放没有解析：world_center 只叠加了 position。
				payload["warnings"] = []string{
					"the layer carries a transform (rotation/scale) which is not applied — world_center only adds the layer's position",
				}
			}
			return printJSON(cmd.OutOrStdout(), payload, prettyOutput)
		},
	}
	cmd.Flags().StringVar(&scenePath, "scene", "", ".tscn file to parse (disk path)")
	cmd.Flags().StringVar(&layer, "layer", "", "TileMapLayer node name to reverse-look up")
	cmd.Flags().StringVar(&color, "color", "", "target color: R,G,B (e.g. 255,80,0) or #RRGGBB (e.g. '#FF5000')")
	cmd.Flags().Float64Var(&tolerance, "tolerance", 0, "max RGB euclidean distance (0..441.67); 0 = exact color match")
	_ = cmd.MarkFlagRequired("scene")
	_ = cmd.MarkFlagRequired("layer")
	_ = cmd.MarkFlagRequired("color")
	_ = cmd.MarkFlagRequired("tolerance")
	return cmd
}
