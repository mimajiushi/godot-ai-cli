// The `image` command group: local image analysis (palette extraction and
// pixel probes) that needs no editor, daemon, or third-party runtime.
// Covers the "analyze texture colors / verify rendered pixels" workflow that
// previously forced agents to write ad-hoc Python+PIL scripts.
package cli

import (
	"fmt"
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
		Short: "Local image analysis (palette, pixel probes) — no editor required",
	}
	cmd.AddCommand(newImagePaletteCommand())
	cmd.AddCommand(newImageProbeCommand())
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
