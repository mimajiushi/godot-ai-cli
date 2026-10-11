package image

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strconv"
	"strings"
)

// 按颜色反查像素（需求 R-5 4.1）："红点=刷怪点"这类美术约定要求先回答
// "这张图集里哪些图块含指定颜色的像素"，而手搓阈值 + 逐像素扫既慢又不可
// 复现。命中判定写死在这里，帮助文本与 references/commands.md 引用同一口径：
//
//	distance = sqrt(Δr² + Δg² + Δb²)，取 8 位**直通**（非预乘）通道（0..255）
//	命中：distance <= tolerance（等于阈值也算命中）
//	alpha == 0 的像素不参与比对（透明填充没有颜色信息；alpha 本身不受预乘影响）
//
// 通道口径的坑见 pixelStraightRGBA8：Go 的 RGBA() 是预乘值，直接拿来算距离会
// 让半透明标记像素（α<255 的抗锯齿边）系统性漏掉。
//
// 坐标口径：centroid / extent / pixels 全部是"块内局部坐标"（相对所在块的
// 左上角）；缺省整图一块时即图内坐标。max_distance 回报命中像素里最远的一个，
// 用来判断"阈值是不是已经贴到误伤边缘"。

// ColorExtent 是命中像素在块内的包围范围（含端点，块内局部坐标）。
type ColorExtent struct {
	X [2]int `json:"x"`
	Y [2]int `json:"y"`
}

// ColorHitStats 是一片矩形区域的一次命中统计（块内局部坐标）。
// PixelCount == 0 表示该区域没有命中（此时 Extent 为零值）。
type ColorHitStats struct {
	Scanned            int         // 参与比对的像素数（alpha != 0）
	TransparentSkipped int         // 因全透明被跳过的像素数
	PixelCount         int         // 命中像素数
	Centroid           [2]float64  // 命中像素质心（两位小数）
	Extent             ColorExtent // 命中像素包围范围
	MaxDistance        float64     // 命中像素里最大的 RGB 欧氏距离（两位小数）
	Pixels             [][2]int    // 命中像素坐标（块内局部，wantPixels 时采集）
}

// ColorFindResult 是 ColorFind 的输出。
type ColorFindResult struct {
	Size               [2]int       `json:"size"`
	RegionSize         [2]int       `json:"region_size"`
	Matches            []ColorMatch `json:"matches"`
	Scanned            int          `json:"scanned"`
	TransparentSkipped int          `json:"transparent_skipped"`
}

// ColorMatch 是一个分块（region）的命中统计。Region 是块索引 "cx,cy"。
type ColorMatch struct {
	Region      string      `json:"region"`
	PixelCount  int         `json:"pixel_count"`
	Centroid    [2]float64  `json:"centroid"`
	Extent      ColorExtent `json:"extent"`
	MaxDistance float64     `json:"max_distance"`
	Pixels      [][2]int    `json:"pixels,omitempty"`
}

// ColorDistance 是两个 8 位 RGB 颜色的欧氏距离（0..441.67）。image find-color
// 与 tilemap find-by-color 共用它，保证两条命令的阈值语义完全一致。
func ColorDistance(a, b [3]int) float64 {
	dr := float64(a[0] - b[0])
	dg := float64(a[1] - b[1])
	db := float64(a[2] - b[2])
	return math.Sqrt(dr*dr + dg*dg + db*db)
}

// pixelStraightRGBA8 取一个像素的**直通**（非预乘）8 位 RGBA 通道。
//
// Go 的 color.Color.RGBA() 按约定返回**预乘**通道：α=128 的纯红会读成
// R=128、G=0、B=0，直接 >>8 参与距离就与本文档写死的"8 位 R,G,B 欧氏距离"
// 口径不符——半透明/抗锯齿的标记像素会被系统性漏掉（tolerance 越小越明显）。
// 所以这里走 NRGBA 模型换算拿到未预乘的原始通道：*image.NRGBA（Godot 的
// 8 位 PNG 解码结果就是它）走零拷贝快路径，其余图像类型交给
// color.NRGBAModel 换算（换算对 α=0/255 都是恒等，故不透明像素的含义不变）。
// alpha 不受预乘影响，可直接用于 α==0 跳过判定。
func pixelStraightRGBA8(img image.Image, x, y int) (r, g, b, a uint8) {
	if n, ok := img.(*image.NRGBA); ok {
		i := n.PixOffset(x, y)
		return n.Pix[i], n.Pix[i+1], n.Pix[i+2], n.Pix[i+3]
	}
	c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
	return c.R, c.G, c.B, c.A
}

