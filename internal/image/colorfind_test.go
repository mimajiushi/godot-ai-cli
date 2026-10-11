package image

import (
	"image"
	"image/color"
	"testing"
)

// paintSheet 造一张 64x64 的图集：16x16 一块，块 (2,2) 里画出需求文档现场
// 那三个红/橙像素（局部 (9,5) (6,7) (5,8)），块 (3,3) 里放一个"刚好卡在
// 阈值上"的像素，块 (0,0) 里放一个"刚过阈值"的像素，另加全透明填充。
// 目标色 (255,80,0) / 阈值 60 的期望：
//   - (2,2) 3 个命中（距离 0 / 8.77 / 14.14），质心 (6.67,6.67)，
//     extent x=[5,9] y=[5,8]，max_distance 14.14
//   - (3,3) (255,140,0) 距离正好 60 → 命中（边界算命中）
//   - (0,0) (255,141,0) 距离 61 → 不命中
func paintSheet() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	// 块 (2,2)：真正的红点（局部坐标 (9,5) (6,7) (5,8)）
	img.SetRGBA(32+9, 32+5, color.RGBA{255, 80, 0, 255})
	img.SetRGBA(32+6, 32+7, color.RGBA{250, 86, 4, 255})
	img.SetRGBA(32+5, 32+8, color.RGBA{245, 74, 8, 255})
	// 块 (3,3)：距离恰好等于阈值 60（边界值算命中）
	img.SetRGBA(48+3, 48+4, color.RGBA{255, 140, 0, 255})
	// 块 (0,0)：距离 61（刚过阈值，不命中）
	img.SetRGBA(2, 3, color.RGBA{255, 141, 0, 255})
	// 全透明的两个像素：alpha == 0 → 不参与比对，即使颜色"看着像"目标色
	img.SetRGBA(1, 1, color.RGBA{255, 80, 0, 0})
	img.SetRGBA(33, 33, color.RGBA{255, 80, 0, 0})
	return img
}

// TestColorFindAtlasGrid：按 16x16 图集网格分组时，命中块、质心、extent、
// max_distance 与窗口边界都要对得上。
func TestColorFindAtlasGrid(t *testing.T) {
	res := ColorFind(paintSheet(), [3]int{255, 80, 0}, 60, 16, 16, false)

	if res.Size != [2]int{64, 64} || res.RegionSize != [2]int{16, 16} {
		t.Fatalf("size/region_size = %v / %v", res.Size, res.RegionSize)
	}
	if len(res.Matches) != 2 {
		t.Fatalf("matches = %v, want 块 (3,3) 与 (2,2)", res.Matches)
	}
	// 块序按 (cy, cx)：(2,2) 在 (3,3) 之前。
	first, second := res.Matches[0], res.Matches[1]
	if first.Region != "2,2" || second.Region != "3,3" {
		t.Fatalf("match order = %q, %q (want 行优先)", first.Region, second.Region)
	}
	if first.PixelCount != 3 {
		t.Errorf("(2,2) pixel_count = %d, want 3", first.PixelCount)
	}
	if first.Centroid != [2]float64{6.67, 6.67} {
		t.Errorf("(2,2) centroid = %v, want [6.67 6.67]", first.Centroid)
	}
	if first.Extent.X != [2]int{5, 9} || first.Extent.Y != [2]int{5, 8} {
		t.Errorf("(2,2) extent = %v, want x[5,9] y[5,8]", first.Extent)
	}
	if first.MaxDistance != 14.14 {
		t.Errorf("(2,2) max_distance = %v, want 14.14", first.MaxDistance)
	}
	if first.Pixels != nil {
		t.Errorf("不以 --pixels 调用时不该采集像素坐标，got %v", first.Pixels)
	}
	// 边界命中：距离正好等于 tolerance。
	if second.PixelCount != 1 || second.MaxDistance != 60 {
		t.Errorf("(3,3) = %v, want 1 个距离 60 的命中", second)
	}
	// 扫描计数：image.NewRGBA 的零值是「全透明黑」，只有画过的 5 个不透明
	// 像素参与比对，其余 4091 个像素计入 transparent_skipped。
	if res.Scanned != 5 || res.TransparentSkipped != 4091 {
		t.Errorf("scanned/transparent_skipped = %d/%d, want 5/4091",
			res.Scanned, res.TransparentSkipped)
	}
}

