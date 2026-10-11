package tilemapfile

import (
	"fmt"
	"image"
	"math"

	imganalysis "github.com/mimajiushi/godot-ai-cli/internal/image"
)

// 地图级颜色反查（需求 R-5 4.2）："地图上的红点 = 刷怪点"要求一条命令回答
// "哪些格子用了含指定颜色像素的图块"，以及"这些格子的中心在世界哪里"——
// 后者正是 Marker2D.position 需要的值。TileSet 解码、图集区域定位与
// tilemap render 共用同一套口径（AtlasSource.AtlasRect），命中判定与
// image find-color 共用 imganalysis.ScanColor（同一阈值语义）。

// CellColorMatch 是一格的颜色命中结论。
type CellColorMatch struct {
	X              int        `json:"x"`
	Y              int        `json:"y"`
	Source         int        `json:"source"`
	Atlas          [2]int     `json:"atlas"`
	PixelCount     int        `json:"pixel_count"`
	CentroidInCell [2]float64 `json:"centroid_in_cell"` // 格内局部坐标（两位小数）
}

// WorldPoint 是一个点位的世界坐标（像素，浮点：格尺寸为奇数时中心会落半像素）。
type WorldPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// CellColorResult 是 FindCellsByColor 的输出。Cells 与 WorldCenters 一一对应
// （同一顺序），调用方可以直接按下标取配对值。
type CellColorResult struct {
	Layer              string
	RegionSize         [2]int
	LayerPosition      [2]float64
	HasTransform       bool
	Cells              []CellColorMatch
	WorldCenters       []WorldPoint
	ScannedCells       int // 体检过的格子数（图层的全部格子）
	AlternativeIgnored int // 命中的格子里 alternative != 0 的个数（按 alternative 0 判定）
}

// FindCellsByColor 反查图层里含目标颜色像素的格子。每格只体检自己那块图集
// 区域（同一 (source, atlas) 只扫一次并缓存），因此大图的代价与"用到的图块
// 种类数"成正比，而不是格数 × 像素数。
//
// 坐标口径：CellColorMatch.CentroidInCell 是格内局部坐标；
// WorldCenters 是格中心的世界坐标 = (格坐标 + 0.5) × 格尺寸 + 图层 position。
// alternative != 0 的格子按 alternative 0 的区域像素判定（与 tilemap render
// 同口径），命中时计入 AlternativeIgnored。
func FindCellsByColor(sc *Scene, layerName string, target [3]int, tolerance float64) (*CellColorResult, error) {
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

	// 同一图集区域只扫一次：key 是 (source, atlas_x, atlas_y)。
	type atlasKey struct {
		source int
		ax, ay int
	}
	hits := map[atlasKey]imganalysis.ColorHitStats{}
	textures := map[int]image.Image{}
	regionSize := [2]int{}
	regionSizeSet := false
	res := &CellColorResult{
		Layer:         layer.Name,
		LayerPosition: layer.Position,
		HasTransform:  layer.HasTransform,
		Cells:         []CellColorMatch{},
		WorldCenters:  []WorldPoint{},
	}
	for _, c := range layer.Cells {
		res.ScannedCells++
		src, ok := ts.Sources[c.Source]
		if !ok {
			return nil, fmt.Errorf("cell (%d,%d) uses source_id %d which the TileSet does not define", c.X, c.Y, c.Source)
		}
		if !regionSizeSet {
			regionSize = src.RegionSize
			regionSizeSet = true
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
		key := atlasKey{source: c.Source, ax: c.Atlas[0], ay: c.Atlas[1]}
		st, cached := hits[key]
		if !cached {
			sr := src.AtlasRect(c.Atlas)
			if !sr.In(tex.Bounds()) {
				return nil, fmt.Errorf("cell (%d,%d): atlas region %v is outside the %v texture", c.X, c.Y, sr, tex.Bounds())
			}
			st = imganalysis.ScanColor(tex, sr, target, tolerance, false)
			hits[key] = st
		}
		if st.PixelCount == 0 {
			continue
		}
		if c.Alternative != 0 {
			res.AlternativeIgnored++
		}
		res.Cells = append(res.Cells, CellColorMatch{
			X:              c.X,
			Y:              c.Y,
			Source:         c.Source,
			Atlas:          c.Atlas,
			PixelCount:     st.PixelCount,
			CentroidInCell: st.Centroid,
		})
		res.WorldCenters = append(res.WorldCenters, WorldPoint{
			X: round2f((float64(c.X)+0.5)*float64(regionSize[0]) + layer.Position[0]),
			Y: round2f((float64(c.Y)+0.5)*float64(regionSize[1]) + layer.Position[1]),
		})
	}
	res.RegionSize = regionSize
	return res, nil
}

// round2f 把世界坐标收敛到两位小数，避免浮点末位噪声进回包。
func round2f(v float64) float64 {
	return math.Round(v*100) / 100
}
