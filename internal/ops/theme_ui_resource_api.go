package ops

// themeOps: theme resources and styleboxes.
func themeOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "theme", Name: "create", PluginCommand: "create_theme",
			Summary: "Create a Theme resource file",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "res:// path of the new theme"),
				pb("overwrite", "overwrite", false, "false", "Replace an existing file"),
			},
		},
		{
			Domain: "theme", Name: "set-color", PluginCommand: "theme_set_color",
			Summary: "Set a color item on a theme",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("theme-path", "theme_path", true, "", "res:// path of the theme"),
				ps("class-name", "class_name", true, "", "Control class, e.g. Label"),
				ps("name", "name", true, "", "Item name, e.g. font_color"),
				pj("value", "value", true, `Color as JSON, e.g. "#ff0000" or {"r":1,"g":0,"b":0,"a":1}`),
			},
		},
		{
			Domain: "theme", Name: "set-constant", PluginCommand: "theme_set_constant",
			Summary: "Set a constant item on a theme",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("theme-path", "theme_path", true, "", "res:// path of the theme"),
				ps("class-name", "class_name", true, "", "Control class"),
				ps("name", "name", true, "", "Item name"),
				pi("value", "value", true, "", "Constant value"),
			},
		},
		{
			Domain: "theme", Name: "set-font-size", PluginCommand: "theme_set_font_size",
			Summary: "Set a font size item on a theme",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("theme-path", "theme_path", true, "", "res:// path of the theme"),
				ps("class-name", "class_name", true, "", "Control class"),
				ps("name", "name", true, "", "Item name, e.g. font_size"),
				pi("value", "value", true, "", "Font size in pixels"),
			},
		},
		{
			Domain: "theme", Name: "set-stylebox-flat", PluginCommand: "theme_set_stylebox_flat",
			Summary: "Set a StyleBoxFlat item on a theme",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("theme-path", "theme_path", true, "", "res:// path of the theme"),
				ps("class-name", "class_name", true, "", "Control class"),
				ps("name", "name", true, "", "Item name, e.g. panel"),
				pj("bg-color", "bg_color", false, "Background color (JSON)"),
				pj("border-color", "border_color", false, "Border color (JSON)"),
				pj("border", "border", false, `JSON {"left":..,"right":..,"top":..,"bottom":..} widths`),
				pj("corners", "corners", false, "JSON corner radii object"),
				pj("margins", "margins", false, "JSON content margins object"),
				pj("shadow", "shadow", false, "JSON shadow settings object"),
				pb("anti-aliasing", "anti_aliasing", false, "", "Enable anti-aliasing"),
			},
		},
		{
			Domain: "theme", Name: "apply", PluginCommand: "apply_theme",
			Summary: "Apply a theme to a Control node (empty theme-path clears)",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("node-path", "node_path", true, "", "Scene path of the Control"),
				ps("theme-path", "theme_path", false, "", "res:// path of the theme"),
			},
		},
	}
}

// uiOps: Control layout, text, anchors, and draw recipes.
func uiOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "ui", Name: "set-anchor-preset", PluginCommand: "set_anchor_preset",
			Summary: "Apply an anchor preset to a Control",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Scene path of the Control"),
				ps("preset", "preset", true, "", "full_rect | center | top_left | ..."),
				ps("resize-mode", "resize_mode", false, "minsize", "minsize | keep_size"),
				pi("margin", "margin", false, "0", "Margin applied with the preset"),
			},
		},
		{
			Domain: "ui", Name: "set-text", PluginCommand: "set_text",
			Summary: "Set the text of a Label/Button/RichTextLabel",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Scene path of the Control"),
				ps("text", "text", true, "", "Text content"),
			},
		},
		{
			Domain: "ui", Name: "build-layout", PluginCommand: "build_layout",
			Summary: "Build a Control subtree from a declarative layout tree",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				pj("tree", "tree", true, "JSON layout tree object"),
				ps("parent-path", "parent_path", false, "", "Parent node path (default: scene root)"),
			},
		},
		{
			Domain: "ui", Name: "draw-recipe", PluginCommand: "control_draw_recipe",
			Summary: "Apply a custom-draw recipe to a Control",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Scene path of the Control"),
				pj("ops", "ops", true, "JSON array of draw operations"),
				pb("clear-existing", "clear_existing", false, "true", "Replace existing draw operations"),
			},
		},
	}
}