// TestColorFindPixelsAndWholeImage：--pixels 采集全部命中坐标（块内局部、
// 行优先）；不给 --region-size 时整图当一块。
func TestColorFindPixelsAndWholeImage(t *testing.T) {
	res := ColorFind(paintSheet(), [3]int{255, 80, 0}, 60, 16, 16, true)
	m := res.Matches[0]
	want := [][2]int{{9, 5}, {6, 7}, {5, 8}} // 按 y 再 x 的扫描顺序
	if len(m.Pixels) != len(want) {
		t.Fatalf("(2,2) pixels = %v, want %v", m.Pixels, want)
	}
	for i, p := range want {
		if m.Pixels[i] != p {
			t.Errorf("pixels[%d] = %v, want %v", i, m.Pixels[i], p)
		}
	}

	whole := ColorFind(paintSheet(), [3]int{255, 80, 0}, 60, 0, 0, false)
	if whole.RegionSize != [2]int{64, 64} {
		t.Errorf("缺省 region_size = %v, want 整图 64x64", whole.RegionSize)
	}
	// 整图一块：4 个命中（(2,2) 的三个 + (3,3) 的边界像素）落进同一块，
	// 局部坐标即图内坐标，extent 覆盖 (37,37)..(51,52)。
	if len(whole.Matches) != 1 || whole.Matches[0].Region != "0,0" {
		t.Fatalf("整图一块的 matches = %v", whole.Matches)
	}
	if whole.Matches[0].PixelCount != 4 {
		t.Errorf("整图 pixel_count = %d, want 4", whole.Matches[0].PixelCount)
	}
	if whole.Matches[0].Extent.X != [2]int{37, 51} || whole.Matches[0].Extent.Y != [2]int{37, 52} {
		t.Errorf("整图 extent = %v, want x[37,51] y[37,52]", whole.Matches[0].Extent)
	}
	if whole.Matches[0].Centroid != [2]float64{41.75, 42} {
		t.Errorf("整图 centroid = %v, want [41.75 42]", whole.Matches[0].Centroid)
	}
}

// TestColorFindToleranceBoundary：阈值边界要"含等号"，并且全透明像素永远
// 不算命中（哪怕颜色与目标色完全一致）。
func TestColorFindToleranceBoundary(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.SetRGBA(0, 0, color.RGBA{150, 100, 100, 255}) // 距离 (100,100,100) = 50
	img.SetRGBA(1, 0, color.RGBA{100, 100, 100, 0})   // 目标色但全透明
	target := [3]int{100, 100, 100}

	at := ColorFind(img, target, 50, 0, 0, false)
	if len(at.Matches) != 1 || at.Matches[0].PixelCount != 1 {
		t.Errorf("tolerance == 距离时必须命中：%v", at.Matches)
	}
	// 未画过的像素是「全透明黑」：64 - 1 个不透明 = 63 个被跳过。
	if at.TransparentSkipped != 63 || at.Scanned != 1 {
		t.Errorf("scanned/transparent_skipped = %d/%d, want 1/63", at.Scanned, at.TransparentSkipped)
	}

	below := ColorFind(img, target, 49.9, 0, 0, false)
	if len(below.Matches) != 0 {
		t.Errorf("tolerance < 距离时不该命中：%v", below.Matches)
	}
	// 全透明像素即使"颜色一样"也不是命中：阈值给到最大也一样。
	huge := ColorFind(img, target, 500, 0, 0, false)
	if len(huge.Matches) != 1 || huge.Matches[0].PixelCount != 1 {
		t.Errorf("全透明像素必须被跳过：%v", huge.Matches)
	}
}

