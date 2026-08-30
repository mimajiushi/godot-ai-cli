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
			Domain: "material", Name: "set-shader-param", PluginCommand: "material_set_shader_param",
			Summary: "Set a shader uniform on a ShaderMaterial",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "res:// path of the material"),
				ps("param", "param", true, "", "Uniform name"),
				pj("value", "value", true, "JSON value"),
			},
		},
		{
			Domain: "material", Name: "get", PluginCommand: "material_get",
			Summary: "Read a material's properties",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("path", "path", true, "", "res:// path of the material"),
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
			Params: []ParamSpec{
				ps("node-path", "node_path", true, "", "Scene path of the node"),
				ps("resource-path", "resource_path", false, "", "res:// material (empty detaches)"),
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
