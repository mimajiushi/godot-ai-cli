package ops

// audioOps: audio players, streams, and playback control.
func audioOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "audio", Name: "player-create", PluginCommand: "audio_player_create",
			Summary: "Create an AudioStreamPlayer node",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("parent-path", "parent_path", true, "", "Parent node path"),
				ps("name", "name", false, "AudioStreamPlayer", "Node name"),
				ps("type", "type", false, "1d", "1d | 2d | 3d"),
			},
		},
		{
			Domain: "audio", Name: "player-set-stream", PluginCommand: "audio_player_set_stream",
			Summary: "Assign an audio stream to a player",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("player-path", "player_path", true, "", "Player node path"),
				ps("stream-path", "stream_path", true, "", "res:// path of the audio stream"),
			},
		},
		{
			Domain: "audio", Name: "player-set-playback", PluginCommand: "audio_player_set_playback",
			Summary: "Configure playback settings (volume, pitch, autoplay, bus)",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("player-path", "player_path", true, "", "Player node path"),
				pf("volume-db", "volume_db", false, "", "Volume in dB"),
				pf("pitch-scale", "pitch_scale", false, "", "Pitch scale factor"),
				pb("autoplay", "autoplay", false, "", "Play automatically when the scene runs"),
				ps("bus", "bus", false, "", "Audio bus name"),
			},
		},
		{
			Domain: "audio", Name: "play", PluginCommand: "audio_play",
			Summary: "Start playback on an audio player",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("player-path", "player_path", true, "", "Player node path"),
				pf("from-position", "from_position", false, "0.0", "Start position in seconds"),
			},
		},
		{
			Domain: "audio", Name: "stop", PluginCommand: "audio_stop",
			Summary: "Stop playback on an audio player",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("player-path", "player_path", true, "", "Player node path"),
			},
		},
		{
			Domain: "audio", Name: "list", PluginCommand: "audio_list",
			Summary: "List audio streams under res://",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("root", "root", false, "res://", "Subtree to search"),
				pb("include-duration", "include_duration", false, "true", "Probe each stream's duration"),
			},
		},
	}
}

// particleOps: particle nodes, process materials, draw passes, presets.
func particleOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "particle", Name: "create", PluginCommand: "particle_create",
			Summary: "Create a particles node",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("parent-path", "parent_path", true, "", "Parent node path"),
				ps("name", "name", false, "Particles", "Node name"),
				ps("type", "type", false, "gpu_3d", "gpu_3d | gpu_2d | cpu_3d | cpu_2d"),
			},
		},
		{
			Domain: "particle", Name: "set-main", PluginCommand: "particle_set_main",
			Summary: "Set main particle node properties (amount, lifetime, ...)",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("node-path", "node_path", true, "", "Particles node path"),
				pj("properties", "properties", true, "JSON object of property names to values"),
			},
		},
		{
			Domain: "particle", Name: "set-process", PluginCommand: "particle_set_process",
			Summary: "Set process material properties (gravity, velocity, ...)",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("node-path", "node_path", true, "", "Particles node path"),
				pj("properties", "properties", true, "JSON object of property names to values"),
			},
		},
		{
			Domain: "particle", Name: "set-draw-pass", PluginCommand: "particle_set_draw_pass",
			Summary: "Configure a draw pass (mesh, texture, material)",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("node-path", "node_path", true, "", "Particles node path"),
				pi("pass", "pass", false, "1", "Draw pass index (1-based)"),
				ps("mesh", "mesh", false, "", "res:// mesh path"),
				ps("texture", "texture", false, "", "res:// texture path"),
				ps("material", "material", false, "", "res:// material path"),
			},
		},
		{
			Domain: "particle", Name: "restart", PluginCommand: "particle_restart",
			Summary: "Restart particle emission",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("node-path", "node_path", true, "", "Particles node path"),
			},
		},
		{
			Domain: "particle", Name: "get", PluginCommand: "particle_get",
			Summary: "Read a particles node's configuration",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("node-path", "node_path", true, "", "Particles node path"),
			},
		},
		{
			Domain: "particle", Name: "apply-preset", PluginCommand: "particle_apply_preset",
			Summary: "Create particles from a named preset (fire, smoke, rain, ...)",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("parent-path", "parent_path", true, "", "Parent node path"),
				ps("name", "name", true, "", "Node name"),
				ps("preset", "preset", true, "", "Preset name"),
				ps("type", "type", false, "gpu_3d", "gpu_3d | gpu_2d | cpu_3d | cpu_2d"),
				pj("overrides", "overrides", false, "JSON object overriding preset properties"),
			},
		},
	}
}

