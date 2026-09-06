package tilemapfile

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// encodeCells builds a tile_map_data payload the way Godot 4 serializes it:
// 2-byte header + 12 little-endian bytes per cell (<hhhhhh).
func encodeCells(cells ...Cell) string {
	buf := &bytes.Buffer{}
	buf.Write([]byte{0, 0}) // 2-byte header
	for _, c := range cells {
		for _, v := range [6]uint16{
			uint16(int16(c.X)), uint16(int16(c.Y)),
			uint16(c.Source), uint16(c.Atlas[0]), uint16(c.Atlas[1]), uint16(c.Alternative),
		} {
			_ = binary.Write(buf, binary.LittleEndian, v)
		}
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// writeAtlasPNG writes a solid-tile test atlas. tiles maps atlas coords to
// the color filling that 8x8 tile; tile (ax,ay) sits at pixel
// (margin + ax*(8+sep), margin + ay*(8+sep)).
func writeAtlasPNG(t *testing.T, path string, margin, sep int, tiles map[[2]int]color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for at, c := range tiles {
		px := margin + at[0]*(8+sep)
		py := margin + at[1]*(8+sep)
		for y := py; y < py+8; y++ {
			for x := px; x < px+8; x++ {
				img.SetRGBA(x, y, c)
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// fixtureProject builds a minimal project: project.godot, two atlas PNGs,
// a TileSet .tres (two sources, source 0 with margin/separation 1) and a
// scene with two TileMapLayer nodes. Returns the scene path.
func fixtureProject(t *testing.T, groundCells, decorCells []Cell) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "project.godot"), []byte("; test fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	red := color.RGBA{0xFF, 0, 0, 0xFF}
	green := color.RGBA{0, 0xFF, 0, 0xFF}
	blue := color.RGBA{0, 0, 0xFF, 0xFF}
	yellow := color.RGBA{0xFF, 0xFF, 0, 0xFF}
	magenta := color.RGBA{0xFF, 0, 0xFF, 0xFF}

	// Source 0: margin 1, separation 1 → tile (1,0) starts at pixel x=10.
	writeAtlasPNG(t, filepath.Join(root, "tiles0.png"), 1, 1, map[[2]int]color.RGBA{
		{0, 0}: red, {1, 0}: green, {0, 1}: blue, {1, 1}: yellow,
	})
	// Source 1: no margin/separation → tile (0,0) at pixel (0,0).
	writeAtlasPNG(t, filepath.Join(root, "tiles1.png"), 0, 0, map[[2]int]color.RGBA{
		{0, 0}: magenta,
	})

	tres := `[gd_resource type="TileSet" load_steps=5 format=3 uid="uid://ts"]

[ext_resource type="Texture2D" uid="uid://t0" path="res://tiles0.png" id="1_t0"]
[ext_resource type="Texture2D" uid="uid://t1" path="res://tiles1.png" id="2_t1"]

[sub_resource type="TileSetAtlasSource" id="TileSetAtlasSource_a"]
texture = ExtResource("1_t0")
margin = Vector2i(1, 1)
separation = Vector2i(1, 1)
texture_region_size = Vector2i(8, 8)

[sub_resource type="TileSetAtlasSource" id="TileSetAtlasSource_b"]
texture = ExtResource("2_t1")
texture_region_size = Vector2i(8, 8)

[resource]
tile_size = Vector2i(8, 8)
sources/0 = SubResource("TileSetAtlasSource_a")
sources/1 = SubResource("TileSetAtlasSource_b")
`
	if err := os.WriteFile(filepath.Join(root, "tiles.tres"), []byte(tres), 0o644); err != nil {
		t.Fatal(err)
	}

	tscn := `[gd_scene load_steps=2 format=3 uid="uid://scene"]

[ext_resource type="TileSet" uid="uid://ts" path="res://tiles.tres" id="1_ts"]

[node name="Main" type="Node2D"]

[node name="Ground" type="TileMapLayer" parent="."]
tile_set = ExtResource("1_ts")
tile_map_data = PackedByteArray("` + encodeCells(groundCells...) + `")

[node name="Decor" type="TileMapLayer" parent="."]
tile_map_data = PackedByteArray("` + encodeCells(decorCells...) + `")
tile_set = ExtResource("1_ts")
`
	scenePath := filepath.Join(root, "map.tscn")
	if err := os.WriteFile(scenePath, []byte(tscn), 0o644); err != nil {
		t.Fatal(err)
	}
	return scenePath
}

// groundFixtureCells are deliberately unsorted and include a negative
// coordinate plus one alternative != 0 cell.
func groundFixtureCells() []Cell {
	return []Cell{
		{X: 2, Y: 1, Source: 0, Atlas: [2]int{0, 1}},                 // blue
		{X: -1, Y: 0, Source: 0, Atlas: [2]int{1, 1}},                // yellow
		{X: 0, Y: 0, Source: 0, Atlas: [2]int{1, 0}, Alternative: 2}, // green, alt ignored
		{X: 1, Y: 0, Source: 1, Atlas: [2]int{0, 0}},                 // magenta
	}
}

func TestParseSceneDump(t *testing.T) {
	scenePath := fixtureProject(t, groundFixtureCells(), []Cell{{X: 0, Y: 0, Source: 0, Atlas: [2]int{0, 0}}})
	sc, err := ParseScene(scenePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Layers) != 2 || sc.Layers[0].Name != "Ground" || sc.Layers[1].Name != "Decor" {
		t.Fatalf("layers = %+v", sc.Layers)
	}
	if sc.Layers[0].TileSetRef != "1_ts" || sc.Layers[1].TileSetRef != "1_ts" {
		t.Errorf("tile_set refs = %q / %q", sc.Layers[0].TileSetRef, sc.Layers[1].TileSetRef)
	}
	ext, ok := sc.ExtResources["1_ts"]
	if !ok || ext.Path != "res://tiles.tres" || ext.Type != "TileSet" {
		t.Errorf("ext_resource 1_ts = %+v (ok=%v)", ext, ok)
	}

	got := sc.Layers[0].Cells
	if len(got) != 4 {
		t.Fatalf("decoded %d cells, want 4", len(got))
	}
	// Sorted by x, then y; the negative int16 must survive the round trip.
	wantOrder := [][2]int{{-1, 0}, {0, 0}, {1, 0}, {2, 1}}
	for i, w := range wantOrder {
		if got[i].X != w[0] || got[i].Y != w[1] {
			t.Errorf("cells[%d] = (%d,%d), want (%d,%d)", i, got[i].X, got[i].Y, w[0], w[1])
		}
	}
	if got[1].Alternative != 2 {
		t.Errorf("alternative = %d, want 2", got[1].Alternative)
	}
	if got[2].Source != 1 {
		t.Errorf("cells[2] = %+v, want the source-1 cell", got[2])
	}
	if got[3].Atlas != [2]int{0, 1} {
		t.Errorf("cells[3] = %+v", got[3])
	}

	if l, err := sc.FindLayer("Decor"); err != nil || len(l.Cells) != 1 {
		t.Errorf("FindLayer(Decor) = %+v, %v", l, err)
	}
	if _, err := sc.FindLayer("Nope"); err == nil {
		t.Error("FindLayer(Nope) must fail")
	}
}

func TestRenderLayer(t *testing.T) {
	scenePath := fixtureProject(t, groundFixtureCells(), []Cell{{X: 0, Y: 0, Source: 0, Atlas: [2]int{0, 0}}})
	sc, err := ParseScene(scenePath)
	if err != nil {
		t.Fatal(err)
	}
	res, err := RenderLayer(sc, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Layer != "Ground" || res.Cells != 4 {
		t.Errorf("rendered layer %q with %d cells", res.Layer, res.Cells)
	}
	if res.AlternativeIgnored != 1 {
		t.Errorf("AlternativeIgnored = %d, want 1", res.AlternativeIgnored)
	}
	// Cell range x -1..2, y 0..1 at 8x8 regions → 32x16 canvas.
	b := res.Image.Bounds()
	if b.Dx() != 32 || b.Dy() != 16 {
		t.Fatalf("canvas = %v, want 32x16", b)
	}
	// Sample the center pixel of each rendered cell (offset: minX=-1, minY=0).
	checks := []struct {
		px, py int
		name   string
		want   color.RGBA
	}{
		{0*8 + 4, 0*8 + 4, "(-1,0) atlas(1,1)", color.RGBA{0xFF, 0xFF, 0, 0xFF}},
		{1*8 + 4, 0*8 + 4, "(0,0) atlas(1,0)", color.RGBA{0, 0xFF, 0, 0xFF}},
		{2*8 + 4, 0*8 + 4, "(1,0) src1 atlas(0,0)", color.RGBA{0xFF, 0, 0xFF, 0xFF}},
		{3*8 + 4, 1*8 + 4, "(2,1) atlas(0,1)", color.RGBA{0, 0, 0xFF, 0xFF}},
	}
	for _, c := range checks {
		got := color.RGBAModel.Convert(res.Image.At(c.px, c.py)).(color.RGBA)
		if got != c.want {
			t.Errorf("%s: pixel (%d,%d) = %v, want %v", c.name, c.px, c.py, got, c.want)
		}
	}

	// Named-layer render: Decor is 1 cell → 8x8 canvas of red.
	res2, err := RenderLayer(sc, "Decor")
	if err != nil {
		t.Fatal(err)
	}
	if b := res2.Image.Bounds(); b.Dx() != 8 || b.Dy() != 8 {
		t.Errorf("Decor canvas = %v, want 8x8", b)
	}
	if got := color.RGBAModel.Convert(res2.Image.At(4, 4)).(color.RGBA); got != (color.RGBA{0xFF, 0, 0, 0xFF}) {
		t.Errorf("Decor pixel = %v, want red", got)
	}
}

func TestDecodeTileMapDataErrors(t *testing.T) {
	if _, err := DecodeTileMapData("!!!not-base64!!!"); err == nil {
		t.Error("invalid base64 accepted")
	}
	// 2-byte header + 5 bytes: not a multiple of 12.
	bad := base64.StdEncoding.EncodeToString([]byte{0, 0, 1, 2, 3, 4, 5})
	if _, err := DecodeTileMapData(bad); err == nil {
		t.Error("truncated payload accepted")
	}
	if got, err := DecodeTileMapData(base64.StdEncoding.EncodeToString([]byte{0, 0})); err != nil || len(got) != 0 {
		t.Errorf("header-only payload = %v, %v; want empty, nil", got, err)
	}
}

func TestParseTileSetDefaults(t *testing.T) {
	// No margin/separation lines and no texture_region_size on the source →
	// zeros and the TileSet tile_size fallback.
	tres := `[gd_resource type="TileSet" load_steps=3 format=3]

[ext_resource type="Texture2D" path="res://a.png" id="1_a"]

[sub_resource type="TileSetAtlasSource" id="TileSetAtlasSource_a"]
texture = ExtResource("1_a")

[resource]
tile_size = Vector2i(32, 32)
sources/0 = SubResource("TileSetAtlasSource_a")
`
	ts, err := parseTileSetText("x.tres", tres)
	if err != nil {
		t.Fatal(err)
	}
	src := ts.Sources[0]
	if src == nil {
		t.Fatal("source 0 missing")
	}
	if src.RegionSize != [2]int{32, 32} {
		t.Errorf("RegionSize = %v, want tile_size fallback [32 32]", src.RegionSize)
	}
	if src.Margin != [2]int{0, 0} || src.Separation != [2]int{0, 0} {
		t.Errorf("Margin/Separation = %v/%v, want zero defaults", src.Margin, src.Separation)
	}
	if src.TexturePath != "res://a.png" {
		t.Errorf("TexturePath = %q", src.TexturePath)
	}
}
