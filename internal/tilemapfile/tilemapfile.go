// Package tilemapfile parses Godot 4 .tscn/.tres text resources directly —
// no editor, daemon, or scene instantiation required. It decodes the binary
// tile_map_data PackedByteArray of TileMapLayer nodes (the dump path) and
// resolves TileSet atlases to composite a full map PNG (the render path).
//
// tile_map_data layout (Godot 4.3+ TileMapLayer): a 2-byte header followed
// by 12 bytes per cell, little-endian <hhhhhh — int16 x, int16 y (signed
// cell coordinates), uint16 source_id, uint16 atlas_x, uint16 atlas_y,
// uint16 alternative.
package tilemapfile

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ExtResource is one [ext_resource] entry of a .tscn/.tres file.
type ExtResource struct {
	Type string // e.g. "TileSet", "Texture2D"
	Path string // res:// path
}

// Scene is the tile-relevant content of a .tscn file.
type Scene struct {
	Path         string
	ProjectRoot  string // directory holding the project.godot above Path
	ExtResources map[string]ExtResource
	Layers       []*Layer
}

// Layer is one TileMapLayer node with its decoded cells.
type Layer struct {
	Name       string `json:"layer"`
	TileSetRef string `json:"-"` // ext_resource id from the tile_set property ("" when unset)
	Cells      []Cell `json:"cells"`
	// Position 是节点自身的 position（Vector2）：格坐标到世界坐标的平移量。
	// 缺失时为 (0,0)——图层没被挪动过，图层本地坐标即世界坐标。
	Position [2]float64 `json:"-"`
	// HasTransform 为真表示节点带 transform（旋转/缩放）；本包只解析 position
	// 的平移，遇到 transform 时由调用方在回包里提示坐标未叠加旋转/缩放。
	HasTransform bool `json:"-"`
}

// Cell is one decoded tile_map_data record.
type Cell struct {
	X           int    `json:"x"`
	Y           int    `json:"y"`
	Source      int    `json:"source"`
	Atlas       [2]int `json:"atlas"`
	Alternative int    `json:"alternative"`
}

// attrRe extracts key="value" attributes from a section header line.
var attrRe = regexp.MustCompile(`(\w+)="([^"]*)"`)

// tileMapDataRe 匹配"这一层声明了 tile_map_data"（任何取值形态都先接住）：
// 具体形态交给 packedByteArrayBytes 分辨，不认识的形态必须报错——旧实现只认
// base64 字符串形态，数值列表形态会被静默跳过，于是 tilemap dump 安静地给出
// cells: []，调用方会误判成"这些格子真的没瓦片"（vF-4）。
var tileMapDataRe = regexp.MustCompile(`^tile_map_data\s*=`)

// packedAssignRe 匹配一条 `key = value` 属性行，用来判断折行的续行：续行里只
// 有数字/逗号/base64 字符，不会是新的赋值或新的 [section]。要求 `=` 前有空格
// （Godot 的文本格式就是这么写的），否则 base64 末尾的 `=` 填充会被误认成赋值。
var packedAssignRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\s+=`)

// extResourceRefRe matches an ExtResource("id") property value.
var extResourceRefRe = regexp.MustCompile(`^ExtResource\("([^"]+)"\)`)

// ParseScene reads a .tscn file and extracts every TileMapLayer node with
// its decoded cells. res:// references resolve against ProjectRoot — the
// nearest ancestor directory of path that holds a project.godot.
func ParseScene(path string) (*Scene, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	root, err := FindProjectRoot(path)
	if err != nil {
		return nil, err
	}
	return parseSceneText(path, root, string(data))
}

// FindProjectRoot walks up from a scene path to the directory containing
// project.godot — the anchor for res:// paths.
func FindProjectRoot(scenePath string) (string, error) {
	abs, err := filepath.Abs(scenePath)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(abs)
	for {
		if _, err := os.Stat(filepath.Join(dir, "project.godot")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no project.godot found above %s — res:// paths cannot be resolved", scenePath)
		}
		dir = parent
	}
}

// ResolveRes turns a res:// path into a disk path under the project root.
func ResolveRes(projectRoot, resPath string) (string, error) {
	if !strings.HasPrefix(resPath, "res://") {
		return "", fmt.Errorf("want a res:// path, got %q", resPath)
	}
	return filepath.Join(projectRoot, filepath.FromSlash(strings.TrimPrefix(resPath, "res://"))), nil
}

