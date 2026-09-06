package tilemapfile

import (
	"fmt"
	"image"
	"image/draw"

	imganalysis "github.com/mimajiushi/godot-ai-cli/internal/image"
)

// RenderResult is the composited map image plus render metadata.
type RenderResult struct {
	Image              image.Image
	Layer              string
	Cells              int
	RegionSize         [2]int
	AlternativeIgnored int // cells whose alternative tile was rendered as alternative 0
}

// RenderLayer composites one TileMapLayer into a PNG-ready image: every
// cell's atlas region (margin + atlas_coord*(region+separation)) is copied
// from its source texture onto a canvas sized to the cell bounding box and
// offset to non-negative coordinates. Only the selected layer is rendered —
// layers are not stacked. Cells with alternative != 0 are drawn as
// alternative 0 and counted in AlternativeIgnored (the CLI surfaces this as
// a one-line stderr note).
func RenderLayer(sc *Scene, layerName string) (*RenderResult, error) {
	layer, err := sc.FindLayer(layerName)
	if err != nil {
		return nil, err
	}
	if len(layer.Cells) == 0 {
		return nil, fmt.Errorf("layer %q has no cells (no tile_map_data)", layer.Name)
	}
	if layer.TileSetRef == "" {
		return nil, fmt.Errorf("layer %q has no tile_set reference", layer.Name)
	}
	ext, ok := sc.ExtResources[layer.TileSetRef]
	if !ok {
		return nil, fmt.Errorf("layer %q references unknown ext_resource %q", layer.Name, layer.TileSetRef)
	}
	tsPath, err := ResolveRes(sc.ProjectRoot, ext.Path)
	if err != nil {
		return nil, err
	}
	ts, err := ParseTileSet(tsPath)
	if err != nil {
		return nil, err
	}

	// Canvas bounds from the cell range, offset to non-negative.
	minX, minY, maxX, maxY := layer.Cells[0].X, layer.Cells[0].Y, layer.Cells[0].X, layer.Cells[0].Y
	for _, c := range layer.Cells[1:] {
		minX, minY = min(minX, c.X), min(minY, c.Y)
		maxX, maxY = max(maxX, c.X), max(maxY, c.Y)
	}

	// Textures are decoded lazily, once per source id.
	textures := map[int]image.Image{}
	regionSize := [2]int{0, 0}
	var canvas *image.RGBA
	ignored := 0
	for _, c := range layer.Cells {
		src, ok := ts.Sources[c.Source]
		if !ok {
			return nil, fmt.Errorf("cell (%d,%d) uses source_id %d which the TileSet does not define", c.X, c.Y, c.Source)
		}
		if canvas == nil {
			regionSize = src.RegionSize
			canvas = image.NewRGBA(image.Rect(0, 0,
				(maxX-minX+1)*regionSize[0], (maxY-minY+1)*regionSize[1]))
		} else if src.RegionSize != regionSize {
			return nil, fmt.Errorf("atlas sources with different texture_region_size (%v vs %v) are not supported", src.RegionSize, regionSize)
		}
		tex, ok := textures[c.Source]
		if !ok {
			texPath, err := ResolveRes(sc.ProjectRoot, src.TexturePath)
			if err != nil {
				return nil, err
			}
			tex, err = imganalysis.Load(texPath)
			if err != nil {
				return nil, fmt.Errorf("load atlas texture %s: %v", texPath, err)
			}
			textures[c.Source] = tex
		}
		if c.Alternative != 0 {
			ignored++
		}
		// Atlas pixel position: margin + atlas_coord * (region + separation).
		sx := src.Margin[0] + c.Atlas[0]*(regionSize[0]+src.Separation[0])
		sy := src.Margin[1] + c.Atlas[1]*(regionSize[1]+src.Separation[1])
		sr := image.Rect(sx, sy, sx+regionSize[0], sy+regionSize[1])
		if !sr.In(tex.Bounds()) {
			return nil, fmt.Errorf("cell (%d,%d): atlas region %v is outside the %v texture", c.X, c.Y, sr, tex.Bounds())
		}
		dx := (c.X - minX) * regionSize[0]
		dy := (c.Y - minY) * regionSize[1]
		draw.Draw(canvas, image.Rect(dx, dy, dx+regionSize[0], dy+regionSize[1]), tex, sr.Min, draw.Src)
	}
	return &RenderResult{
		Image:              canvas,
		Layer:              layer.Name,
		Cells:              len(layer.Cells),
		RegionSize:         regionSize,
		AlternativeIgnored: ignored,
	}, nil
}
