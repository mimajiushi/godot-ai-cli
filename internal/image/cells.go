package image

import "image"

// 逐格占用体检（image cells / grid-detect 强制栅格共用）：
// 按给定 cell 尺寸把图（或 --region 子区域）切成栅格，逐格统计不透明像素数
// 与格内包围盒，并标记 bbox 贴边的格子（可能是跨格图元或被裁断）。

// CellStat 是一格的体检结果。BBox 为格内相对坐标 [x,y,w,h]，空格为 nil。
type CellStat struct {
	Col         int     `json:"col"`
	Row         int     `json:"row"`
	Rect        [4]int  `json:"rect"` // 图内绝对坐标 [x,y,w,h]
	Opaque      int     `json:"opaque"`
	BBox        *[4]int `json:"bbox"`
	TouchesEdge bool    `json:"touches_edge"`
}

// CellsResult 是 Cells 的输出。
type CellsResult struct {
	Size          [2]int     `json:"size"` // 实际体检区域尺寸（region 裁剪后）
	Cell          [2]int     `json:"cell"`
	Offset        [2]int     `json:"offset"`
	Cols          int        `json:"cols"`
	Rows          int        `json:"rows"`
	Cells         []CellStat `json:"cells"`          // 仅非空格
	EmptyCells    [][2]int   `json:"empty_cells"`    // [col,row]
	TouchingCells [][2]int   `json:"touching_cells"` // bbox 贴边的 [col,row]
}

// Cells 按 cell×cell 栅格（从 offset 起）逐格统计 img 的不透明像素。
// alphaThreshold ∈ [0,1]；超出图右边/下边的残缺格按实际可见部分统计。
// region 非空时先裁剪（栅格索引仍从裁剪区域原点起算）。
func Cells(img image.Image, cellW, cellH, offsetX, offsetY int, alphaThreshold float64) CellsResult {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	thr := uint32(clamp01(alphaThreshold) * 65535)

	res := CellsResult{
		Size:   [2]int{w, h},
		Cell:   [2]int{cellW, cellH},
		Offset: [2]int{offsetX, offsetY},
	}
	if cellW <= 0 || cellH <= 0 {
		return res
	}
	res.Cols = (w - offsetX + cellW - 1) / cellW
	res.Rows = (h - offsetY + cellH - 1) / cellH
	if res.Cols < 0 {
		res.Cols = 0
	}
	if res.Rows < 0 {
		res.Rows = 0
	}

	for r := 0; r < res.Rows; r++ {
		for c := 0; c < res.Cols; c++ {
			x0 := b.Min.X + offsetX + c*cellW
			y0 := b.Min.Y + offsetY + r*cellH
			x1 := min(x0+cellW, b.Max.X)
			y1 := min(y0+cellH, b.Max.Y)
			stat := CellStat{Col: c, Row: r, Rect: [4]int{x0 - b.Min.X, y0 - b.Min.Y, x1 - x0, y1 - y0}}

			minX, minY, maxX, maxY := -1, -1, -1, -1
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					if _, _, _, a := img.At(x, y).RGBA(); a > thr {
						stat.Opaque++
						rx, ry := x-x0, y-y0
						if minX < 0 || rx < minX {
							minX = rx
						}
						if rx > maxX {
							maxX = rx
						}
						if minY < 0 || ry < minY {
							minY = ry
						}
						if ry > maxY {
							maxY = ry
						}
					}
				}
			}
			if stat.Opaque == 0 {
				res.EmptyCells = append(res.EmptyCells, [2]int{c, r})
				continue
			}
			stat.BBox = &[4]int{minX, minY, maxX - minX + 1, maxY - minY + 1}
			// bbox 贴到格子任一边界 → 可能是跨格图元（长进度条）或被裁断
			stat.TouchesEdge = minX == 0 || minY == 0 ||
				maxX == (x1-x0)-1 || maxY == (y1-y0)-1
			if stat.TouchesEdge {
				res.TouchingCells = append(res.TouchingCells, [2]int{c, r})
			}
			res.Cells = append(res.Cells, stat)
		}
	}
	return res
}
