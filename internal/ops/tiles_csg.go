package ops

// tilemapOps: TileMapLayer cell editing and queries.
func tilemapOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "tilemap", Name: "set-cell", PluginCommand: "tilemap_set_cell",
			Summary: "Set one tile cell",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "TileMapLayer node path"),
				pi("source-id", "source_id", true, "", "TileSet source id (-1 clears the cell)"),
				pi("atlas-col", "atlas_col", true, "", "Atlas column"),
				pi("atlas-row", "atlas_row", true, "", "Atlas row"),
				pi("map-x", "map_x", true, "", "Cell x"),
				pi("map-y", "map_y", true, "", "Cell y"),
			},
		},
		{
			Domain: "tilemap", Name: "set-cells-rect", PluginCommand: "tilemap_set_cells_rect",
			Summary: "Fill a rectangle of cells with one tile",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "TileMapLayer node path"),
				pi("source-id", "source_id", true, "", "TileSet source id"),
				pi("atlas-col", "atlas_col", true, "", "Atlas column"),
				pi("atlas-row", "atlas_row", true, "", "Atlas row"),
				pi("rect-x", "rect_x", true, "", "Rectangle origin x"),
				pi("rect-y", "rect_y", true, "", "Rectangle origin y"),
				pi("rect-w", "rect_w", true, "", "Rectangle width in cells"),
				pi("rect-h", "rect_h", true, "", "Rectangle height in cells"),
			},
		},
		{
			Domain: "tilemap", Name: "clear", PluginCommand: "tilemap_clear",
			Summary: "Clear all cells of a TileMapLayer",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "TileMapLayer node path"),
			},
		},
		{
			Domain: "tilemap", Name: "get-cells", PluginCommand: "tilemap_get_cells",
			Summary: "List the used cells of a TileMapLayer",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("path", "path", true, "", "TileMapLayer node path"),
			},
		},
	}
}

// tilesetOps: TileSet atlas inspection.
func tilesetOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "tileset", Name: "get-atlas-tiles", PluginCommand: "tileset_get_atlas_tiles",
			Summary: "List the tiles of a TileSet atlas source",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("tileset-path", "tileset_path", true, "", "res:// path of the TileSet"),
				pi("source-id", "source_id", true, "", "Atlas source id"),
			},
		},
		{
			Domain: "tileset", Name: "get-atlas-image", PluginCommand: "tileset_get_atlas_image",
			Summary: "Read a TileSet atlas image",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("tileset-path", "tileset_path", true, "", "res:// path of the TileSet"),
				pi("source-id", "source_id", true, "", "Atlas source id"),
				pi("max-size", "max_size", false, "0", "Pixel cap for the returned image (0 = full size)"),
			},
		},
	}
}

// gridmapOps: GridMap item editing and queries.
func gridmapOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "gridmap", Name: "set-item", PluginCommand: "gridmap_set_item",
			Summary: "Set one GridMap cell item",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "GridMap node path"),
				pi("item", "item", true, "", "MeshLibrary item id (-1 clears the cell)"),
				pi("map-x", "map_x", true, "", "Cell x"),
				pi("map-y", "map_y", true, "", "Cell y"),
				pi("map-z", "map_z", true, "", "Cell z"),
				pi("orientation", "orientation", false, "0", "Cell orientation code"),
			},
		},
		{
			Domain: "gridmap", Name: "fill", PluginCommand: "gridmap_fill",
			Summary: "Fill a box of GridMap cells with one item",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "GridMap node path"),
				pi("item", "item", true, "", "MeshLibrary item id"),
				pi("rect-x", "rect_x", true, "", "Box origin x"),
				pi("rect-y", "rect_y", true, "", "Box origin y"),
				pi("rect-z", "rect_z", true, "", "Box origin z"),
				pi("rect-w", "rect_w", true, "", "Box width in cells"),
				pi("rect-h", "rect_h", true, "", "Box height in cells"),
				pi("rect-d", "rect_d", true, "", "Box depth in cells"),
				pi("orientation", "orientation", false, "0", "Cell orientation code"),
			},
		},
		{
			Domain: "gridmap", Name: "clear", PluginCommand: "gridmap_clear",
			Summary: "Clear all cells of a GridMap",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "GridMap node path"),
			},
		},
		{
			Domain: "gridmap", Name: "get-used-cells", PluginCommand: "gridmap_get_used_cells",
			Summary: "List the used cells of a GridMap",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("path", "path", true, "", "GridMap node path"),
			},
		},
		{
			Domain: "gridmap", Name: "list-library-items", PluginCommand: "gridmap_list_library_items",
			Summary: "List the MeshLibrary items available to a GridMap",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("path", "path", true, "", "GridMap node path"),
			},
		},
	}
}

// csgOps: CSG shape creation and boolean operations.
func csgOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "csg", Name: "create", PluginCommand: "csg_create",
			Summary: "Create a CSG shape node",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("parent-path", "parent_path", true, "", "Parent node path"),
				ps("name", "name", false, "", "Node name (default: shape-derived)"),
				ps("shape", "shape", false, "box", "box | sphere | cylinder | torus | polygon | combiner"),
				ps("operation", "operation", false, "union", "union | intersection | subtraction"),
			},
		},
		{
			Domain: "csg", Name: "set-operation", PluginCommand: "csg_set_operation",
			Summary: "Change a CSG node's boolean operation",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "CSG node path"),
				ps("operation", "operation", true, "", "union | intersection | subtraction"),
			},
		},
	}
}