// ScanColor 扫描 rect（img 坐标系里的矩形，与 img 求交）内命中目标颜色的
// 像素，返回块内局部坐标的统计。wantPixels 为真时采集全部命中坐标。
func ScanColor(img image.Image, rect image.Rectangle, target [3]int, tolerance float64, wantPixels bool) ColorHitStats {
	var st ColorHitStats
	r := rect.Intersect(img.Bounds())
	if r.Empty() {
		return st
	}
	minX, minY, maxX, maxY := -1, -1, -1, -1
	sumX, sumY := 0, 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			cr, cg, cb, a := pixelStraightRGBA8(img, x, y)
			if a == 0 {
				st.TransparentSkipped++
				continue
			}
			st.Scanned++
			d := ColorDistance([3]int{int(cr), int(cg), int(cb)}, target)
			if d > tolerance {
				continue
			}
			lx, ly := x-r.Min.X, y-r.Min.Y
			st.PixelCount++
			sumX += lx
			sumY += ly
			if minX < 0 || lx < minX {
				minX = lx
			}
			if minY < 0 || ly < minY {
				minY = ly
			}
			if lx > maxX {
				maxX = lx
			}
			if ly > maxY {
				maxY = ly
			}
			if d > st.MaxDistance {
				st.MaxDistance = d
			}
			if wantPixels {
				st.Pixels = append(st.Pixels, [2]int{lx, ly})
			}
		}
	}
	if st.PixelCount == 0 {
		return st
	}
	st.Centroid = [2]float64{
		round2(float64(sumX) / float64(st.PixelCount)),
		round2(float64(sumY) / float64(st.PixelCount)),
	}
	st.Extent = ColorExtent{X: [2]int{minX, maxX}, Y: [2]int{minY, maxY}}
	st.MaxDistance = round2(st.MaxDistance)
	return st
}

// ColorFind 按 blockW x blockH 把 img 切成图集网格逐块统计命中（块序按
// (cy, cx)，只输出至少有 1 个命中的块）。blockW/blockH <= 0 时整图当一块，
// RegionSize 回报为图片尺寸。残缺的末行/末列块按实际可见区域统计。
func ColorFind(img image.Image, target [3]int, tolerance float64, blockW, blockH int, wantPixels bool) ColorFindResult {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if blockW <= 0 || blockH <= 0 {
		blockW, blockH = w, h
	}
	res := ColorFindResult{
		Size:       [2]int{w, h},
		RegionSize: [2]int{blockW, blockH},
		Matches:    []ColorMatch{},
	}
	if w <= 0 || h <= 0 || blockW <= 0 || blockH <= 0 {
		return res
	}
	cols := (w + blockW - 1) / blockW
	rows := (h + blockH - 1) / blockH
	for cy := 0; cy < rows; cy++ {
		for cx := 0; cx < cols; cx++ {
			rect := image.Rect(
				b.Min.X+cx*blockW, b.Min.Y+cy*blockH,
				min(b.Min.X+(cx+1)*blockW, b.Max.X), min(b.Min.Y+(cy+1)*blockH, b.Max.Y))
			st := ScanColor(img, rect, target, tolerance, wantPixels)
			res.Scanned += st.Scanned
			res.TransparentSkipped += st.TransparentSkipped
			if st.PixelCount == 0 {
				continue
			}
			res.Matches = append(res.Matches, ColorMatch{
				Region:      fmt.Sprintf("%d,%d", cx, cy),
				PixelCount:  st.PixelCount,
				Centroid:    st.Centroid,
				Extent:      st.Extent,
				MaxDistance: st.MaxDistance,
				Pixels:      st.Pixels,
			})
		}
	}
	return res
}

// ParseColorSpec 解析目标颜色："R,G,B"（各自 0..255）或 "#RRGGBB" / "RRGGBB"。
// 只有这两种写法，避免出现"阈值算法各自发挥"式的歧义。
func ParseColorSpec(raw string) ([3]int, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return [3]int{}, fmt.Errorf("want R,G,B or #RRGGBB, got an empty value")
	}
	if strings.Contains(s, ",") {
		parts := strings.Split(s, ",")
		if len(parts) != 3 {
			return [3]int{}, fmt.Errorf("want three comma-separated channels R,G,B, got %q", raw)
		}
		var out [3]int
		for i, p := range parts {
			v, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil || v < 0 || v > 255 {
				return [3]int{}, fmt.Errorf("channel %d must be an integer 0..255, got %q", i+1, strings.TrimSpace(p))
			}
			out[i] = v
		}
		return out, nil
	}
	r, g, b, err := ParseHexColor(s)
	if err != nil {
		return [3]int{}, fmt.Errorf("want R,G,B or #RRGGBB, got %q", raw)
	}
	return [3]int{r, g, b}, nil
}

// ColorHex 把 8 位 RGB 渲染成 "#RRGGBB"（与 image palette/probe 的十六进制
// 口径一致，便于回包和 -h 示例互相印证）。
func ColorHex(c [3]int) string {
	return hexColor(c[0], c[1], c[2])
}

// round2 把浮点结果收敛到两位小数：回包要能稳定 diff / 断言，不受浮点末位噪声影响。
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
