// 截图基线对比：把"当前这一帧"和已知的基线 PNG 逐像素比对，给出差异比例与
// 差异标注图。需求 godot-ai-cli-req-inline-material-shader-params.md（R-2）
// 的「editor screenshot 无基线对比」一项由这里支撑：CLI 侧本地计算，不经
// daemon、不碰编辑器，因此可离线复现。
package image

import (
	"fmt"
	"image"
	"image/color"
)

// diffMarkColor 是差异标注图里差异像素的标记色（洋红 #FF00FF）：同一色号的
// 出现与否本身就是定位信息，且不会和常见的红/绿/蓝断言色混淆。
var diffMarkColor = color.RGBA{R: 0xFF, G: 0x00, B: 0xFF, A: 0xFF}

// DiffStats 是一次逐像素对比的结论。DiffRatio = DiffPixels / TotalPixels，
// 阈值判定与回包字段都基于它。
type DiffStats struct {
	TotalPixels  int      `json:"total_pixels"`
	DiffPixels   int      `json:"diff_pixels"`
	DiffRatio    float64  `json:"diff_ratio"`
	SamplePoints [][2]int `json:"sample_points"` // 前若干个差异像素（sampleLimit 个）
}

// DiffImages 逐像素比较 current 与 baseline，并返回差异标注图：
// 差异像素涂成 diffMarkColor，相同像素保留 current 原样（保留上下文，
// 一眼能看出差异位置与周围画面）。两张图尺寸不一致直接报错——尺寸不同
// 时"第几个像素不同"没有意义，调用方需要先对齐分辨率。
// sampleLimit <= 0 时不采集采样点。
func DiffImages(current, baseline image.Image, sampleLimit int) (image.Image, DiffStats, error) {
	cb, bb := current.Bounds(), baseline.Bounds()
	cw, ch := cb.Dx(), cb.Dy()
	bw, bh := bb.Dx(), bb.Dy()
	if cw != bw || ch != bh {
		return nil, DiffStats{}, fmt.Errorf(
			"baseline size mismatch: captured image is %dx%d but the baseline is %dx%d",
			cw, ch, bw, bh)
	}

	out := image.NewRGBA(image.Rect(0, 0, cw, ch))
	stats := DiffStats{TotalPixels: cw * ch}
	for y := 0; y < ch; y++ {
		for x := 0; x < cw; x++ {
			cur := current.At(cb.Min.X+x, cb.Min.Y+y)
			ref := baseline.At(bb.Min.X+x, bb.Min.Y+y)
			if !samePixel(cur, ref) {
				stats.DiffPixels++
				if sampleLimit > 0 && len(stats.SamplePoints) < sampleLimit {
					stats.SamplePoints = append(stats.SamplePoints, [2]int{x, y})
				}
				out.Set(x, y, diffMarkColor)
				continue
			}
			out.Set(x, y, cur)
		}
	}
	if stats.TotalPixels > 0 {
		stats.DiffRatio = float64(stats.DiffPixels) / float64(stats.TotalPixels)
	}
	return out, stats, nil
}

// samePixel 比较两个像素的 8 位 RGBA 四通道（与 Probe / hexColor 的 8 位口径
// 一致：16 位色深图先降到 8 位再比，避免同色号的 16 位抖动被当成差异）。
func samePixel(a, b color.Color) bool {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar>>8 == br>>8 && ag>>8 == bg>>8 && ab>>8 == bb>>8 && aa>>8 == ba>>8
}
