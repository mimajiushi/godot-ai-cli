package ops

// animationOps: AnimationPlayer/Animation management, tracks, presets.
func animationOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "animation", Name: "player-create", PluginCommand: "animation_player_create",
			Summary: "Create an AnimationPlayer node",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("parent-path", "parent_path", true, "", "Parent node path"),
				ps("name", "name", false, "AnimationPlayer", "Node name"),
			},
		},
		{
			Domain: "animation", Name: "create", PluginCommand: "animation_create",
			Summary: "Create an Animation clip (auto-creates player + library if missing)",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("player-path", "player_path", true, "", "AnimationPlayer node path"),
				ps("name", "name", true, "", "Animation name"),
				pf("length", "length", true, "", "Duration in seconds"),
				ps("loop-mode", "loop_mode", false, "none", "none | loop | pingpong"),
				pb("overwrite", "overwrite", false, "false", "Replace an existing animation of the same name"),
			},
		},
		{
			Domain: "animation", Name: "add-property-track", PluginCommand: "animation_add_property_track",
			Summary: "Add a property track with keyframes to an animation",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("player-path", "player_path", true, "", "AnimationPlayer node path"),
				ps("animation-name", "animation_name", true, "", "Animation name"),
				ps("track-path", "track_path", true, "", "Node:property path the track animates"),
				pj("keyframes", "keyframes", true, `JSON array of {"time": s, "value": ...}`),
				ps("interpolation", "interpolation", false, "linear", "linear | nearest | cubic"),
			},
		},
		{
			Domain: "animation", Name: "add-method-track", PluginCommand: "animation_add_method_track",
			Summary: "Add a method-call track to an animation",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("player-path", "player_path", true, "", "AnimationPlayer node path"),
				ps("animation-name", "animation_name", true, "", "Animation name"),
				ps("target-node-path", "target_node_path", true, "", "Node the method is called on"),
				pj("keyframes", "keyframes", true, `JSON array of {"time": s, "method": "...", "args": [...]}`),
			},
		},
		{
			Domain: "animation", Name: "set-autoplay", PluginCommand: "animation_set_autoplay",
			Summary: "Set an AnimationPlayer's autoplay animation",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("player-path", "player_path", true, "", "AnimationPlayer node path"),
				ps("animation-name", "animation_name", false, "", "Animation name (empty clears autoplay)"),
			},
		},
		{
			Domain: "animation", Name: "play", PluginCommand: "animation_play",
			Summary: "Play an animation in the editor",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("player-path", "player_path", true, "", "AnimationPlayer node path"),
				ps("animation-name", "animation_name", false, "", "Animation name (empty resumes current)"),
			},
		},
		{
			Domain: "animation", Name: "stop", PluginCommand: "animation_stop",
			Summary: "Stop an AnimationPlayer",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("player-path", "player_path", true, "", "AnimationPlayer node path"),
			},
		},
		{
			Domain: "animation", Name: "list", PluginCommand: "animation_list",
			Summary: "List an AnimationPlayer's animations",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("player-path", "player_path", true, "", "AnimationPlayer node path"),
			},
		},
		{
			Domain: "animation", Name: "get", PluginCommand: "animation_get",
			Summary: "Read one animation's tracks and settings",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("player-path", "player_path", true, "", "AnimationPlayer node path"),
				ps("animation-name", "animation_name", true, "", "Animation name"),
			},
		},
		{
			Domain: "animation", Name: "create-simple", PluginCommand: "animation_create_simple",
			Summary: "Create an animation from a compact tween list",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("player-path", "player_path", true, "", "AnimationPlayer node path"),
				ps("name", "name", true, "", "Animation name"),
				pj("tweens", "tweens", true, `JSON array of tween steps`),
				pf("length", "length", false, "", "Duration in seconds (default: derived)"),
				ps("loop-mode", "loop_mode", false, "none", "none | loop | pingpong"),
				pb("overwrite", "overwrite", false, "false", "Replace an existing animation of the same name"),
			},
		},
		{
			Domain: "animation", Name: "delete", PluginCommand: "animation_delete",
			Summary: "Delete an animation",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("player-path", "player_path", true, "", "AnimationPlayer node path"),
				ps("animation-name", "animation_name", true, "", "Animation name"),
			},
		},
		{
			Domain: "animation", Name: "validate", PluginCommand: "animation_validate",
			Summary: "Validate an animation's tracks against the scene",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("player-path", "player_path", true, "", "AnimationPlayer node path"),
				ps("animation-name", "animation_name", true, "", "Animation name"),
			},
		},
		{
			Domain: "animation", Name: "preset-fade", PluginCommand: "animation_preset_fade",
			Summary: "Create a fade in/out animation preset",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("player-path", "player_path", true, "", "AnimationPlayer node path"),
				ps("target-path", "target_path", true, "", "Node to fade"),
				ps("mode", "mode", false, "in", "in | out"),
				pf("duration", "duration", false, "0.5", "Duration in seconds"),
				ps("animation-name", "animation_name", false, "", "Animation name (default: preset-derived)"),
				pb("overwrite", "overwrite", false, "false", "Replace an existing animation of the same name"),
			},
		},
		{
			Domain: "animation", Name: "preset-slide", PluginCommand: "animation_preset_slide",
			Summary: "Create a slide in/out animation preset",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("player-path", "player_path", true, "", "AnimationPlayer node path"),
				ps("target-path", "target_path", true, "", "Node to slide"),
				ps("direction", "direction", false, "left", "left | right | up | down"),
				ps("mode", "mode", false, "in", "in | out"),
				pf("distance", "distance", false, "", "Slide distance in pixels (default: preset-derived)"),
				pf("duration", "duration", false, "0.4", "Duration in seconds"),
				ps("animation-name", "animation_name", false, "", "Animation name (default: preset-derived)"),
				pb("overwrite", "overwrite", false, "false", "Replace an existing animation of the same name"),
			},
		},
		{
			Domain: "animation", Name: "preset-shake", PluginCommand: "animation_preset_shake",
			Summary: "Create a shake animation preset",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("player-path", "player_path", true, "", "AnimationPlayer node path"),
				ps("target-path", "target_path", true, "", "Node to shake"),
				pf("intensity", "intensity", false, "", "Shake amplitude (default: preset-derived)"),
				pf("duration", "duration", false, "0.3", "Duration in seconds"),
				pf("frequency", "frequency", false, "30.0", "Shakes per second"),
				pi("seed", "seed", false, "0", "Random seed (0 = random)"),
				ps("animation-name", "animation_name", false, "", "Animation name (default: preset-derived)"),
				pb("overwrite", "overwrite", false, "false", "Replace an existing animation of the same name"),
			},
		},
		{
			Domain: "animation", Name: "preset-pulse", PluginCommand: "animation_preset_pulse",
			Summary: "Create a scale-pulse animation preset",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("player-path", "player_path", true, "", "AnimationPlayer node path"),
				ps("target-path", "target_path", true, "", "Node to pulse"),
				pf("from-scale", "from_scale", false, "1.0", "Starting scale factor"),
				pf("to-scale", "to_scale", false, "1.1", "Peak scale factor"),
				pf("duration", "duration", false, "0.4", "Duration in seconds"),
				ps("animation-name", "animation_name", false, "", "Animation name (default: preset-derived)"),
				pb("overwrite", "overwrite", false, "false", "Replace an existing animation of the same name"),
			},
		},
		{
			Domain: "animation", Name: "tree-create", PluginCommand: "animation_tree_create",
			Summary: "Create an inactive AnimationTree linked to an AnimationPlayer",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("parent-path", "parent_path", true, "", "Parent node path for the new AnimationTree"),
				ps("player-path", "player_path", true, "", "AnimationPlayer node path to link"),
				ps("name", "name", false, "AnimationTree", "New AnimationTree node name"),
				ps("root-type", "root_type", false, "state_machine", "state_machine | blend_tree | blend_space_1d | blend_space_2d"),
			},
		},
		{
			Domain: "animation", Name: "tree-get", PluginCommand: "animation_tree_get",
			Summary: "Read an AnimationTree graph, its resources, and parameters",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("tree-path", "tree_path", true, "", "AnimationTree node path"),
				ps("graph-path", "graph_path", false, "", "Nested graph resource path (empty selects tree_root)"),
			},
		},
		{
			Domain: "animation", Name: "tree-node-add", PluginCommand: "animation_tree_node_add",
			Summary: "Add a native node to an AnimationTree graph",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("tree-path", "tree_path", true, "", "AnimationTree node path"),
				ps("name", "name", true, "", "Unique graph node name"),
				ps("node-type", "node_type", false, "AnimationNodeAnimation", "Concrete native AnimationNode class"),
				pj("properties", "properties", false, "JSON object of editable node properties"),
				pj("position", "position", false, `JSON {"x":..,"y":..} graph position`),
				ps("graph-path", "graph_path", false, "", "Nested graph resource path"),
			},
		},
		{
			Domain: "animation", Name: "tree-node-remove", PluginCommand: "animation_tree_node_remove",
			Summary: "Remove an AnimationTree graph node and its edges",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("tree-path", "tree_path", true, "", "AnimationTree node path"),
				ps("name", "name", true, "", "Graph node name"),
				ps("graph-path", "graph_path", false, "", "Nested graph resource path"),
			},
		},
		{
			Domain: "animation", Name: "tree-node-set", PluginCommand: "animation_tree_node_set",
			Summary: "Set editable properties on the selected AnimationTree graph node",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("tree-path", "tree_path", true, "", "AnimationTree node path"),
				pj("properties", "properties", true, "JSON object of editable node properties"),
				ps("graph-path", "graph_path", false, "", "Nested graph resource path"),
			},
		},
		{
			Domain: "animation", Name: "tree-connect", PluginCommand: "animation_tree_connect",
			Summary: "Connect a blend-tree input port",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("tree-path", "tree_path", true, "", "AnimationTree node path"),
				ps("input-node", "input_node", true, "", "Input node name"),
				pi("input-index", "input_index", true, "", "Zero-based input port index"),
				ps("output-node", "output_node", true, "", "Upstream output node name"),
				ps("graph-path", "graph_path", false, "", "Nested blend-tree path"),
			},
		},
		{
			Domain: "animation", Name: "tree-disconnect", PluginCommand: "animation_tree_disconnect",
			Summary: "Disconnect a blend-tree input port",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("tree-path", "tree_path", true, "", "AnimationTree node path"),
				ps("input-node", "input_node", true, "", "Input node name"),
				pi("input-index", "input_index", true, "", "Zero-based input port index"),
				ps("graph-path", "graph_path", false, "", "Nested blend-tree path"),
			},
		},
		{
			Domain: "animation", Name: "tree-point-add", PluginCommand: "animation_tree_point_add",
			Summary: "Add a named point to a 1D or 2D blend space",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("tree-path", "tree_path", true, "", "AnimationTree node path"),
				ps("name", "name", true, "", "Unique blend point name"),
				pj("position", "position", true, "JSON number for 1D or {\"x\":..,\"y\":..} object for 2D"),
				ps("node-type", "node_type", false, "AnimationNodeAnimation", "Concrete native AnimationRootNode class"),
				pj("properties", "properties", false, "JSON object of editable point-node properties"),
				pi("at-index", "at_index", false, "-1", "Insertion index (-1 appends)"),
				ps("graph-path", "graph_path", false, "", "Nested blend-space path"),
			},
		},
		{
			Domain: "animation", Name: "tree-point-remove", PluginCommand: "animation_tree_point_remove",
			Summary: "Remove a blend-space point",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("tree-path", "tree_path", true, "", "AnimationTree node path"),
				ps("name", "name", true, "", "Blend point name"),
				ps("graph-path", "graph_path", false, "", "Nested blend-space path"),
			},
		},
		{
			Domain: "animation", Name: "tree-point-set", PluginCommand: "animation_tree_point_set",
			Summary: "Move a blend-space point",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("tree-path", "tree_path", true, "", "AnimationTree node path"),
				ps("name", "name", true, "", "Blend point name"),
				pj("position", "position", true, "JSON number for 1D or {\"x\":..,\"y\":..} object for 2D"),
				ps("graph-path", "graph_path", false, "", "Nested blend-space path"),
			},
		},
		{
			Domain: "animation", Name: "tree-transition-add", PluginCommand: "animation_tree_transition_add",
			Summary: "Add a state-machine transition",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("tree-path", "tree_path", true, "", "AnimationTree node path"),
				ps("from-state", "from_state", true, "", "Source state name"),
				ps("to-state", "to_state", true, "", "Destination state name"),
				ps("graph-path", "graph_path", false, "", "Nested state-machine path"),
				pj("properties", "properties", false, "JSON object of transition properties"),
			},
		},
		{
			Domain: "animation", Name: "tree-transition-remove", PluginCommand: "animation_tree_transition_remove",
			Summary: "Remove a state-machine transition",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("tree-path", "tree_path", true, "", "AnimationTree node path"),
				ps("from-state", "from_state", true, "", "Source state name"),
				ps("to-state", "to_state", true, "", "Destination state name"),
				ps("graph-path", "graph_path", false, "", "Nested state-machine path"),
			},
		},
		{
			Domain: "animation", Name: "tree-transition-set", PluginCommand: "animation_tree_transition_set",
			Summary: "Set properties on a state-machine transition",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("tree-path", "tree_path", true, "", "AnimationTree node path"),
				ps("from-state", "from_state", true, "", "Source state name"),
				ps("to-state", "to_state", true, "", "Destination state name"),
				ps("graph-path", "graph_path", false, "", "Nested state-machine path"),
				pj("properties", "properties", false, "JSON object of transition properties"),
			},
		},
		{
			Domain: "animation", Name: "library-add", PluginCommand: "animation_library_add",
			Summary: "Attach a saved AnimationLibrary to an AnimationMixer",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("player-path", "player_path", true, "", "AnimationPlayer or AnimationTree node path"),
				ps("resource-path", "resource_path", true, "", "res:// path of the AnimationLibrary"),
				ps("library-name", "library_name", false, "", "Library name (empty selects the default library)"),
			},
		},
		{
			Domain: "animation", Name: "library-remove", PluginCommand: "animation_library_remove",
			Summary: "Detach an AnimationLibrary from an AnimationMixer",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("player-path", "player_path", true, "", "AnimationPlayer or AnimationTree node path"),
				ps("library-name", "library_name", false, "", "Library name (empty selects the default library)"),
			},
		},
		{
			Domain: "animation", Name: "library-list", PluginCommand: "animation_library_list",
			Summary: "List an AnimationMixer's libraries, resource paths, and clips",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("player-path", "player_path", true, "", "AnimationPlayer or AnimationTree node path"),
			},
		},
	}
}

