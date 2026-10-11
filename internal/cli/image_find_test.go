package cli

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeColorSheet 造一张 64x64 的"图集"：中性灰底（不透明）+ 16x16 块
// (2,2) 里三个红点（局部 (9,5) (6,7) (5,8)，与需求文档现场同构）+ 块 (0,0)
// 里一个刚过阈值的金色像素 + 一个全透明像素。
func writeColorSheet(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "sheet.png")
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	gray := color.RGBA{80, 80, 80, 255}
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.SetRGBA(x, y, gray)
		}
	}
	img.SetRGBA(32+9, 32+5, color.RGBA{255, 80, 0, 255})
	img.SetRGBA(32+6, 32+7, color.RGBA{250, 86, 4, 255})
	img.SetRGBA(32+5, 32+8, color.RGBA{245, 74, 8, 255})
	img.SetRGBA(2, 3, color.RGBA{255, 141, 0, 255}) // 距离 61 → 阈值 60 时不命中
	img.SetRGBA(1, 1, color.RGBA{255, 80, 0, 0})    // 全透明：不参与比对
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return src
}

// TestImageFindColorAtlasGrid：--region-size 16x16 时按图集网格分组，
// 命中块的 pixel_count/centroid/extent 与扫描计数都要对。
func TestImageFindColorAtlasGrid(t *testing.T) {
	src := writeColorSheet(t)
	p := runImageCmd(t, "find-color", "--path", src, "--color", "255,80,0",
		"--tolerance", "60", "--region-size", "16x16")

	if p["texture"] != src || p["resolved"] != src {
		t.Errorf("texture/resolved = %v / %v, want %q", p["texture"], p["resolved"], src)
	}
	if p["color"] != "#FF5000" || p["tolerance"].(float64) != 60 {
		t.Errorf("color/tolerance = %v / %v, want #FF5000 / 60", p["color"], p["tolerance"])
	}
	// 4096 像素里 1 个全透明 → 参与比对 4095。
	if p["scanned"].(float64) != 4095 || p["transparent_skipped"].(float64) != 1 {
		t.Errorf("scanned/transparent_skipped = %v/%v, want 4095/1", p["scanned"], p["transparent_skipped"])
	}
	size, _ := p["size"].([]any)
	if len(size) != 2 || size[0].(float64) != 64 || size[1].(float64) != 64 {
		t.Errorf("size = %v, want [64 64]", size)
	}
	rs, _ := p["region_size"].([]any)
	if len(rs) != 2 || rs[0].(float64) != 16 || rs[1].(float64) != 16 {
		t.Errorf("region_size = %v, want [16 16]", rs)
	}
	matches, _ := p["matches"].([]any)
	if len(matches) != 1 {
		t.Fatalf("matches = %v, want 只有块 (2,2)", matches)
	}
	m := matches[0].(map[string]any)
	if m["region"] != "2,2" || m["pixel_count"].(float64) != 3 {
		t.Errorf("match = %v, want region 2,2 / pixel_count 3", m)
	}
	centroid, _ := m["centroid"].([]any)
	if len(centroid) != 2 || centroid[0].(float64) != 6.67 || centroid[1].(float64) != 6.67 {
		t.Errorf("centroid = %v, want [6.67 6.67]", centroid)
	}
	extent, _ := m["extent"].(map[string]any)
	ex, _ := extent["x"].([]any)
	ey, _ := extent["y"].([]any)
	if ex[0].(float64) != 5 || ex[1].(float64) != 9 || ey[0].(float64) != 5 || ey[1].(float64) != 8 {
		t.Errorf("extent = %v, want x[5,9] y[5,8]", extent)
	}
	if _, hasPixels := m["pixels"]; hasPixels {
		t.Errorf("不给 --pixels 时不该出现 pixels：%v", m)
	}
}