// parseSceneText is the text-scanning half of ParseScene (kept separate so
// tests can feed fixtures without touching the filesystem layout).
func parseSceneText(path, projectRoot, text string) (*Scene, error) {
	sc := &Scene{
		Path:         path,
		ProjectRoot:  projectRoot,
		ExtResources: map[string]ExtResource{},
	}
	var cur *Layer // non-nil while inside a TileMapLayer node section
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t\r")
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "[ext_resource "):
			cur = nil
			attrs := attrsOf(trim)
			if id := attrs["id"]; id != "" {
				sc.ExtResources[id] = ExtResource{Type: attrs["type"], Path: attrs["path"]}
			}
		case strings.HasPrefix(trim, "[node "):
			attrs := attrsOf(trim)
			if attrs["type"] == "TileMapLayer" {
				cur = &Layer{Name: attrs["name"]}
				sc.Layers = append(sc.Layers, cur)
			} else {
				cur = nil
			}
		case strings.HasPrefix(trim, "["):
			cur = nil // any other section ends the node body
		case cur != nil && tileMapDataRe.MatchString(trim):
			cells, err := parseTileMapDataProperty(lines, &i, trim)
			if err != nil {
				return nil, fmt.Errorf("%s: layer %q: %v", path, cur.Name, err)
			}
			cur.Cells = cells
		case cur != nil && strings.HasPrefix(trim, "tile_set"):
			if m := extResourceRefRe.FindStringSubmatch(valueOf(trim)); m != nil {
				cur.TileSetRef = m[1]
			}
		case cur != nil && strings.HasPrefix(trim, "position"):
			// Node2D 的 position：只在非默认值时被序列化，缺省即 (0,0)。
			if v, ok := parseVector2(valueOf(trim)); ok {
				cur.Position = v
			}
		case cur != nil && strings.HasPrefix(trim, "transform"):
			// transform 带旋转/缩放（TileMapLayer 极少用）；只标记不解析，
			// 让调用方有机会说明"本地坐标 ≠ 世界坐标"。
			cur.HasTransform = true
		}
	}
	return sc, nil
}

// attrsOf parses the key="value" attributes of a section header.
func attrsOf(header string) map[string]string {
	out := map[string]string{}
	for _, m := range attrRe.FindAllStringSubmatch(header, -1) {
		out[m[1]] = m[2]
	}
	return out
}

// valueOf returns the right-hand side of a `key = value` property line.
func valueOf(line string) string {
	i := strings.Index(line, "=")
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(line[i+1:])
}

// parseTileMapDataProperty 解析一条 tile_map_data 属性（导出器折行时可能跨行），
// 返回解码后的格子。支持两种序列化形态，两者殊途同归到同一份字节：
//
//	PackedByteArray("BASE64")         Godot 4.3+（format=4）的字符串形态
//	PackedByteArray(0, 0, 1, 0, ...)  Godot 4.0/4.1（format=3）的数值列表形态
//
// 其余形态一律返回错误：静默给出空 cells 比报错危险得多（调用方会把"没解析到"
// 当成"这格真的没瓦片"）。
func parseTileMapDataProperty(lines []string, i *int, first string) ([]Cell, error) {
	payload := first
	for !packedPayloadClosed(payload) {
		if *i+1 >= len(lines) || !packedContinuation(lines[*i+1]) {
			return nil, fmt.Errorf("unterminated tile_map_data PackedByteArray (no closing parenthesis): %q", clip(first, 80))
		}
		*i++
		payload += strings.TrimSpace(lines[*i])
	}
	raw, err := packedByteArrayBytes(valueOf(payload))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		// PackedByteArray() / PackedByteArray("")：显式声明的空数据（合法空层）。
		return []Cell{}, nil
	}
	return DecodeTileMapBytes(raw)
}

// packedPayloadClosed 报告是否已经读到 PackedByteArray(...) 的闭合括号。
func packedPayloadClosed(payload string) bool {
	return strings.HasSuffix(strings.TrimSpace(payload), ")")
}

// packedContinuation 报告下一行是否像 PackedByteArray 的折行续写：续行只有
// 数字/逗号/base64 字符与结尾括号，不会以 [ 开头，也不是一条新的 key = value
// 属性。用"看起来不是新属性/新 section"来收行，避免把下一层的属性吞进来。
func packedContinuation(line string) bool {
	t := strings.TrimSpace(line)
	return t != "" && !strings.HasPrefix(t, "[") && !packedAssignRe.MatchString(t)
}

