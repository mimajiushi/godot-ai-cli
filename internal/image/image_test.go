package image

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testImage builds a 4x2 image with a known color layout:
//
//	row 0: red, red, green, transparent
//	row 1: green, blue, blue, blue
//
// Opaque counts (threshold 200): blue 3, green 2, red 2 — the red/green tie
// exercises the hex tiebreak (#FF0000 > #00FF00 by byte order? no:
// "#00FF00" < "#FF0000", so green sorts first on the tie).
func testImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	set := func(x, y int, c color.RGBA) { img.SetRGBA(x, y, c) }
	red := color.RGBA{0xFF, 0, 0, 0xFF}
	green := color.RGBA{0, 0xFF, 0, 0xFF}
	blue := color.RGBA{0, 0, 0xFF, 0xFF}
	transparent := color.RGBA{0, 0, 0, 0}
	set(0, 0, red)
	set(1, 0, red)
	set(2, 0, green)
	set(3, 0, transparent)
	set(0, 1, green)
	set(1, 1, blue)
	set(2, 1, blue)
	set(3, 1, blue)
	return img
}

func TestPalette(t *testing.T) {
	got := Palette(testImage(), 12, 200)
	want := []ColorCount{
		{Hex: "#0000FF", Count: 3},
		{Hex: "#00FF00", Count: 2}, // tie with red: hex ascending wins
		{Hex: "#FF0000", Count: 2},
	}
	if len(got) != len(want) {
		t.Fatalf("Palette() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Palette()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestPaletteTopLimit(t *testing.T) {
	got := Palette(testImage(), 1, 200)
	if len(got) != 1 || got[0].Hex != "#0000FF" {
		t.Errorf("Palette(top=1) = %+v, want only blue", got)
	}
}

func TestPaletteAlphaThreshold(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.SetRGBA(0, 0, color.RGBA{0xFF, 0, 0, 100}) // below threshold 200
	img.SetRGBA(1, 0, color.RGBA{0, 0xFF, 0, 0xFF})
	got := Palette(img, 12, 200)
	if len(got) != 1 || got[0].Hex != "#00FF00" {
		t.Errorf("Palette() = %+v, want only green (translucent red skipped)", got)
	}
}

func TestGridPalette(t *testing.T) {
	got, err := GridPalette(testImage(), 2, 1, 200)
	if err != nil {
		t.Fatal(err)
	}
	// 2x1 tiles over a 4x2 image: 2 columns x 2 rows.
	if len(got) != 4 {
		t.Fatalf("GridPalette() returned %d tiles, want 4", len(got))
	}
	// Tile (0,0) covers (0,0)-(1,0): red x2.
	if got[0].Coord != [2]int{0, 0} || len(got[0].Top) != 1 || got[0].Top[0].Hex != "#FF0000" || got[0].Top[0].Count != 2 {
		t.Errorf("tile (0,0) = %+v, want red x2", got[0])
	}
	// Tile (1,1) covers (2,1)-(3,1): blue x2.
	if got[3].Coord != [2]int{1, 1} || len(got[3].Top) != 1 || got[3].Top[0].Hex != "#0000FF" {
		t.Errorf("tile (1,1) = %+v, want blue x2", got[3])
	}
}

func TestGridPaletteRejectsBadGeometry(t *testing.T) {
	if _, err := GridPalette(testImage(), 0, 1, 200); err == nil {
		t.Error("zero tile width must error")
	}
	if _, err := GridPalette(testImage(), 99, 1, 200); err == nil {
		t.Error("tile wider than the image must error")
	}
}

func TestProbe(t *testing.T) {
	got, err := Probe(testImage(), [][2]int{{0, 0}, {1, 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("Probe() = %+v, want 2 samples", got)
	}
	if got[0].Hex != "#FF0000" || got[0].RGBA != [4]int{255, 0, 0, 255} || got[0].At != [2]int{0, 0} {
		t.Errorf("sample[0] = %+v, want red at (0,0)", got[0])
	}
	if got[1].Hex != "#0000FF" {
		t.Errorf("sample[1] = %+v, want blue at (1,1)", got[1])
	}
}

func TestProbeOutOfBounds(t *testing.T) {
	_, err := Probe(testImage(), [][2]int{{4, 0}})
	if err == nil || !strings.Contains(err.Error(), "4x2") {
		t.Errorf("out-of-bounds probe should name the image size, got %v", err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "ok.png")
	f, err := os.Create(pngPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, testImage()); err != nil {
		t.Fatal(err)
	}
	f.Close()

	img, err := Load(pngPath)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 4 || img.Bounds().Dy() != 2 {
		t.Errorf("Load() bounds = %v, want 4x2", img.Bounds())
	}

	badPath := filepath.Join(dir, "bad.png")
	if err := os.WriteFile(badPath, []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(badPath); err == nil || !strings.Contains(err.Error(), "PNG, JPEG") {
		t.Errorf("Load(garbage) should error naming the supported formats, got %v", err)
	}
	if _, err := Load(filepath.Join(dir, "missing.png")); err == nil {
		t.Error("Load(missing) must error")
	}
}

func TestParseHexColor(t *testing.T) {
	for _, s := range []string{"#212327", "212327"} {
		r, g, b, err := ParseHexColor(s)
		if err != nil || r != 0x21 || g != 0x23 || b != 0x27 {
			t.Errorf("ParseHexColor(%q) = %d,%d,%d,%v", s, r, g, b, err)
		}
	}
	for _, s := range []string{"#123", "zzzzzz", ""} {
		if _, _, _, err := ParseHexColor(s); err == nil {
			t.Errorf("ParseHexColor(%q) must error", s)
		}
	}
}