// TestImageFindColorPixelsAndRegion：--pixels 给全部命中坐标；--region 先裁
// 再按网格分组（坐标相对裁剪区域）。
func TestImageFindColorPixelsAndRegion(t *testing.T) {
	src := writeColorSheet(t)
	p := runImageCmd(t, "find-color", "--path", src, "--color", "#FF5000",
		"--tolerance", "60", "--region-size", "16x16", "--pixels")
	matches, _ := p["matches"].([]any)
	m := matches[0].(map[string]any)
	px, _ := m["pixels"].([]any)
	if len(px) != 3 {
		t.Fatalf("pixels = %v, want 3 个命中坐标", px)
	}
	want := [][2]float64{{9, 5}, {6, 7}, {5, 8}} // 行优先扫描顺序
	for i, w := range want {
		got := px[i].([]any)
		if got[0].(float64) != w[0] || got[1].(float64) != w[1] {
			t.Errorf("pixels[%d] = %v, want %v", i, got, w)
		}
	}

	// 只裁块 (2,2)：命中落到裁剪后的块 (0,0)，坐标仍是格内局部坐标。
	cropped := runImageCmd(t, "find-color", "--path", src, "--color", "#FF5000",
		"--tolerance", "60", "--region-size", "16x16", "--region", "32,32,16,16")
	if cropped["region"] != "32,32,16,16" {
		t.Errorf("region echo = %v", cropped["region"])
	}
	cm, _ := cropped["matches"].([]any)
	if len(cm) != 1 || cm[0].(map[string]any)["region"] != "0,0" {
		t.Errorf("裁剪后的 matches = %v, want 块 0,0", cm)
	}
	cc, _ := cm[0].(map[string]any)["centroid"].([]any)
	if cc[0].(float64) != 6.67 || cc[1].(float64) != 6.67 {
		t.Errorf("裁剪后 centroid = %v, want [6.67 6.67]", cc)
	}
}

// TestImageFindColorWholeImageAndErrors：不给 --region-size 时整图一块；
// 非法颜色 / 负阈值 / 缺必填 flag 都要结构化报错。
func TestImageFindColorWholeImageAndErrors(t *testing.T) {
	src := writeColorSheet(t)
	p := runImageCmd(t, "find-color", "--path", src, "--color", "255,80,0", "--tolerance", "60")
	rs, _ := p["region_size"].([]any)
	if rs[0].(float64) != 64 || rs[1].(float64) != 64 {
		t.Errorf("缺省 region_size = %v, want 整图 [64 64]", rs)
	}
	matches, _ := p["matches"].([]any)
	m := matches[0].(map[string]any)
	extent, _ := m["extent"].(map[string]any)
	ex, _ := extent["x"].([]any)
	if m["region"] != "0,0" || m["pixel_count"].(float64) != 3 || ex[0].(float64) != 37 || ex[1].(float64) != 41 {
		t.Errorf("整图一块的 match = %v", m)
	}

	for _, bad := range [][]string{
		{"find-color", "--path", src, "--color", "红色", "--tolerance", "10"},
		{"find-color", "--path", src, "--color", "#GGGGGG", "--tolerance", "10"},
		{"find-color", "--path", src, "--color", "255,80,0", "--tolerance", "-1"},
		{"find-color", "--path", src, "--color", "255,80,0", "--tolerance", "10", "--region-size", "0x16"},
		{"find-color", "--path", src, "--color", "255,80,0", "--tolerance", "10", "--region", "1,2,3"},
	} {
		if out := runImageCmdErr(t, bad...); !strings.Contains(out, "INVALID_PARAMS") {
			t.Errorf("find-color %v want INVALID_PARAMS, got %s", bad, out)
		}
	}
	// 必填缺一：--color / --tolerance。runImageCmdErr 直接跑 image 子命令
	// （不经 root 的 execute），所以 cobra 的 usage 报错以文本形式返回。
	for _, missing := range [][]string{
		{"find-color", "--path", src, "--tolerance", "10"},
		{"find-color", "--path", src, "--color", "255,80,0"},
	} {
		if out := runImageCmdErr(t, missing...); !strings.Contains(out, "required flag") {
			t.Errorf("find-color %v want required-flag error, got %s", missing, out)
		}
	}
}
