package tilemapfile

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// AtlasSource is one TileSetAtlasSource sub-resource of a TileSet .tres.
type AtlasSource struct {
	TextureRef  string // ext_resource id from the texture property
	TexturePath string // res:// path resolved through the .tres ext_resources
	RegionSize  [2]int // texture_region_size (falls back to tile_size, then 16x16)
	Margin      [2]int
	Separation  [2]int
}

// TileSet is the render-relevant content of a TileSet .tres file.
type TileSet struct {
	Path    string
	Sources map[int]*AtlasSource // source_id -> atlas source
}

// subResourceRefRe matches a SubResource("id") property value.
var subResourceRefRe = regexp.MustCompile(`^SubResource\("([^"]+)"\)`)

// vector2iRe matches a Vector2i(x, y) property value.
var vector2iRe = regexp.MustCompile(`^Vector2i\(\s*(-?\d+)\s*,\s*(-?\d+)\s*\)`)

// sourceAssignRe matches a `sources/N = SubResource(...)` line of the
// TileSet [resource] section.
var sourceAssignRe = regexp.MustCompile(`^sources/(\d+)\s*=`)

// ParseTileSet reads a TileSet .tres and extracts its atlas sources:
// texture reference, region size, margin and separation per source id.
// Supported shape: [ext_resource] textures + [sub_resource
// type="TileSetAtlasSource"] blocks + `sources/N = SubResource(...)`
// assignments in the [resource] section — anything else (e.g. scene
// collection sources) is reported as unsupported.
func ParseTileSet(path string) (*TileSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseTileSetText(path, string(data))
}

func parseTileSetText(path, text string) (*TileSet, error) {
	extResources := map[string]ExtResource{}
	subResources := map[string]*AtlasSource{} // sub_resource id -> partial source
	sourceIDs := map[int]string{}             // source_id -> sub_resource id
	tileSize := [2]int{16, 16}

	section := "" // current section kind: ext_resource | sub_resource | resource | other
	var subID string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "[ext_resource "):
			section = "ext_resource"
			attrs := attrsOf(line)
			if id := attrs["id"]; id != "" {
				extResources[id] = ExtResource{Type: attrs["type"], Path: attrs["path"]}
			}
		case strings.HasPrefix(line, "[sub_resource "):
			section = "sub_resource"
			attrs := attrsOf(line)
			subID = attrs["id"]
			if attrs["type"] == "TileSetAtlasSource" && subID != "" {
				subResources[subID] = &AtlasSource{RegionSize: [2]int{-1, -1}}
			}
		case strings.HasPrefix(line, "[resource]"):
			section = "resource"
		case strings.HasPrefix(line, "["):
			section = "other"
		case section == "sub_resource":
			src, ok := subResources[subID]
			if !ok {
				continue
			}
			switch {
			case strings.HasPrefix(line, "texture "):
				if m := extResourceRefRe.FindStringSubmatch(valueOf(line)); m != nil {
					src.TextureRef = m[1]
				}
			case strings.HasPrefix(line, "texture_region_size"):
				if v, ok := parseVector2i(valueOf(line)); ok {
					src.RegionSize = v
				}
			case strings.HasPrefix(line, "margin"):
				if v, ok := parseVector2i(valueOf(line)); ok {
					src.Margin = v
				}
			case strings.HasPrefix(line, "separation"):
				if v, ok := parseVector2i(valueOf(line)); ok {
					src.Separation = v
				}
			}
		case section == "resource":
			if strings.HasPrefix(line, "tile_size") {
				if v, ok := parseVector2i(valueOf(line)); ok {
					tileSize = v
				}
			}
			if m := sourceAssignRe.FindStringSubmatch(line); m != nil {
				id, _ := strconv.Atoi(m[1])
				if sm := subResourceRefRe.FindStringSubmatch(valueOf(line)); sm != nil {
					sourceIDs[id] = sm[1]
				} else {
					return nil, fmt.Errorf("%s: sources/%d is not a SubResource(...) reference — unsupported TileSet layout", path, id)
				}
			}
		}
	}

	ts := &TileSet{Path: path, Sources: map[int]*AtlasSource{}}
	for id, subID := range sourceIDs {
		src, ok := subResources[subID]
		if !ok {
			return nil, fmt.Errorf("%s: sources/%d references unknown sub_resource %q", path, id, subID)
		}
		if src.TextureRef == "" {
			return nil, fmt.Errorf("%s: atlas source %d has no texture — unsupported source form", path, id)
		}
		ext, ok := extResources[src.TextureRef]
		if !ok {
			return nil, fmt.Errorf("%s: atlas source %d references unknown ext_resource %q", path, id, src.TextureRef)
		}
		src.TexturePath = ext.Path
		if src.RegionSize[0] <= 0 || src.RegionSize[1] <= 0 {
			src.RegionSize = tileSize // atlas sources default to the TileSet tile_size
		}
		ts.Sources[id] = src
	}
	return ts, nil
}

// parseVector2i parses a Vector2i(x, y) value.
func parseVector2i(value string) ([2]int, bool) {
	m := vector2iRe.FindStringSubmatch(value)
	if m == nil {
		return [2]int{}, false
	}
	x, _ := strconv.Atoi(m[1])
	y, _ := strconv.Atoi(m[2])
	return [2]int{x, y}, true
}