// packedByteArrayBytes 把 PackedByteArray(...) 的实参文本解析成原始字节：
// 引号形态是 base64，数值列表形态是逗号分隔的 0..255 十进制。
func packedByteArrayBytes(value string) ([]byte, error) {
	v := strings.TrimSpace(value)
	if !strings.HasPrefix(v, "PackedByteArray") {
		return nil, fmt.Errorf("unsupported tile_map_data form %q — expected PackedByteArray(<bytes>): a base64 string (Godot format=4) or a numeric byte list (format=3)", clip(v, 80))
	}
	open := strings.Index(v, "(")
	closeIdx := strings.LastIndex(v, ")")
	if open < 0 || closeIdx < open {
		return nil, fmt.Errorf("malformed tile_map_data: %q", clip(v, 80))
	}
	args := strings.TrimSpace(v[open+1 : closeIdx])
	if args == "" {
		return nil, nil
	}
	if strings.HasPrefix(args, `"`) {
		if !strings.HasSuffix(args, `"`) {
			return nil, fmt.Errorf("unterminated PackedByteArray string payload")
		}
		raw, err := base64.StdEncoding.DecodeString(args[1 : len(args)-1])
		if err != nil {
			return nil, fmt.Errorf("tile_map_data is not valid base64: %v", err)
		}
		return raw, nil
	}
	parts := strings.Split(args, ",")
	raw := make([]byte, 0, len(parts))
	for i, p := range parts {
		p = strings.TrimSpace(p)
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 255 {
			return nil, fmt.Errorf("tile_map_data byte %d is not an integer 0..255: %q", i, clip(p, 24))
		}
		raw = append(raw, byte(n))
	}
	return raw, nil
}

// clip 截断过长的文本，错误信息里不要把整段 tile_map_data 铺出来（按 rune
// 截断，避免切出非法 UTF-8）。
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// DecodeTileMapData 把 base64 的 tile_map_data 负载（Godot format=4 的字符串
// 形态）解码成按 x、再按 y 排序的格子。
func DecodeTileMapData(b64 string) ([]Cell, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("tile_map_data is not valid base64: %v", err)
	}
	return DecodeTileMapBytes(raw)
}

// DecodeTileMapBytes 把原始 tile_map_data 字节解成按 x、再按 y 排序的格子。
// 布局：2 字节头，随后每格 12 个小端字节
// <hhhhhh = int16 x, int16 y, uint16 source_id, uint16 atlas_x, uint16 atlas_y,
// uint16 alternative. 两种序列化形态（base64 字符串 / 数值列表）都归到这里，
// 保证解析结果完全一致。
func DecodeTileMapBytes(raw []byte) ([]Cell, error) {
	const headerSize, cellSize = 2, 12
	if len(raw) < headerSize || (len(raw)-headerSize)%cellSize != 0 {
		return nil, fmt.Errorf("tile_map_data has %d bytes — want a 2-byte header plus a multiple of %d", len(raw), cellSize)
	}
	cells := make([]Cell, 0, (len(raw)-headerSize)/cellSize)
	for off := headerSize; off+cellSize <= len(raw); off += cellSize {
		cells = append(cells, Cell{
			X:           int(int16(binary.LittleEndian.Uint16(raw[off:]))),
			Y:           int(int16(binary.LittleEndian.Uint16(raw[off+2:]))),
			Source:      int(binary.LittleEndian.Uint16(raw[off+4:])),
			Atlas:       [2]int{int(binary.LittleEndian.Uint16(raw[off+6:])), int(binary.LittleEndian.Uint16(raw[off+8:]))},
			Alternative: int(binary.LittleEndian.Uint16(raw[off+10:])),
		})
	}
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].X != cells[j].X {
			return cells[i].X < cells[j].X
		}
		return cells[i].Y < cells[j].Y
	})
	return cells, nil
}

// FindLayer returns the named layer, or the first layer when name is "".
func (sc *Scene) FindLayer(name string) (*Layer, error) {
	if len(sc.Layers) == 0 {
		return nil, fmt.Errorf("%s declares no TileMapLayer nodes", sc.Path)
	}
	if name == "" {
		return sc.Layers[0], nil
	}
	for _, l := range sc.Layers {
		if l.Name == name {
			return l, nil
		}
	}
	names := make([]string, 0, len(sc.Layers))
	for _, l := range sc.Layers {
		names = append(names, l.Name)
	}
	return nil, fmt.Errorf("no TileMapLayer named %q in %s (layers: %s)", name, sc.Path, strings.Join(names, ", "))
}
