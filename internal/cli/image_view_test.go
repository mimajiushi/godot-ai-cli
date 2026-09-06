package cli

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// runImageView builds a 2x1 red/blue source PNG and runs `image view` on it.
func runImageView(t *testing.T, extraArgs ...string) (map[string]any, string) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "src.png")
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.SetRGBA(0, 0, color.RGBA{0xFF, 0, 0, 0xFF})
	img.SetRGBA(1, 0, color.RGBA{0, 0, 0xFF, 0xFF})
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()

	out := filepath.Join(dir, "out.png")
	args := append([]string{"view", "--path", src, "--out", out}, extraArgs...)
	cmd := newImageCommand()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("image view %v: %v\n%s", args, err, buf.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, buf.String())
	}
	return payload, out
}

func decodeFile(t *testing.T, path string) image.Image {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// TestImageViewUpscale: default flow upscales the whole image by --scale.
func TestImageViewUpscale(t *testing.T) {
	payload, out := runImageView(t, "--scale", "3")
	size, _ := payload["size"].([]any)
	if len(size) != 2 || size[0].(float64) != 6 || size[1].(float64) != 3 {
		t.Errorf("size = %v, want [6 3]", size)
	}
	got := decodeFile(t, out)
	if got.Bounds().Dx() != 6 || got.Bounds().Dy() != 3 {
		t.Fatalf("output bounds = %v, want 6x3", got.Bounds())
	}
	left := color.RGBAModel.Convert(got.At(1, 1)).(color.RGBA)
	right := color.RGBAModel.Convert(got.At(4, 1)).(color.RGBA)
	if left != (color.RGBA{0xFF, 0, 0, 0xFF}) || right != (color.RGBA{0, 0, 0xFF, 0xFF}) {
		t.Errorf("pixels = %v / %v, want solid red / blue blocks", left, right)
	}
}

// TestImageViewRegion: --region crops in source pixels before the upscale.
func TestImageViewRegion(t *testing.T) {
	_, out := runImageView(t, "--region", "1,0,1,1", "--scale", "2")
	got := decodeFile(t, out)
	if got.Bounds().Dx() != 2 || got.Bounds().Dy() != 2 {
		t.Fatalf("output bounds = %v, want 2x2", got.Bounds())
	}
	if c := color.RGBAModel.Convert(got.At(0, 0)).(color.RGBA); c != (color.RGBA{0, 0, 0xFF, 0xFF}) {
		t.Errorf("pixel = %v, want blue", c)
	}
}

// TestImageViewBadParams: an out-of-bounds region and a zero scale are
// reported as parameter errors, not panics.
func TestImageViewBadParams(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.png")
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	f, _ := os.Create(src)
	_ = png.Encode(f, img)
	f.Close()

	for _, args := range [][]string{
		{"view", "--path", src, "--out", filepath.Join(dir, "o.png"), "--region", "5,0,1,1"},
		{"view", "--path", src, "--out", filepath.Join(dir, "o.png"), "--scale", "0"},
	} {
		cmd := newImageCommand()
		buf := &bytes.Buffer{}
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Errorf("image view %v succeeded, want INVALID_PARAMS", args)
		}
	}
}
