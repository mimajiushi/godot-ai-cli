package image

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"testing"
)

// sheetWithClusters builds a w×h image whose opaque 8px blocks sit at the
// given column starts — the synthetic stand-in for a real sprite sheet.
func sheetWithClusters(w, h int, starts ...int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for _, s := range starts {
		for x := s; x < s+8; x++ {
			for y := 0; y < h; y++ {
				img.SetRGBA(x, y, color.RGBA{0xFF, 0, 0, 0xFF})
			}
		}
	}
	return img
}

// pairStarts reproduces the 浮游炮 layout from the requirement doc: 8 pairs
// of 8px blocks — 32px inside a pair, 48px between pair starts — on 384×32.
func pairStarts() []int {
	var starts []int
	for k := 0; k < 8; k++ {
		starts = append(starts, 4+48*k, 36+48*k)
	}
	return starts
}

func TestGridDetectPairedSheet(t *testing.T) {
	img := sheetWithClusters(384, 32, pairStarts()...)
	got := GridDetect(img, 0)

	if len(got.ColumnClusters) != 16 {
		t.Fatalf("column_clusters = %v, want 16 clusters", got.ColumnClusters)
	}
	if got.ColumnClusters[0] != [2]int{4, 11} || got.ColumnClusters[1] != [2]int{36, 43} {
		t.Errorf("first clusters = %v %v", got.ColumnClusters[0], got.ColumnClusters[1])
	}
	if len(got.RowClusters) != 1 || got.RowClusters[0] != [2]int{0, 31} {
		t.Errorf("row_clusters = %v, want one full-height run", got.RowClusters)
	}

	var has48, has32 bool
	for _, g := range got.SuggestedGrids {
		if g.Cell == [2]int{48, 32} && g.Frames == 8 && g.Exact {
			has48 = true
		}
		if g.Cell == [2]int{32, 32} && g.Frames == 12 && g.Exact {
			has32 = true
		}
	}
	if !has48 || !has32 {
		t.Errorf("suggested_grids missing 48×8 or 32×12: %+v", got.SuggestedGrids)
	}
}

func TestGridDetectUniformSheet(t *testing.T) {
	// 128×32, four 16px sprites on a clean 32px grid.
	var starts []int
	for k := 0; k < 4; k++ {
		starts = append(starts, 4+32*k)
	}
	img := sheetWithClusters(128, 32, starts...)
	got := GridDetect(img, 0)
	found := false
	for _, g := range got.SuggestedGrids {
		if g.Cell == [2]int{32, 32} && g.Frames == 4 && g.Exact {
			found = true
		}
	}
	if !found {
		t.Errorf("uniform sheet: no 32×4 suggestion in %+v", got.SuggestedGrids)
	}
}

func TestGridDetectEmptyImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	got := GridDetect(img, 0)
	if len(got.ColumnClusters) != 0 || len(got.SuggestedGrids) != 0 {
		t.Errorf("empty image must produce no clusters/grids: %+v", got)
	}
}

func TestCropBounds(t *testing.T) {
	img := sheetWithClusters(64, 32, 0)
	c, err := Crop(img, 4, 8, 16, 16)
	if err != nil {
		t.Fatal(err)
	}
	if c.Bounds().Dx() != 16 || c.Bounds().Dy() != 16 {
		t.Errorf("crop size = %v", c.Bounds())
	}
	if _, err := Crop(img, 60, 0, 16, 16); err == nil {
		t.Error("out-of-bounds region accepted")
	}
	if _, err := Crop(img, 0, 0, 0, 16); err == nil {
		t.Error("zero-size region accepted")
	}
}

func TestDownscaleNearest(t *testing.T) {
	img := sheetWithClusters(64, 32, 0)
	if got := DownscaleNearest(img, 640); got.Bounds().Dx() != 64 {
		t.Errorf("already-fitting image must pass through, got %v", got.Bounds())
	}
	got := DownscaleNearest(img, 16)
	if got.Bounds().Dx() != 16 || got.Bounds().Dy() != 8 {
		t.Errorf("downscaled size = %v, want 16x8", got.Bounds())
	}
}

func TestEncodeGIF(t *testing.T) {
	f1 := sheetWithClusters(32, 32, 0)
	f2 := sheetWithClusters(32, 32, 8)
	var buf bytes.Buffer
	if err := EncodeGIF(&buf, []image.Image{f1, f2}, []int{100, 100}); err != nil {
		t.Fatal(err)
	}
	g, err := gif.DecodeAll(&buf)
	if err != nil {
		t.Fatalf("encoded output is not a valid GIF: %v", err)
	}
	if len(g.Image) != 2 || len(g.Delay) != 2 || g.Delay[0] != 10 {
		t.Errorf("gif frames/delays = %d/%v", len(g.Image), g.Delay)
	}
	if err := EncodeGIF(&buf, nil, nil); err == nil {
		t.Error("zero frames accepted")
	}
}
