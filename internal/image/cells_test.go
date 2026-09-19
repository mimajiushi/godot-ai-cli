package image

import (
	"image"
	"image/color"
	"testing"
)

// 造一张 64×80 的测试图：16×16 栅格（4 列 × 5 行），
// r0 整行有满宽横条（跨格图元），(1,1) 一个小方块，其余空格。
func makeLatticeSheet(t *testing.T) *image.RGBA {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 80))
	opaque := color.RGBA{255, 255, 255, 255}
	// r0：y=6..12 满宽横条（模拟进度条，跨全部 4 列）
	for y := 6; y <= 12; y++ {
		for x := 0; x < 64; x++ {
			img.SetRGBA(x, y, opaque)
		}
	}
	// (c1,r1)：格内 6..9 × 7..10 小方块（图内 x=22..25，y=23..26）
	for y := 16 + 7; y <= 16+10; y++ {
		for x := 6 + 16; x <= 9+16; x++ {
			img.SetRGBA(x, y, opaque)
		}
	}
	return img
}

func TestCellsCountsOccupancyAndBBox(t *testing.T) {
	img := makeLatticeSheet(t)
	res := Cells(img, 16, 16, 0, 0, 0.03)
	if res.Cols != 4 || res.Rows != 5 {
		t.Fatalf("grid = %dx%d, want 4x5", res.Cols, res.Rows)
	}
	// r0 四格各有 16*7=112 个不透明像素，bbox 贴左右边（跨格横条）。
	for c := 0; c < 4; c++ {
		cell := findCell(t, res, c, 0)
		if cell.Opaque != 112 {
			t.Errorf("cell (%d,0) opaque = %d, want 112", c, cell.Opaque)
		}
		if cell.BBox == nil || *cell.BBox != [4]int{0, 6, 16, 7} {
			t.Errorf("cell (%d,0) bbox = %v, want [0 6 16 7]", c, cell.BBox)
		}
		if !cell.TouchesEdge {
			t.Errorf("cell (%d,0) must touch edge (full-width bar)", c)
		}
	}
	// (1,1) 小方块：4×4=16 像素，bbox=(6,7,4,4)，不贴边。
	cell := findCell(t, res, 1, 1)
	if cell.Opaque != 16 || *cell.BBox != [4]int{6, 7, 4, 4} {
		t.Errorf("cell (1,1) = %+v", cell)
	}
	if cell.TouchesEdge {
		t.Errorf("cell (1,1) must NOT touch edge")
	}
	// 空格清单：20 格 - 5 非空 = 15。
	if len(res.EmptyCells) != 15 {
		t.Errorf("empty cells = %d, want 15", len(res.EmptyCells))
	}
	if len(res.TouchingCells) != 4 {
		t.Errorf("touching cells = %d, want 4 (r0 横条四格)", len(res.TouchingCells))
	}
}

func TestCellsRespectsOffset(t *testing.T) {
	// 24×24 图、cell 16、offset 4：可放 2 列（4..19 与 20..24 残缺格）。
	img := image.NewRGBA(image.Rect(0, 0, 24, 24))
	opaque := color.RGBA{255, 255, 255, 255}
	img.SetRGBA(5, 5, opaque)
	img.SetRGBA(23, 23, opaque)
	res := Cells(img, 16, 16, 4, 4, 0.03)
	if res.Cols != 2 || res.Rows != 2 {
		t.Fatalf("grid = %dx%d, want 2x2 (残缺格照算)", res.Cols, res.Rows)
	}
	c00 := findCell(t, res, 0, 0)
	if c00.Rect != [4]int{4, 4, 16, 16} || c00.Opaque != 1 {
		t.Errorf("cell (0,0) = %+v", c00)
	}
	c11 := findCell(t, res, 1, 1)
	// 残缺格 20..23 × 20..23（4×4），(23,23) 在内，格内相对 (3,3)。
	if c11.Rect != [4]int{20, 20, 4, 4} || c11.Opaque != 1 || *c11.BBox != [4]int{3, 3, 1, 1} {
		t.Errorf("cell (1,1) = %+v", c11)
	}
	// (23,23) 贴到残缺格右下边界 → touches_edge。
	if !c11.TouchesEdge {
		t.Errorf("cell (1,1) should touch edge (残缺格边界)")
	}
}

func TestCellsAlphaThreshold(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	img.SetRGBA(3, 3, color.RGBA{255, 255, 255, 5}) // alpha=5/255 ≈ 0.0196
	if res := Cells(img, 16, 16, 0, 0, 0.03); len(res.Cells) != 0 {
		t.Errorf("alpha 5/255 below 0.03 threshold must read empty, got %+v", res.Cells)
	}
	if res := Cells(img, 16, 16, 0, 0, 0.01); len(res.Cells) != 1 {
		t.Errorf("alpha 5/255 above 0.01 threshold must read occupied")
	}
}

func findCell(t *testing.T, res CellsResult, col, row int) CellStat {
	t.Helper()
	for _, c := range res.Cells {
		if c.Col == col && c.Row == row {
			return c
		}
	}
	t.Fatalf("cell (%d,%d) not occupied; cells = %+v", col, row, res.Cells)
	return CellStat{}
}