// resourceOps: generic resource search/load/create plus the typed creators
// (upstream folds curve/environment/physics_shape/texture creators into
// resource_manage).
func resourceOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "resource", Name: "search", PluginCommand: "search_resources",
			Summary: "Search resources by type and path prefix",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("type", "type", false, "", "Resource type filter"),
				ps("path", "path", false, "", "res:// path prefix filter"),
				pi("offset", "offset", false, "0", "Skip this many matches"),
				pi("limit", "limit", false, "100", "Maximum matches to return"),
			},
		},
		{
			Domain: "resource", Name: "load", PluginCommand: "load_resource",
			Summary: "Load a resource and read its properties",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("path", "path", true, "", "res:// path"),
			},
		},
		{
			Domain: "resource", Name: "assign", PluginCommand: "assign_resource",
			Summary: "Assign a resource to a node's property",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Scene path of the node"),
				ps("property", "property", true, "", "Property name"),
				ps("resource-path", "resource_path", true, "", "res:// path of the resource"),
			},
		},
		{
			Domain: "resource", Name: "get-info", PluginCommand: "get_resource_info",
			Summary: "Inspect a resource class (properties and defaults)",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("type", "type", true, "", "Resource class name"),
			},
		},
		{
			Domain: "resource", Name: "create", PluginCommand: "create_resource",
			Summary: "Create a resource, optionally saved and/or assigned",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("type", "type", true, "", "Resource class name"),
				pj("properties", "properties", false, "JSON object of property names to values"),
				ps("path", "path", false, "", "Node path when assigning"),
				ps("property", "property", false, "", "Property when assigning"),
				ps("resource-path", "resource_path", false, "", "res:// path to save to"),
				pb("overwrite", "overwrite", false, "false", "Replace an existing file"),
			},
		},
		{
			Domain: "resource", Name: "curve-set-points", PluginCommand: "curve_set_points",
			Summary: "Set the points of a Curve resource",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				pj("points", "points", true, `JSON array of {"x":..,"y":..} points`),
				ps("path", "path", false, "", "Node path when targeting an assigned curve"),
				ps("property", "property", false, "", "Property when targeting an assigned curve"),
				ps("resource-path", "resource_path", false, "", "res:// path of a Curve resource"),
			},
		},
		{
			Domain: "resource", Name: "environment-create", PluginCommand: "environment_create",
			Summary: "Create an Environment resource from a preset",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", false, "", "Node path when assigning"),
				ps("preset", "preset", false, "default", "Environment preset name"),
				pj("properties", "properties", false, "JSON object overriding preset properties"),
				pj("sky", "sky", false, "Sky settings (JSON)"),
				ps("resource-path", "resource_path", false, "", "res:// path to save to"),
				pb("overwrite", "overwrite", false, "false", "Replace an existing file"),
			},
		},
		{
			Domain: "resource", Name: "physics-shape-autofit", PluginCommand: "physics_shape_autofit",
			Summary: "Auto-fit a collision shape to a node's geometry",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Scene path of the CollisionShape node"),
				ps("source-path", "source_path", false, "", "Geometry source node path (default: sibling mesh)"),
				ps("shape-type", "shape_type", false, "", "Force a shape type (default: auto)"),
			},
		},
		{
			Domain: "resource", Name: "gradient-texture-create", PluginCommand: "gradient_texture_create",
			Summary: "Create a GradientTexture2D resource",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				pj("stops", "stops", true, `JSON array of {"offset":..,"color":..} stops`),
				pi("width", "width", false, "256", "Texture width"),
				pi("height", "height", false, "1", "Texture height"),
				ps("fill", "fill", false, "linear", "linear | radial"),
				ps("path", "path", false, "", "Node path when assigning"),
				ps("property", "property", false, "", "Property when assigning"),
				ps("resource-path", "resource_path", false, "", "res:// path to save to"),
				pb("overwrite", "overwrite", false, "false", "Replace an existing file"),
			},
		},
		{
			Domain: "resource", Name: "noise-texture-create", PluginCommand: "noise_texture_create",
			Summary: "Create a NoiseTexture2D resource",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("noise-type", "noise_type", false, "simplex_smooth", "simplex | simplex_smooth | cellular | perlin | value_cubic"),
				pi("width", "width", false, "512", "Texture width"),
				pi("height", "height", false, "512", "Texture height"),
				pf("frequency", "frequency", false, "0.01", "Noise frequency"),
				pi("seed", "seed", false, "0", "Random seed (0 = random)"),
				pi("fractal-octaves", "fractal_octaves", false, "0", "Fractal octaves (0 = default)"),
				ps("path", "path", false, "", "Node path when assigning"),
				ps("property", "property", false, "", "Property when assigning"),
				ps("resource-path", "resource_path", false, "", "res:// path to save to"),
				pb("overwrite", "overwrite", false, "false", "Replace an existing file"),
			},
		},
		{
			Domain: "resource", Name: "spriteframes-add-animation", PluginCommand: "spriteframes_add_animation",
			Summary: "Add a named animation to a SpriteFrames .tres",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("resource-path", "resource_path", true, "", "res:// path of the SpriteFrames .tres"),
				ps("name", "name", true, "", "Animation name"),
				pf("speed", "speed", false, "5.0", "Animation speed in FPS"),
				pb("loop", "loop", false, "true", "Whether the animation loops"),
			},
		},
		{
			Domain: "resource", Name: "spriteframes-add-frame", PluginCommand: "spriteframes_add_frame",
			Summary: "Append a frame (whole texture or an atlas region) to a SpriteFrames animation",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("resource-path", "resource_path", true, "", "res:// path of the SpriteFrames .tres"),
				ps("anim", "anim", true, "", "Target animation name"),
				ps("texture", "texture", true, "", "res:// path of the frame texture"),
				ps("region", "region", false, "", `Atlas region as "x,y,w,h" (omit for the whole texture)`),
				pi("at-index", "at_index", false, "", "Insert position (default: append)"),
			},
		},
		{
			Domain: "resource", Name: "spriteframes-from-sheet", PluginCommand: "spriteframes_from_sheet",
			Summary: "Batch-build SpriteFrames animations from a sprite sheet grid",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("resource-path", "resource_path", true, "", "res:// path of the SpriteFrames .tres (created if missing)"),
				ps("texture", "texture", true, "", "res:// path of the sprite sheet texture"),
				ps("cell", "cell", true, "", `Cell size as "WxH", e.g. 32x32`),
				ps("rows", "rows", true, "", `Row-to-animation map, e.g. "0:idle,1:walk"`),
				pf("fps", "fps", false, "8.0", "Animation speed (FPS) for every generated animation"),
				pb("loop", "loop", false, "true", "Whether the generated animations loop"),
			},
		},
	}
}

// apiOps: ClassDB metadata.
func apiOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "api", Name: "get-class", PluginCommand: "get_class_info",
			Summary: "Inspect ClassDB metadata for a Godot class (default: properties only)",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("class-name", "class_name", true, "", "Class name, e.g. Node3D"),
				pj("sections", "sections", false, `JSON array of sections (properties, methods, signals, enums, constants, inheritors) or "all"`),
				pb("include-inherited", "include_inherited", false, "false", "Include inherited members"),
				pb("include-inheritors", "include_inheritors", false, "false", "Include the inheritors section"),
				pi("offset", "offset", false, "0", "Pagination offset (one section at a time)"),
				pi("limit", "limit", false, "100", "Pagination limit (0 = all)"),
			},
		},
	}
}
