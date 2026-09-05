package image

import (
	"fmt"
	"image"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"io"
)

// Crop returns the sub-image at (x,y,w,h) in source-image pixel coordinates,
// validating the rectangle against the bounds.
func Crop(img image.Image, x, y, w, h int) (image.Image, error) {
	b := img.Bounds()
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("region w/h must be positive (got %dx%d)", w, h)
	}
	r := image.Rect(x, y, x+w, y+h)
	if !r.In(b) {
		return nil, fmt.Errorf("region %d,%d,%d,%d is outside the %dx%d image", x, y, w, h, b.Dx(), b.Dy())
	}
	return img.(interface {
		SubImage(image.Rectangle) image.Image
	}).SubImage(r), nil
}

// DownscaleNearest shrinks img so its longest edge is at most maxEdge, using
// nearest-neighbor sampling — pixel-art frames stay crisp, unlike a smoothing
// resample. Returns img unchanged when it already fits.
func DownscaleNearest(img image.Image, maxEdge int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if maxEdge <= 0 || (w <= maxEdge && h <= maxEdge) {
		return img
	}
	scale := float64(maxEdge) / float64(max(w, h))
	nw := max(1, int(float64(w)*scale+0.5))
	nh := max(1, int(float64(h)*scale+0.5))
	out := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy := b.Min.Y + int(float64(y)*float64(h)/float64(nh))
		for x := 0; x < nw; x++ {
			sx := b.Min.X + int(float64(x)*float64(w)/float64(nw))
			out.Set(x, y, img.At(sx, sy))
		}
	}
	return out
}

// EncodeGIF writes frames as an animated GIF, quantizing each frame to the
// Plan9 palette with Floyd-Steinberg dithering (std lib only — good enough
// for debug playback). delaysMs carries per-frame delays in milliseconds.
func EncodeGIF(w io.Writer, frames []image.Image, delaysMs []int) error {
	if len(frames) == 0 {
		return fmt.Errorf("no frames to encode")
	}
	g := &gif.GIF{}
	for i, f := range frames {
		b := f.Bounds()
		pm := image.NewPaletted(b, palette.Plan9)
		drawDithered(pm, f)
		g.Image = append(g.Image, pm)
		delay := 10 // 100ms default
		if i < len(delaysMs) && delaysMs[i] > 0 {
			delay = max(2, delaysMs[i]/10) // GIF delays are in 10ms units
		}
		g.Delay = append(g.Delay, delay)
	}
	return gif.EncodeAll(w, g)
}

// drawDithered copies src onto the paletted dst with Floyd-Steinberg dithering.
func drawDithered(dst *image.Paletted, src image.Image) {
	draw.FloydSteinberg.Draw(dst, dst.Bounds(), src, src.Bounds().Min)
}
