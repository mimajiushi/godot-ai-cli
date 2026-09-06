// The `tilemap dump` / `tilemap render` leaves: local .tscn tile data
// inspection that needs no editor, daemon, or even an open scene — the
// TileMapLayer tile_map_data PackedByteArray is decoded straight from the
// scene file (see internal/tilemapfile).
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

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
			return printJSON(cmd.OutOrStdout(), map[string]any{
				"scene":       scenePath,
				"layer":       res.Layer,
				"out":         out,
				"cells":       res.Cells,
				"region_size": res.RegionSize,
				"size":        [2]int{b.Dx(), b.Dy()},
			}, prettyOutput)
		},
	}
	cmd.Flags().StringVar(&scenePath, "scene", "", ".tscn file to parse (disk path)")
	cmd.Flags().StringVar(&layer, "layer", "", "TileMapLayer node name (default: the first layer)")
	cmd.Flags().StringVar(&out, "out", "", "output PNG path")
	_ = cmd.MarkFlagRequired("scene")
	_ = cmd.MarkFlagRequired("out")
	return cmd
}
