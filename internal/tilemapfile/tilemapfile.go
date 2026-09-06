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

// packedByteArrayRe matches the start of a tile_map_data property line.
var packedByteArrayRe = regexp.MustCompile(`^tile_map_data\s*=\s*PackedByteArray\("`)

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
		case cur != nil && packedByteArrayRe.MatchString(trim):
			// The base64 payload is normally one line; keep consuming lines
			// until the closing ") in case an exporter wrapped it.
			payload := trim
			for !strings.HasSuffix(strings.TrimSpace(payload), `")`) && i+1 < len(lines) {
				i++
				payload += strings.TrimSpace(lines[i])
			}
			b64, err := packedPayload(payload)
			if err != nil {
				return nil, fmt.Errorf("%s: layer %q: %v", path, cur.Name, err)
			}
			cells, err := DecodeTileMapData(b64)
			if err != nil {
				return nil, fmt.Errorf("%s: layer %q: %v", path, cur.Name, err)
			}
			cur.Cells = cells
		case cur != nil && strings.HasPrefix(trim, "tile_set"):
			if m := extResourceRefRe.FindStringSubmatch(valueOf(trim)); m != nil {
				cur.TileSetRef = m[1]
			}
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

// packedPayload extracts the base64 string out of a PackedByteArray("...")
// property line (possibly multi-line-concatenated by the caller).
func packedPayload(line string) (string, error) {
	start := strings.Index(line, `PackedByteArray("`)
	if start < 0 {
		return "", fmt.Errorf("malformed tile_map_data line: %q", line)
	}
	rest := line[start+len(`PackedByteArray("`):]
	end := strings.LastIndex(rest, `")`)
	if end < 0 {
		return "", fmt.Errorf("unterminated PackedByteArray payload")
	}
	return rest[:end], nil
}

// DecodeTileMapData decodes the base64 tile_map_data payload into cells
// sorted by x, then y. Layout: 2-byte header, then per cell 12 little-endian
// bytes <hhhhhh = int16 x, int16 y, uint16 source_id, uint16 atlas_x,
// uint16 atlas_y, uint16 alternative.
func DecodeTileMapData(b64 string) ([]Cell, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("tile_map_data is not valid base64: %v", err)
	}
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
