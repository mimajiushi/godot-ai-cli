package ops

// navigationOps / shaderOps / visualShaderOps：上游 v4.2.x 新增的工具面
// （navigation_manage 的 bake/path_get、compile-validated 的 shader 编写、
// VisualShader 图编辑）。插件侧命令在 v4.2.3 的 navigation_handler.gd /
// shader_handler.gd / visual_shader_handler.gd 实现；这里是一等的 CLI 映射。

// navigationOps: 2D/3D 导航网格烘焙与显式地图路径查询。
func navigationOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "navigation", Name: "bake", PluginCommand: "navigation_bake",
			Summary: "Bake a NavigationRegion2D/3D mesh (threaded, scene-anchored swap, undoable)",
			Timeout: ScanTimeout, Write: true,
			ResponseNote: `deferred reply (threaded engine bake, 30s budget); batch_execute cannot await it.`,
			Params: []ParamSpec{
				// 上游 bake() 经 _resolve_region 读 params["path"]（GDScript 套件钉死），
				// 不是 path_get 的 region_path——wire key 必须是 "path"（回归抓获 F2）。
				ps("region-path", "path", true, "", "Scene path of the NavigationRegion2D/3D node"),
				pb("force-sync", "force_sync", false, "true", "Wait for the engine bake to finish before replying"),
			},
		},
		{
			Domain: "navigation", Name: "path-get", PluginCommand: "navigation_path_get",
			Summary: "Query a path on an explicitly selected navigation map (2D or 3D)",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("dimension", "dimension", true, "", "2d | 3d"),
				pj("from-point", "from_point", true, `Start point ({"x":..,"y":..} or {"x":..,"y":..,"z":..})`),
				pj("to-point", "to_point", true, `End point, same shape as from-point`),
				ps("region-path", "region_path", false, "", "Restrict the query to this region's map (default: the edited scene's map)"),
				ps("scene-file", "scene_file", false, "", "Scene file to resolve the region in (default: currently edited scene)"),
				pi("navigation-layers", "navigation_layers", false, "1", "Navigation layer mask"),
				pb("optimize", "optimize", false, "true", "Post-process the path for corners"),
				pb("force-sync", "force_sync", false, "false", "Force the map to sync before querying"),
			},
		},
	}
}

// shaderOps: compile-validated raw shader authoring。create/patch 都过引擎
// shader parser，解析/类型错误整体拒绝且不写盘。
func shaderOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "shader", Name: "create", PluginCommand: "shader_create",
			Summary: "Create a .gdshader/.gdshaderinc file (engine-parse validated before write)",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("resource-path", "resource_path", true, "", "Destination res:// path ending in .gdshader or .gdshaderinc"),
				ps("code", "code", true, "", "Shader source (required, nonempty)"),
				ps("shader-type", "shader_type", false, "spatial", "spatial | canvas_item | particles | sky | fog"),
				pb("overwrite", "overwrite", false, "false", "Replace an existing file"),
			},
		},
		{
			Domain: "shader", Name: "get", PluginCommand: "shader_get",
			Summary: "Read a shader file back (code, uniforms, render modes, includes)",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("path", "path", true, "", "res:// path of the .gdshader/.gdshaderinc file"),
			},
		},
		{
			Domain: "shader", Name: "patch", PluginCommand: "shader_patch",
			Summary: "Apply a text replacement to a shader file and re-validate before writing",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "res:// path of the .gdshader/.gdshaderinc file"),
				ps("old-text", "old_text", true, "", "Exact snippet to replace (nonempty, must match at least once)"),
				ps("new-text", "new_text", true, "", "Replacement text"),
				pb("replace-all", "replace_all", false, "false", "Replace every occurrence (multiple matches fail otherwise)"),
				ps("shader-type", "shader_type", false, "spatial", "Used to wrap .gdshaderinc validation"),
			},
		},
		{
			Domain: "shader", Name: "validate", PluginCommand: "shader_validate",
			Summary: "Parse-check shader code through the engine compiler without writing",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("code", "code", true, "", "Shader source (required, nonempty)"),
				ps("kind", "kind", false, "shader", "shader | include"),
				ps("shader-type", "shader_type", false, "spatial", "spatial | canvas_item | particles | sky | fog"),
				ps("base-dir", "base_dir", false, "", "Directory for relative #include resolution (default: user:// scratch)"),
			},
		},
	}
}

// visualShaderOps: VisualShader 图编写——建图、读图、操作批编辑、节点目录。
func visualShaderOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "visual-shader", Name: "create-graph", PluginCommand: "visual_shader_create_graph",
			Summary: "Create a VisualShader .tres with selected stages and varyings",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("resource-path", "resource_path", true, "", "Destination res:// path ending in .tres"),
				ps("shader-type", "shader_type", false, "spatial", "spatial | canvas_item | particles | sky | fog"),
				pj("stages", "stages", false, `Stage specs: [{"stage":"fragment","nodes":[...],"connections":[...]}] (nodes/connections arrays required per stage)`),
				pj("varyings", "varyings", false, `Varyings to declare: [{"name":..,"mode":..,"type":..}]`),
				pb("overwrite", "overwrite", false, "false", "Replace an existing file"),
			},
		},
		{
			Domain: "visual-shader", Name: "get", PluginCommand: "visual_shader_get",
			Summary: "Read a VisualShader graph (nodes, connections, varyings) as JSON",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("path", "path", true, "", "res:// path of the VisualShader .tres file"),
			},
		},
		{
			Domain: "visual-shader", Name: "edit", PluginCommand: "visual_shader_edit",
			Summary: "Apply a batch of graph edit operations (add/replace/remove/connect/disconnect nodes, varyings)",
			Timeout: DefaultTimeout, Write: true,
			ResponseNote: `operations[]: {"op":"add_node"|"replace_node"|"remove_node"|"connect_nodes"|"disconnect_nodes"|"add_varying"|"remove_varying", ...}; string ids may reference nodes added earlier in the same batch.`,
			Params: []ParamSpec{
				ps("resource-path", "resource_path", true, "", "res:// path of the VisualShader .tres file"),
				pj("operations", "operations", true, `JSON array of edit operations, e.g. [{"op":"add_node","stage":"fragment","type":"VisualShaderNodeColorConstant"}]`),
			},
		},
		{
			Domain: "visual-shader", Name: "node-catalog", PluginCommand: "visual_shader_node_catalog",
			Summary: "List instantiable VisualShaderNode classes (filter/paginate) with settable properties",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("filter", "filter", false, "", "Case-insensitive class-name substring filter"),
				pi("offset", "offset", false, "0", "Page offset"),
				pi("limit", "limit", false, "100", "Page size (capped by the plugin)"),
			},
		},
	}
}
