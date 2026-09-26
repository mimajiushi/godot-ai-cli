package image

import (
	"image"
	"image/color"
	"strings"
	"testing"
)

// solidImage 造一张纯色 bmp，用于基线对比用例。
func solidImage(w, h int, c color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// TestBaselineDiffIdenticalIsZero：逐字节相同的两张图 → 差异比例 0、
// 标注图与原图一致。
func TestBaselineDiffIdenticalIsZero(t *testing.T) {
	a := solidImage(4, 3, color.RGBA{0x21, 0x23, 0x27, 0xFF})
	b := solidImage(4, 3, color.RGBA{0x21, 0x23, 0x27, 0xFF})

	out, stats, err := DiffImages(a, b, 8)
	if err != nil {
		t.Fatalf("identical images must not error: %v", err)
	}
	if stats.DiffPixels != 0 || stats.DiffRatio != 0 {
		t.Errorf("stats = %+v, want zero diff", stats)
	}
	if stats.TotalPixels != 12 {
		t.Errorf("total_pixels = %d, want 12", stats.TotalPixels)
	}
	if len(stats.SamplePoints) != 0 {
		t.Errorf("sample points = %v, want none", stats.SamplePoints)
	}
	if _, _, _, a := out.At(0, 0).RGBA(); int(a>>8) != 0xFF {
		t.Errorf("unchanged pixels must survive into the annotated image")
	}
}

// TestBaselineDiffMarksPixelsAndSamplesThem：差异像素被涂成洋红标记色、
// 采样点按扫描顺序截断到上限，比例按总像素数算。
func TestBaselineDiffMarksPixelsAndSamplesThem(t *testing.T) {
	a := solidImage(2, 2, color.RGBA{0, 0, 0, 0xFF})
	b := solidImage(2, 2, color.RGBA{0, 0, 0, 0xFF})
	// 只有 (1,0) 与 (0,1) 不同 → 2/4 = 0.5
	a.(*image.RGBA).SetRGBA(1, 0, color.RGBA{0xFF, 0xFF, 0xFF, 0xFF})
	a.(*image.RGBA).SetRGBA(0, 1, color.RGBA{0xFF, 0xFF, 0xFF, 0xFF})

	out, stats, err := DiffImages(a, b, 1)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if stats.DiffPixels != 2 || stats.DiffRatio != 0.5 {
		t.Errorf("stats = %+v, want 2/4 = 0.5", stats)
	}
	if len(stats.SamplePoints) != 1 || stats.SamplePoints[0] != [2]int{1, 0} {
		t.Errorf("sample points = %v, want the first differing pixel (1,0)", stats.SamplePoints)
	}
	r, g, bl, _ := out.At(1, 0).RGBA()
	if int(r>>8) != 0xFF || int(g>>8) != 0 || int(bl>>8) != 0xFF {
		t.Errorf("differing pixel must be marked #FF00FF, got #%02X%02X%02X", r>>8, g>>8, bl>>8)
	}
}

// TestBaselineDiffSizeMismatchErrors：尺寸不一致必须直接报错并点名两边尺寸。
func TestBaselineDiffSizeMismatchErrors(t *testing.T) {
	_, _, err := DiffImages(solidImage(2, 1, color.RGBA{}), solidImage(3, 1, color.RGBA{}), 4)
	if err == nil {
		t.Fatal("size mismatch must error")
	}
	msg := err.Error()
	for _, want := range []string{"2x1", "3x1"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q must name %q", msg, want)
		}
	}
}