// TestColorFindNoMatchIsEmptyArray：没有命中时 matches 必须是 []（不是 null），
// 调用方才能机械地遍历。
func TestColorFindNoMatchIsEmptyArray(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	res := ColorFind(img, [3]int{255, 0, 0}, 0, 2, 2, false)
	if res.Matches == nil || len(res.Matches) != 0 {
		t.Errorf("matches = %v, want 空数组", res.Matches)
	}
}

// TestScanColorLocalCoordinates：ScanColor 的坐标以传入矩形的左上角为原点
// （tilemap find-by-color 靠它把质心落在"格内局部坐标"）。
func TestScanColorLocalCoordinates(t *testing.T) {
	img := image.NewRGBA(image.Rect(10, 20, 26, 36)) // 非零原点，模拟裁剪后的图集
	img.SetRGBA(12, 22, color.RGBA{255, 0, 0, 255})  // 局部 (2,2)
	img.SetRGBA(14, 23, color.RGBA{255, 0, 0, 255})  // 局部 (4,3)

	st := ScanColor(img, img.Bounds(), [3]int{255, 0, 0}, 0, true)
	if st.PixelCount != 2 {
		t.Fatalf("pixel_count = %d, want 2", st.PixelCount)
	}
	if st.Centroid != [2]float64{3, 2.5} {
		t.Errorf("centroid = %v, want [3 2.5]（块内局部坐标）", st.Centroid)
	}
	if st.Extent.X != [2]int{2, 4} || st.Extent.Y != [2]int{2, 3} {
		t.Errorf("extent = %v, want x[2,4] y[2,3]", st.Extent)
	}
	if len(st.Pixels) != 2 || st.Pixels[0] != [2]int{2, 2} || st.Pixels[1] != [2]int{4, 3} {
		t.Errorf("pixels = %v, want [[2 2] [4 3]]", st.Pixels)
	}
}

// TestColorDistanceAndRound：距离函数的取样口径（8 位通道、欧氏）。
func TestColorDistanceAndRound(t *testing.T) {
	if got := ColorDistance([3]int{255, 80, 0}, [3]int{255, 80, 0}); got != 0 {
		t.Errorf("同色距离 = %v, want 0", got)
	}
	if got := ColorDistance([3]int{0, 0, 0}, [3]int{255, 255, 255}); round2(got) != 441.67 {
		t.Errorf("黑白距离 = %v, want 441.67", got)
	}
	if got := ColorDistance([3]int{255, 80, 0}, [3]int{255, 140, 0}); round2(got) != 60 {
		t.Errorf("绿通道差 60 → %v, want 60", got)
	}
}

