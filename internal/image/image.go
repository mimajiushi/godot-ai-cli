// Package image provides local image analysis (palette extraction and pixel
// probes) on PNG/JPEG files — no editor, daemon, or third-party runtime
// required. It backs the `godot-ai-cli image` command group so agents can
// do color analysis and render verification without shelling out to
// Python/PIL.
package image

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg" // register JPEG decoding for image.Decode
	_ "image/png"  // register PNG decoding for image.Decode
	"os"
	"sort"
	"strings"
)

// Load decodes a PNG or JPEG file. Any other format (e.g. WebP, which the
// standard library does not decode) errors with an explicit supported-format
// note.
func Load(path string) (image.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return DecodeBytes(data)
}

// DecodeBytes decodes PNG/JPEG bytes — used both for files on disk (Load)
// and for screenshots returned by the daemon as base64.
func DecodeBytes(data []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%v (supported formats: PNG, JPEG)", err)
	}
	return img, nil
}

// ColorCount is one dominant color with its opaque-pixel count.
type ColorCount struct {
	Hex   string `json:"hex"` // "#RRGGBB"
	Count int    `json:"count"`
}

// TilePalette is the palette of one grid cell.
type TilePalette struct {
	Coord [2]int       `json:"coord"`
	Top   []ColorCount `json:"top"`
}

// Sample is one probed pixel.
type Sample struct {
	At   [2]int `json:"at"`
	Hex  string `json:"hex"` // "#RRGGBB"
	RGBA [4]int `json:"rgba"`
}

// Palette counts opaque (alpha >= alphaThreshold) pixels and returns the top
// N colors, count-descending with a hex tiebreak for determinism.
func Palette(img image.Image, top, alphaThreshold int) []ColorCount {
	counts := countColors(img, img.Bounds(), alphaThreshold)
	return topColors(counts, top)
}

// GridPalette splits the image into tileW x tileH cells (a partial last
// row/column is analyzed at its real size) and returns each cell's top
// colors — top 3 per tile, mirroring the reference workflow.
func GridPalette(img image.Image, tileW, tileH, alphaThreshold int) ([]TilePalette, error) {
	if tileW <= 0 || tileH <= 0 {
		return nil, fmt.Errorf("tile size must be positive, got %dx%d", tileW, tileH)
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if tileW > w || tileH > h {
		return nil, fmt.Errorf("tile size %dx%d exceeds the image size %dx%d", tileW, tileH, w, h)
	}
	var out []TilePalette
	for ty := 0; ty*tileH < h; ty++ {
		for tx := 0; tx*tileW < w; tx++ {
			cell := image.Rect(b.Min.X+tx*tileW, b.Min.Y+ty*tileH,
				min(b.Min.X+(tx+1)*tileW, b.Max.X), min(b.Min.Y+(ty+1)*tileH, b.Max.Y))
			out = append(out, TilePalette{
				Coord: [2]int{tx, ty},
				Top:   topColors(countColors(img, cell, alphaThreshold), 3),
			})
		}
	}
	return out, nil
}

// Probe reads the pixels at the given coordinates. An out-of-bounds point
// errors with the image size for context.
func Probe(img image.Image, points [][2]int) ([]Sample, error) {
	b := img.Bounds()
	out := make([]Sample, 0, len(points))
	for _, p := range points {
		x, y := b.Min.X+p[0], b.Min.Y+p[1]
		if x < b.Min.X || x >= b.Max.X || y < b.Min.Y || y >= b.Max.Y {
			return nil, fmt.Errorf("point (%d,%d) is outside the %dx%d image", p[0], p[1], b.Dx(), b.Dy())
		}
		r, g, bl, a := img.At(x, y).RGBA()
		out = append(out, Sample{
			At:   p,
			Hex:  hexColor(int(r>>8), int(g>>8), int(bl>>8)),
			RGBA: [4]int{int(r >> 8), int(g >> 8), int(bl >> 8), int(a >> 8)},
		})
	}
	return out, nil
}

// countColors tallies opaque pixels inside rect (16.16 fixed-point RGBA is
// shifted down to 8-bit channels).
func countColors(img image.Image, rect image.Rectangle, alphaThreshold int) map[[3]int]int {
	counts := map[[3]int]int{}
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if int(a>>8) < alphaThreshold {
				continue
			}
			counts[[3]int{int(r >> 8), int(g >> 8), int(b >> 8)}]++
		}
	}
	return counts
}

// topColors renders the N most frequent colors with a deterministic order:
// count descending, hex ascending on ties.
func topColors(counts map[[3]int]int, top int) []ColorCount {
	out := make([]ColorCount, 0, len(counts))
	for c, n := range counts {
		out = append(out, ColorCount{Hex: hexColor(c[0], c[1], c[2]), Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Hex < out[j].Hex
	})
	if top > 0 && len(out) > top {
		out = out[:top]
	}
	return out
}

// hexColor renders 8-bit channels as "#RRGGBB".
func hexColor(r, g, b int) string {
	return fmt.Sprintf("#%02X%02X%02X", r, g, b)
}

// ParseHexColor parses "#RRGGBB" / "RRGGBB" into 8-bit channels.
func ParseHexColor(s string) (r, g, b int, err error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return 0, 0, 0, fmt.Errorf("want #RRGGBB, got %q", s)
	}
	if _, err := fmt.Sscanf(s, "%02x%02x%02x", &r, &g, &b); err != nil {
		return 0, 0, 0, fmt.Errorf("want #RRGGBB, got %q", s)
	}
	return r, g, b, nil
}