// materialOps: material resources, params, assignment, presets.
func materialOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "material", Name: "create", PluginCommand: "material_create",
			Summary: "Create a material resource file",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "res:// path of the new material"),
				ps("type", "type", false, "standard", "standard | shader | particles | canvas_item"),
				ps("shader-path", "shader_path", false, "", "Shader res:// path for type=shader"),
				pb("overwrite", "overwrite", false, "false", "Replace an existing file"),
			},
		},
		{
			Domain: "material", Name: "set-param", PluginCommand: "material_set_param",
			Summary: "Set a standard material property",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "res:// path of the material"),
				ps("param", "param", true, "", "Property name, e.g. albedo_color"),
				pj("value", "value", true, "JSON value"),
			},
		},
		{
			// 需求 R-2：材质可以是场景内联子资源（没有 res:// 路径），
			// 因此除 --path 外再接受 --node-path + --slot，二选一由插件校验。
			Domain: "material", Name: "set-shader-param", PluginCommand: "material_set_shader_param",
			Summary: "Set a shader uniform on a ShaderMaterial (disk file or a node's inline material)",
			Timeout: DefaultTimeout, Write: true,
			ResponseNote: `{"path","node_path","property","slot","param","value","previous_value","undoable"}`,
			Params: []ParamSpec{
				ps("path", "path", false, "", "res:// path of the material (mutually exclusive with --node-path)"),
				ps("node-path", "node_path", false, "", "Scene path of the node whose slot material to mutate (inline materials)"),
				ps("slot", "slot", false, "override", "Slot of that node: override | surface_0 | ..."),
				ps("param", "param", true, "", "Uniform name"),
				pj("value", "value", true, "JSON value"),
			},
		},
		{
			Domain: "material", Name: "get", PluginCommand: "material_get",
			Summary:      "Read a material's properties (disk file or a node's inline material)",
			Timeout:      DefaultTimeout,
			ResponseNote: `{"class","type","properties","shader_parameters","shader_parameter_values","resource_local_to_scene","shader_path"}`,
			Params: []ParamSpec{
				ps("path", "path", false, "", "res:// path of the material (mutually exclusive with --node-path)"),
				ps("node-path", "node_path", false, "", "Scene path of the node whose slot material to read (inline materials)"),
				ps("slot", "slot", false, "override", "Slot of that node: override | surface_0 | ..."),
			},
		},
		{
			Domain: "material", Name: "list", PluginCommand: "material_list",
			Summary: "List material resources under res://",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("root", "root", false, "res://", "Subtree to search"),
				ps("type", "type", false, "", "Material type filter"),
			},
		},
		{
			Domain: "material", Name: "assign", PluginCommand: "material_assign",
			Summary: "Assign a material resource to a node's material slot",
			Timeout: DefaultTimeout, Write: true,
			ResponseNote: `{"node_path","property","slot","resource_path","from_node_path","shared","material_class","material_created","undoable"}`,
			Params: []ParamSpec{
				ps("node-path", "node_path", true, "", "Scene path of the node"),
				ps("resource-path", "resource_path", false, "", "res:// material (empty detaches)"),
				ps("from-node-path", "from_node_path", false, "", "Share the source node's same-slot material instance (no duplicate; mutually exclusive with --resource-path)"),
				ps("slot", "slot", false, "override", "override | surface:0 | ..."),
				pb("create-if-missing", "create_if_missing", false, "false", "Create a new material when no path given"),
				ps("type", "type", false, "standard", "Material type for create-if-missing"),
			},
		},
		{
			Domain: "material", Name: "apply-to-node", PluginCommand: "material_apply_to_node",
			Summary: "Create and apply a material to a node in one step",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("node-path", "node_path", true, "", "Scene path of the node"),
				ps("type", "type", false, "standard", "Material type"),
				ps("shader-path", "shader_path", false, "", "Shader res:// path; required for type=shader"),
				pj("props", "params", false, "JSON object of material properties"),
				ps("slot", "slot", false, "override", "override | surface:0 | ..."),
				ps("save-to", "save_to", false, "", "Optional res:// path to save the material"),
				pb("overwrite", "overwrite", false, "false", "Replace an existing saved file"),
			},
		},
		{
			Domain: "material", Name: "apply-preset", PluginCommand: "material_apply_preset",
			Summary: "Apply a named material preset",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("preset", "preset", true, "", "Preset name"),
				ps("path", "path", false, "", "Material res:// path to mutate"),
				ps("node-path", "node_path", false, "", "Node to apply the preset to"),
				pj("overrides", "overrides", false, "JSON object overriding preset properties"),
			},
		},
	}
}
