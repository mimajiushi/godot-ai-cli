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
	"regexp"
	"strconv"
	"strings"
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
	// stderr 单独收：tilemap render 的 alternative 提示写在 stderr，混进
	// stdout 会让 JSON 解析失败。
	root.SetErr(&bytes.Buffer{})
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
	// 格映射信息（需求 R-5 4.3）：这组 fixture 的最小格是 (0,0)、格 8x8，
	// 所以 origin 就是 [0 0]，cell_px_size 为 [8 8]。
	origin, _ := payload["origin"].([]any)
	cellPx, _ := payload["cell_px_size"].([]any)
	if len(origin) != 2 || origin[0].(float64) != 0 || origin[1].(float64) != 0 {
		t.Errorf("origin = %v, want [0 0]", payload["origin"])
	}
	if len(cellPx) != 2 || cellPx[0].(float64) != 8 || cellPx[1].(float64) != 8 {
		t.Errorf("cell_px_size = %v, want [8 8]", payload["cell_px_size"])
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

// tilemapColorFixture 造一个"格子不在原点、图层带 position"的工程：
// 8x8 图集（左格纯白 = atlas (0,0)，右格白底 + 3 个红点 = atlas (1,0)），
// 场景 3 格——(2,3) 红点、(2,4) 红点且 alternative=1、(-1,2) 白格，
// 图层 position = Vector2(16, 8)。
func tilemapColorFixture(t *testing.T) (scenePath, outPath string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "project.godot"), []byte("; fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	atlas := image.NewRGBA(image.Rect(0, 0, 16, 8))
	white := color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}
	for y := 0; y < 8; y++ {
		for x := 0; x < 16; x++ {
			atlas.SetRGBA(x, y, white)
		}
	}
	// 右格（图集 (1,0) = 纹理 x 8..15）里三个红点：格内局部 (1,1) (3,2) (5,3)，
	// 质心恰好是 (3,2)。
	for _, p := range [][2]int{{1, 1}, {3, 2}, {5, 3}} {
		atlas.SetRGBA(8+p[0], p[1], color.RGBA{0xFF, 0, 0, 0xFF})
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

	// tile_map_data：2 字节头 + 每格 12 字节 <hhhhhh。
	buf := &bytes.Buffer{}
	buf.Write([]byte{0, 0})
	for _, c := range [][6]int{
		{-1, 2, 0, 0, 0, 0}, // 白格，不该被红点反查命中
		{2, 3, 0, 1, 0, 0},
		{2, 4, 0, 1, 0, 1}, // alternative = 1：按 alternative 0 判定并计数
	} {
		for _, v := range c {
			_ = binary.Write(buf, binary.LittleEndian, uint16(int16(v)))
		}
	}
	tscn := `[gd_scene load_steps=2 format=3]

[ext_resource type="TileSet" path="res://tiles.tres" id="1_ts"]

[node name="Main" type="Node2D"]

[node name="Ground" type="TileMapLayer" parent="."]
tile_set = ExtResource("1_ts")
position = Vector2(16, 8)
tile_map_data = PackedByteArray("` + base64.StdEncoding.EncodeToString(buf.Bytes()) + `")
`
	scenePath = filepath.Join(dir, "map.tscn")
	if err := os.WriteFile(scenePath, []byte(tscn), 0o644); err != nil {
		t.Fatal(err)
	}
	return scenePath, filepath.Join(dir, "map.png")
}

// runTilemapErr 跑一条应当失败的 tilemap 子命令，经 root 的 execute() 包装
// 返回结构化错误信封（cobra 自己的 Execute 不会打印它）。
func runTilemapErr(t *testing.T, args ...string) string {
	t.Helper()
	root := NewRootCommand()
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(args)
	if code := execute(root); code == 0 {
		t.Fatalf("tilemap %v succeeded, want error", args)
	}
	return buf.String()
}

// TestTilemapFindByColorCommand：按图集像素颜色反查格子；world_center 是格
// 中心（已叠加图层 position），cells 与 world_center 一一对应。
func TestTilemapFindByColorCommand(t *testing.T) {
	scenePath, _ := tilemapColorFixture(t)
	p := runTilemap(t, "tilemap", "find-by-color", "--scene", scenePath,
		"--layer", "Ground", "--color", "255,0,0", "--tolerance", "0")

	if p["layer"] != "Ground" || p["color"] != "#FF0000" || p["scene"] != scenePath {
		t.Errorf("layer/color/scene = %v / %v / %v", p["layer"], p["color"], p["scene"])
	}
	rs, _ := p["region_size"].([]any)
	if len(rs) != 2 || rs[0].(float64) != 8 || rs[1].(float64) != 8 {
		t.Errorf("region_size = %v, want [8 8]", rs)
	}
	if p["scanned_cells"].(float64) != 3 {
		t.Errorf("scanned_cells = %v, want 3", p["scanned_cells"])
	}
	// 两格命中（(2,3) 与 (2,4)），其中 (2,4) 的 alternative=1 按 0 判定。
	if p["alternative_ignored"].(float64) != 1 {
		t.Errorf("alternative_ignored = %v, want 1", p["alternative_ignored"])
	}
	cells, _ := p["cells"].([]any)
	if len(cells) != 2 {
		t.Fatalf("cells = %v, want 2 个红点格", cells)
	}
	first := cells[0].(map[string]any)
	atlas, _ := first["atlas"].([]any)
	centroid, _ := first["centroid_in_cell"].([]any)
	if first["x"].(float64) != 2 || first["y"].(float64) != 3 || first["source"].(float64) != 0 {
		t.Errorf("first cell = %v, want (2,3) source 0", first)
	}
	if atlas[0].(float64) != 1 || atlas[1].(float64) != 0 {
		t.Errorf("first cell atlas = %v, want [1 0]", first["atlas"])
	}
	if first["pixel_count"].(float64) != 3 {
		t.Errorf("first cell pixel_count = %v, want 3", first["pixel_count"])
	}
	if centroid[0].(float64) != 3 || centroid[1].(float64) != 2 {
		t.Errorf("centroid_in_cell = %v, want [3 2]", first["centroid_in_cell"])
	}
	// world_center = (格 + 0.5) × 8 + 图层 position(16,8)。
	centers, _ := p["world_center"].([]any)
	if len(centers) != 2 {
		t.Fatalf("world_center = %v, want 与 cells 一一对应的 2 项", centers)
	}
	want := [][2]float64{{36, 36}, {36, 44}}
	for i, w := range want {
		c := centers[i].(map[string]any)
		if c["x"].(float64) != w[0] || c["y"].(float64) != w[1] {
			t.Errorf("world_center[%d] = %v, want %v", i, c, w)
		}
	}
	// 图层 position 也回报，便于调用方把本地坐标换算成世界坐标。
	lp, _ := p["layer_position"].([]any)
	if len(lp) != 2 || lp[0].(float64) != 16 || lp[1].(float64) != 8 {
		t.Errorf("layer_position = %v, want [16 8]", p["layer_position"])
	}
}

// TestTilemapFindByColorNoMatchAndErrors：无命中时两个数组都是 []；
// 非法颜色 / 缺 --layer / 缺 --tolerance 都要报错。
func TestTilemapFindByColorNoMatchAndErrors(t *testing.T) {
	scenePath, _ := tilemapColorFixture(t)
	none := runTilemap(t, "tilemap", "find-by-color", "--scene", scenePath,
		"--layer", "Ground", "--color", "0,255,0", "--tolerance", "0")
	cells, okCells := none["cells"].([]any)
	centers, okCenters := none["world_center"].([]any)
	if !okCells || !okCenters || len(cells) != 0 || len(centers) != 0 {
		t.Errorf("无命中时 cells/world_center = %v / %v, want 空数组", none["cells"], none["world_center"])
	}

	if out := runTilemapErr(t, "tilemap", "find-by-color", "--scene", scenePath,
		"--layer", "Ground", "--color", "nope", "--tolerance", "1"); !strings.Contains(out, "INVALID_PARAMS") {
		t.Errorf("非法 --color want INVALID_PARAMS, got %s", out)
	}
	if out := runTilemapErr(t, "tilemap", "find-by-color", "--scene", scenePath,
		"--layer", "Ground", "--color", "255,0,0", "--tolerance", "-1"); !strings.Contains(out, "INVALID_PARAMS") {
		t.Errorf("负 --tolerance want INVALID_PARAMS, got %s", out)
	}
	if out := runTilemapErr(t, "tilemap", "find-by-color", "--scene", scenePath,
		"--color", "255,0,0", "--tolerance", "1"); !strings.Contains(out, "USAGE_ERROR") {
		t.Errorf("缺 --layer want USAGE_ERROR, got %s", out)
	}
	if out := runTilemapErr(t, "tilemap", "find-by-color", "--scene", scenePath,
		"--layer", "Ground", "--color", "255,0,0"); !strings.Contains(out, "USAGE_ERROR") {
		t.Errorf("缺 --tolerance want USAGE_ERROR, got %s", out)
	}
	if out := runTilemapErr(t, "tilemap", "find-by-color", "--scene", scenePath,
		"--layer", "Nope", "--color", "255,0,0", "--tolerance", "1"); !strings.Contains(out, "FIND_FAILED") {
		t.Errorf("不存在的图层 want FIND_FAILED, got %s", out)
	}
}

// TestTilemapRenderOriginAndCellSize：格子不在原点时 origin 给出"输出像素 →
// 格"的偏移，cell_px_size 给出格子像素尺寸。
func TestTilemapRenderOriginAndCellSize(t *testing.T) {
	scenePath, outPath := tilemapColorFixture(t)
	p := runTilemap(t, "tilemap", "render", "--scene", scenePath, "--layer", "Ground", "--out", outPath)

	// 最小格 (-1,2) × 8 → 输出左上角对应图层像素 (-8,16)。
	origin, _ := p["origin"].([]any)
	if len(origin) != 2 || origin[0].(float64) != -8 || origin[1].(float64) != 16 {
		t.Errorf("origin = %v, want [-8 16]", p["origin"])
	}
	cellPx, _ := p["cell_px_size"].([]any)
	if len(cellPx) != 2 || cellPx[0].(float64) != 8 || cellPx[1].(float64) != 8 {
		t.Errorf("cell_px_size = %v, want [8 8]", p["cell_px_size"])
	}
	// 画布 = 格范围 4 列 × 3 行 × 8 = 32x24。
	size, _ := p["size"].([]any)
	if size[0].(float64) != 32 || size[1].(float64) != 24 {
		t.Errorf("size = %v, want [32 24]", size)
	}
	got := decodeFile(t, outPath)
	if got.Bounds().Dx() != 32 || got.Bounds().Dy() != 24 {
		t.Errorf("rendered bounds = %v, want 32x24", got.Bounds())
	}
	// 格映射自检：输出像素 (0,0) 落在格 ((-8+0)/8, (16+0)/8) = (-1,2)。
	cellX := (origin[0].(float64) + 0) / cellPx[0].(float64)
	cellY := (origin[1].(float64) + 0) / cellPx[1].(float64)
	if cellX != -1 || cellY != 2 {
		t.Errorf("输出 (0,0) 落在格 (%v,%v), want (-1,2)", cellX, cellY)
	}
}

// toNumericTileMapData 把 .tscn 里第一处 tile_map_data 从 base64 形态改写成
// Godot format=3 的数值列表形态——字节完全一致，只是写法不同。复刻实机里
// `scene create` + `tilemap set-cell` + `scene save` 产出的格式（vF-4）。
func toNumericTileMapData(t *testing.T, scenePath string) {
	t.Helper()
	data, err := os.ReadFile(scenePath)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^tile_map_data = PackedByteArray\("([A-Za-z0-9+/=]*)"\)$`)
	m := re.FindStringSubmatch(string(data))
	if m == nil {
		t.Fatalf("scene has no base64 tile_map_data line:\n%s", data)
	}
	raw, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil {
		t.Fatal(err)
	}
	nums := make([]string, len(raw))
	for i, b := range raw {
		nums[i] = strconv.Itoa(int(b))
	}
	text := strings.Replace(string(data), m[0],
		"tile_map_data = PackedByteArray("+strings.Join(nums, ", ")+")", 1)
	if err := os.WriteFile(scenePath, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// sameJSON 断言两次命令回包逐字节一致（回包是 map，序列化按键排序，结果确定）。
func sameJSON(t *testing.T, what string, a, b map[string]any) {
	t.Helper()
	ab, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(ab) != string(bb) {
		t.Errorf("%s：base64 形态与数值列表形态的回包不一致\nbase64 : %s\nnumeric: %s", what, ab, bb)
	}
}

// TestTilemapCommandsNumericTileMapData：format=3 的数值列表形态 tile_map_data
// 在 dump / find-by-color / render 三条命令上必须与 base64 形态等价（vF-4：
// 旧解析器只认 base64，数值形态会静默变成空 cells）。
func TestTilemapCommandsNumericTileMapData(t *testing.T) {
	scenePath, outPath := tilemapColorFixture(t)

	baseDump := runTilemap(t, "tilemap", "dump", "--scene", scenePath)
	baseFind := runTilemap(t, "tilemap", "find-by-color", "--scene", scenePath,
		"--layer", "Ground", "--color", "255,0,0", "--tolerance", "0")
	baseRender := runTilemap(t, "tilemap", "render", "--scene", scenePath, "--layer", "Ground", "--out", outPath)
	basePNG, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}

	// 同一场景的同一批字节，改写成数值列表形态。
	toNumericTileMapData(t, scenePath)

	// dump：数值形态必须真有 3 格，且回包与 base64 形态完全一致。
	numDump := runTilemap(t, "tilemap", "dump", "--scene", scenePath)
	sameJSON(t, "tilemap dump", baseDump, numDump)
	layers, _ := numDump["layers"].([]any)
	if len(layers) != 1 {
		t.Fatalf("layers = %v", numDump["layers"])
	}
	cells, _ := layers[0].(map[string]any)["cells"].([]any)
	if len(cells) != 3 {
		t.Errorf("数值列表形态 dump cells = %d, want 3（不能是静默空）", len(cells))
	}

	// find-by-color：反查结果一致。
	sameJSON(t, "tilemap find-by-color", baseFind,
		runTilemap(t, "tilemap", "find-by-color", "--scene", scenePath,
			"--layer", "Ground", "--color", "255,0,0", "--tolerance", "0"))

	// render：回包一致，且合成出的 PNG 逐字节相同。
	sameJSON(t, "tilemap render", baseRender,
		runTilemap(t, "tilemap", "render", "--scene", scenePath, "--layer", "Ground", "--out", outPath))
	numPNG, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(basePNG, numPNG) {
		t.Errorf("两种形态渲染出的 PNG 不同（base64 %d 字节 vs 数值列表 %d 字节）", len(basePNG), len(numPNG))
	}
}

// TestTilemapCommandsRejectUnsupportedTileMapData：解析不了的 tile_map_data
// 形态必须结构化报错（SCENE_PARSE_FAILED 且点名 tile_map_data），而不是给出
// 一个看着像"这些格子真的没瓦片"的空结果。
func TestTilemapCommandsRejectUnsupportedTileMapData(t *testing.T) {
	scenePath, outPath := tilemapColorFixture(t)
	data, err := os.ReadFile(scenePath)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^tile_map_data = .*$`)
	if err := os.WriteFile(scenePath, []byte(re.ReplaceAllString(string(data), "tile_map_data = [1, 2, 3]")), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"tilemap", "dump", "--scene", scenePath},
		{"tilemap", "find-by-color", "--scene", scenePath, "--layer", "Ground", "--color", "255,0,0", "--tolerance", "0"},
		{"tilemap", "render", "--scene", scenePath, "--layer", "Ground", "--out", outPath},
	} {
		out := runTilemapErr(t, args...)
		if !strings.Contains(out, "SCENE_PARSE_FAILED") || !strings.Contains(out, "tile_map_data") {
			t.Errorf("%v：want SCENE_PARSE_FAILED 且信息里点名 tile_map_data，got %s", args, out)
		}
	}
}