// TestParseColorSpec：R,G,B 与 #RRGGBB 两种写法 + 明确报错。
func TestParseColorSpec(t *testing.T) {
	cases := []struct {
		in   string
		want [3]int
	}{
		{"255,80,0", [3]int{255, 80, 0}},
		{" 255 , 80 , 0 ", [3]int{255, 80, 0}},
		{"#FF5000", [3]int{255, 80, 0}},
		{"ff5000", [3]int{255, 80, 0}},
	}
	for _, c := range cases {
		got, err := ParseColorSpec(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseColorSpec(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "1,2", "256,0,0", "-1,0,0", "#GGGGGG", "red", "255,80,x"} {
		if got, err := ParseColorSpec(bad); err == nil {
			t.Errorf("ParseColorSpec(%q) = %v, want 报错", bad, got)
		}
	}
	if ColorHex([3]int{255, 80, 0}) != "#FF5000" {
		t.Errorf("ColorHex = %q, want #FF5000", ColorHex([3]int{255, 80, 0}))
	}
}

// TestScanColorStraightAlphaChannels：颜色采样必须取**直通**（非预乘）通道
// （F-3）。Go 的 color.Color.RGBA() 按约定返回预乘值：α=128 的纯红会被读成
// R=128，与帮助文本 / commands.md 写死的"8 位 R,G,B 欧氏距离"不符，半透明
// 标记像素（抗锯齿边）被系统性漏掉。这条用例同时覆盖两条取通道路径：
// *image.NRGBA（零拷贝快路径，也是 8 位 PNG 的解码结果）与其它图像类型
// （NRGBA 模型换算，用 *image.RGBA 的预乘存储代表）。
func TestScanColorStraightAlphaChannels(t *testing.T) {
	target := [3]int{255, 0, 0}

	// 路径一：*image.NRGBA 直接存直通通道。
	nrgba := image.NewNRGBA(image.Rect(0, 0, 4, 1))
	nrgba.SetNRGBA(0, 0, color.NRGBA{255, 0, 0, 128}) // 半透明纯红
	nrgba.SetNRGBA(1, 0, color.NRGBA{255, 0, 0, 0})   // 全透明：仍要跳过
	nrgba.SetNRGBA(2, 0, color.NRGBA{255, 0, 0, 255}) // 不透明纯红
	nrgba.SetNRGBA(3, 0, color.NRGBA{0, 0, 255, 255}) // 不透明纯蓝

	// 路径二：*image.RGBA 的存储按约定是预乘值 → 直通 α=128 的纯红存成 R=128。
	rgba := image.NewRGBA(image.Rect(0, 0, 4, 1))
	rgba.SetRGBA(0, 0, color.RGBA{128, 0, 0, 128})
	rgba.SetRGBA(1, 0, color.RGBA{0, 0, 0, 0})
	rgba.SetRGBA(2, 0, color.RGBA{255, 0, 0, 255})
	rgba.SetRGBA(3, 0, color.RGBA{0, 0, 255, 255})

	for name, img := range map[string]image.Image{"NRGBA": nrgba, "RGBA(预乘存储)": rgba} {
		// tolerance 10：预乘口径下 α=128 纯红被读成 (128,0,0)，距离 127 → 漏掉；
		// 直通口径下距离 0 → 必须命中（与不透明纯红一起共 2 个命中）。
		st := ScanColor(img, img.Bounds(), target, 10, true)
		if st.PixelCount != 2 {
			t.Errorf("%s: tolerance 10 命中数 = %d, want 2（α=128 与不透明的纯红）", name, st.PixelCount)
		}
		if st.MaxDistance != 0 {
			t.Errorf("%s: max_distance = %v, want 0（两个命中像素都是纯红）", name, st.MaxDistance)
		}
		// α==0 跳过的语义不变：scanned 只数 alpha != 0 的像素。
		if st.Scanned != 3 || st.TransparentSkipped != 1 {
			t.Errorf("%s: scanned/transparent_skipped = %d/%d, want 3/1", name, st.Scanned, st.TransparentSkipped)
		}
		if len(st.Pixels) != 2 || st.Pixels[0] != [2]int{0, 0} || st.Pixels[1] != [2]int{2, 0} {
			t.Errorf("%s: pixels = %v, want [[0 0] [2 0]]", name, st.Pixels)
		}
	}

	// 回归：不透明像素的统计值与"预乘 / 直通"无关（α=255 时两种口径恒等），
	// 阈值边界行为与修复前一致。
	opaque := image.NewRGBA(image.Rect(0, 0, 2, 1))
	opaque.SetRGBA(0, 0, color.RGBA{255, 80, 0, 255})
	opaque.SetRGBA(1, 0, color.RGBA{255, 141, 0, 255}) // 距离 61，阈值 60 不命中
	at := ScanColor(opaque, opaque.Bounds(), [3]int{255, 80, 0}, 60, false)
	if at.PixelCount != 1 || at.MaxDistance != 0 || at.Scanned != 2 || at.TransparentSkipped != 0 {
		t.Errorf("不透明像素回归：%+v, want 1 命中 / max_distance 0 / scanned 2 / skipped 0", at)
	}
}
