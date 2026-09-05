package ops

// editorOps: editor state, selection, screenshots, lifecycle, and game
// eval (upstream editor_manage op="game_eval").
func editorOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "editor", Name: "state", PluginCommand: "get_editor_state",
			Summary: "Show editor version, project, current scene, readiness, and play state",
			Timeout: DefaultTimeout,
		},
		{
			Domain: "editor", Name: "selection-get", PluginCommand: "get_selection",
			Summary: "List the currently selected editor nodes",
			Timeout: DefaultTimeout,
		},
		{
			Domain: "editor", Name: "selection-set", PluginCommand: "set_selection",
			Summary: "Select nodes in the editor by scene path",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				pj("paths", "paths", true, `JSON array of node paths, e.g. ["Root/Player"]`),
			},
		},
		{
			Domain: "editor", Name: "screenshot", PluginCommand: "take_screenshot",
			Summary: "Capture the editor viewport (3D/2D), a cinematic Camera3D render, or the game framebuffer",
			Timeout: ScreenshotTimeout,
			ResponseNote: `{"format","width","height","frames_drawn","image_base64"}; image_base64 is a data URI
  ("data:image/png;base64,..."). The CLI-side flags --out (save to file) and
  --assert '#RRGGBB@x,y' (pixel check, --tolerance) consume the image locally
  and omit image_base64 from the output. --full-res captures at the source
  resolution (no downscale cap).`,
			Params: []ParamSpec{
				ps("source", "source", false, "viewport", "viewport | viewport_2d | cinematic | game"),
				pi("max-resolution", "max_resolution", false, "640", "Longest-edge pixel cap for the capture (or use --full-res for no cap)"),
				pb("include-image", "include_image", false, "true", "Embed the image data in the response"),
				ps("view-target", "view_target", false, "", "Camera3D node path for source=cinematic"),
				pb("coverage", "coverage", false, "false", "Include scene-coverage analysis for cinematic shots"),
				pf("elevation", "elevation", false, "", "Cinematic camera elevation in degrees"),
				pf("azimuth", "azimuth", false, "", "Cinematic camera azimuth in degrees"),
				pf("fov", "fov", false, "", "Cinematic camera field of view in degrees"),
				ps("user-prompt", "user_prompt", false, "", "Prompt passed to vision routing when enabled"),
			},
		},
		{
			Domain: "editor", Name: "monitors", PluginCommand: "get_performance_monitors",
			Summary: "Read Godot performance monitor values",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				pj("monitors", "monitors", false, `JSON array of monitor names (default: all)`),
			},
		},
		{
			Domain: "editor", Name: "reload-plugin", PluginCommand: "reload_plugin",
			Summary: "Reload the godot_ai plugin and wait for reconnect",
			Timeout: DefaultTimeout,
		},
		{
			Domain: "editor", Name: "quit", PluginCommand: "quit_editor",
			Summary: "Ask the connected editor to quit gracefully",
			Timeout: DefaultTimeout,
		},
		{
			Domain: "editor", Name: "eval", PluginCommand: "game_eval",
			Summary: "Evaluate GDScript code inside the running game",
			Timeout: GameTimeout,
			ResponseNote: `{"result","source"}; result is the value of the code's explicit
  return (null for plain statements). --echo-prints adds "prints": the print()/
  printerr() lines this eval produced.`,
			Params: []ParamSpec{
				ps("code", "code", true, "", "GDScript source to evaluate in the game context"),
				pb("echo-prints", "echo_prints", false, "false", `Also return the print()/printerr() lines produced during this eval as "prints"`),
			},
		},
	}
}

// logsOps: the log buffers (upstream logs_read / editor_manage logs_clear).
func logsOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "logs", Name: "read", PluginCommand: "get_logs",
			Summary: "Read plugin / game / editor / combined log buffers",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				pi("count", "count", false, "50", "Maximum number of lines to return"),
				pi("offset", "offset", false, "0", "Skip this many lines from the start"),
				ps("source", "source", false, "plugin", "plugin | game | editor | all"),
				ps("since-run-id", "since_run_id", false, "", "Read the game log of one specific run"),
				pi("since-cursor", "since_cursor", false, "", "Incremental editor-log poll cursor"),
				pb("include-details", "include_details", false, "false", "Include structured error details (editor/game/all)"),
				ps("level", "level", false, "", "Keep only lines at this level: error | warn | info ('warning' accepted as warn)"),
				ps("grep", "grep", false, "", "Keep only lines whose text contains this substring (case-sensitive)"),
				pi("tail", "tail", false, "", "Return only the last N matching lines (overrides --count/--offset)"),
			},
		},
		{
			Domain: "logs", Name: "clear", PluginCommand: "clear_logs",
			Summary: "Clear the plugin log buffers",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				pb("clear-debugger-errors", "clear_debugger_errors", false, "false", "Also clear the Debugger dock Errors-tab rows"),
			},
		},
	}
}