// cameraOps: camera nodes, 2D follow/limits/damping, presets.
func cameraOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "camera", Name: "create", PluginCommand: "camera_create",
			Summary: "Create a camera node",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("parent-path", "parent_path", true, "", "Parent node path"),
				ps("name", "name", false, "Camera", "Node name"),
				ps("type", "type", false, "2d", "2d | 3d"),
				pb("make-current", "make_current", false, "false", "Make this the active camera"),
			},
		},
		{
			Domain: "camera", Name: "configure", PluginCommand: "camera_configure",
			Summary: "Set arbitrary camera properties",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("camera-path", "camera_path", true, "", "Camera node path"),
				pj("properties", "properties", true, "JSON object of property names to values"),
			},
		},
		{
			Domain: "camera", Name: "set-limits-2d", PluginCommand: "camera_set_limits_2d",
			Summary: "Set Camera2D scroll limits",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("camera-path", "camera_path", true, "", "Camera2D node path"),
				pi("left", "left", false, "", "Left limit in pixels"),
				pi("right", "right", false, "", "Right limit in pixels"),
				pi("top", "top", false, "", "Top limit in pixels"),
				pi("bottom", "bottom", false, "", "Bottom limit in pixels"),
				pb("smoothed", "smoothed", false, "", "Enable smoothed limits"),
			},
		},
		{
			Domain: "camera", Name: "set-damping-2d", PluginCommand: "camera_set_damping_2d",
			Summary: "Set Camera2D follow damping and drag margins",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("camera-path", "camera_path", true, "", "Camera2D node path"),
				pf("position-speed", "position_speed", false, "", "Position smoothing speed"),
				pf("rotation-speed", "rotation_speed", false, "", "Rotation smoothing speed"),
				pj("drag-margins", "drag_margins", false, `JSON object {"left":..,"right":..,"top":..,"bottom":..}`),
				pb("drag-horizontal-enabled", "drag_horizontal_enabled", false, "", "Enable horizontal drag margin"),
				pb("drag-vertical-enabled", "drag_vertical_enabled", false, "", "Enable vertical drag margin"),
			},
		},
		{
			Domain: "camera", Name: "follow-2d", PluginCommand: "camera_follow_2d",
			Summary: "Make a Camera2D follow a target node",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("camera-path", "camera_path", true, "", "Camera2D node path"),
				ps("target-path", "target_path", true, "", "Target node path"),
				pf("smoothing-speed", "smoothing_speed", false, "5.0", "Follow smoothing speed"),
				pb("zero-transform", "zero_transform", false, "true", "Zero the camera's local transform"),
			},
		},
		{
			Domain: "camera", Name: "get", PluginCommand: "camera_get",
			Summary: "Read a camera's configuration (default: the active camera)",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("camera-path", "camera_path", false, "", "Camera node path"),
			},
		},
		{
			Domain: "camera", Name: "list", PluginCommand: "camera_list",
			Summary: "List cameras in the edited scene",
			Timeout: DefaultTimeout,
		},
		{
			Domain: "camera", Name: "apply-preset", PluginCommand: "camera_apply_preset",
			Summary: "Create a camera from a named preset (side_scroll, top_down, ...)",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("parent-path", "parent_path", true, "", "Parent node path"),
				ps("name", "name", true, "", "Node name"),
				ps("preset", "preset", true, "", "Preset name"),
				ps("type", "type", false, "", "2d | 3d (default: preset-derived)"),
				pb("make-current", "make_current", false, "true", "Make this the active camera"),
				pj("overrides", "overrides", false, "JSON object overriding preset properties"),
			},
		},
	}
}
