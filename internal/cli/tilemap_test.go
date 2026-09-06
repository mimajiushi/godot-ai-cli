package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// tilemapFixture builds a one-layer project: 8x8 regions, tile atlas (0,0)
// red / (1,0) green, and one cell of each at map (0,0) / (1,0).
func tilemapFixture(t *testing.T) (scenePath, outPath string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "project.godot"), []byte("; fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	atlas := image.NewRGBA(image.Rect(0, 0, 16, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 16; x++ {
			c := color.RGBA{0xFF, 0, 0, 0xFF}
			if x >= 8 {
				c = color.RGBA{0, 0xFF, 0, 0xFF}
			}
			atlas.SetRGBA(x, y, c)
		}
	}
	f, err := os.Create(filepath.Join(dir, "tiles.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, atlas); err != nil {
		t.Fatal(err)
	}
	f.Close()

	tres := `[gd_resource type="TileSet" load_steps=3 format=3]

[ext_resource type="Texture2D" path="res://tiles.png" id="1_t"]

[sub_resource type="TileSetAtlasSource" id="TileSetAtlasSource_a"]
texture = ExtResource("1_t")
texture_region_size = Vector2i(8, 8)

[resource]
tile_size = Vector2i(8, 8)
sources/0 = SubResource("TileSetAtlasSource_a")
`
	if err := os.WriteFile(filepath.Join(dir, "tiles.tres"), []byte(tres), 0o644); err != nil {
		t.Fatal(err)
	}

	// Two cells: (1,0)→atlas(1,0) first so decoding order is exercised, then
	// (0,0)→atlas(0,0).
	buf := &bytes.Buffer{}
	buf.Write([]byte{0, 0})
	for _, v := range [12]uint16{1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0} {
		_ = binary.Write(buf, binary.LittleEndian, v)
	}
	tscn := `[gd_scene load_steps=2 format=3]

[ext_resource type="TileSet" path="res://tiles.tres" id="1_ts"]

[node name="Main" type="Node2D"]

[node name="Ground" type="TileMapLayer" parent="."]
tile_set = ExtResource("1_ts")
tile_map_data = PackedByteArray("` + base64.StdEncoding.EncodeToString(buf.Bytes()) + `")
`
	scenePath = filepath.Join(dir, "map.tscn")
	if err := os.WriteFile(scenePath, []byte(tscn), 0o644); err != nil {
		t.Fatal(err)
	}
	return scenePath, filepath.Join(dir, "map.png")
}

func runTilemap(t *testing.T, args ...string) map[string]any {
	t.Helper()
	root := NewRootCommand()
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("tilemap %v: %v\n%s", args, err, buf.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, buf.String())
	}
	return payload
}

// TestTilemapDumpCommand: cells decode from the .tscn text, sorted by x,y.
func TestTilemapDumpCommand(t *testing.T) {
	scenePath, _ := tilemapFixture(t)
	payload := runTilemap(t, "tilemap", "dump", "--scene", scenePath)
	layers, _ := payload["layers"].([]any)
	if len(layers) != 1 {
		t.Fatalf("layers = %v", layers)
	}
	layer := layers[0].(map[string]any)
	if layer["layer"] != "Ground" {
		t.Errorf("layer name = %v", layer["layer"])
	}
	cells, _ := layer["cells"].([]any)
	if len(cells) != 2 {
		t.Fatalf("cells = %v", cells)
	}
	first := cells[0].(map[string]any)
	if first["x"].(float64) != 0 || first["y"].(float64) != 0 {
		t.Errorf("first cell = %v, want (0,0) after sorting", first)
	}
	second := cells[1].(map[string]any)
	atlas, _ := second["atlas"].([]any)
	if len(atlas) != 2 || atlas[0].(float64) != 1 || atlas[1].(float64) != 0 {
		t.Errorf("second cell atlas = %v, want [1 0]", second["atlas"])
	}
}

// TestTilemapRenderCommand: the map PNG is composited from the atlas.
func TestTilemapRenderCommand(t *testing.T) {
	scenePath, outPath := tilemapFixture(t)
	payload := runTilemap(t, "tilemap", "render", "--scene", scenePath, "--out", outPath)
	if payload["layer"] != "Ground" {
		t.Errorf("layer = %v", payload["layer"])
	}
	size, _ := payload["size"].([]any)
	if len(size) != 2 || size[0].(float64) != 16 || size[1].(float64) != 8 {
		t.Errorf("size = %v, want [16 8]", size)
	}
	got := decodeFile(t, outPath)
	if got.Bounds().Dx() != 16 || got.Bounds().Dy() != 8 {
		t.Fatalf("rendered bounds = %v", got.Bounds())
	}
	left := color.RGBAModel.Convert(got.At(4, 4)).(color.RGBA)
	right := color.RGBAModel.Convert(got.At(12, 4)).(color.RGBA)
	if left != (color.RGBA{0xFF, 0, 0, 0xFF}) || right != (color.RGBA{0, 0xFF, 0, 0xFF}) {
		t.Errorf("rendered cells = %v / %v, want red / green", left, right)
	}
}
