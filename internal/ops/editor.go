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
  ("data:image/png;base64,..."). The CLI-side flags --out (save to file, adds
  "saved"/"bytes") and --assert '#RRGGBB@x,y' (pixel check, --tolerance, adds
  "passed"/"samples") consume the image locally and omit image_base64 from the
  output. --full-res captures at the source resolution (no downscale cap).`,
			DocNote: "`--source game` without a running game fails CLI-side with `GAME_NOT_RUNNING` — start it via `project run` first. Pixel-art games need `--full-res` to eyeball frames: the 640 default shrinks a 32px sprite to ~21 screen pixels under a 2x camera.",
			Params: []ParamSpec{
				ps("source", "source", false, "viewport", "viewport | viewport_2d | cinematic | game"),
				pi("max-resolution", "max_resolution", false, "640", "Longest-edge pixel cap for the capture (0 = no cap; the CLI-side --full-res flag sends 0)"),
				pb("include-image", "include_image", false, "true", "Embed the image data in the response"),
				ps("view-target", "view_target", false, "", "Camera3D node path for source=cinematic"),
				pb("coverage", "coverage", false, "false", "Include scene-coverage analysis for cinematic shots"),
				pf("elevation", "elevation", false, "", "Cinematic camera elevation in degrees"),
				pf("azimuth", "azimuth", false, "", "Cinematic camera azimuth in degrees"),
				pf("fov", "fov", false, "", "Cinematic camera field of view in degrees"),
				ps("user-prompt", "user_prompt", false, "", "Prompt passed to vision routing when enabled"),
			},
			CLIFlags: []CLIFlagSpec{
				cls("out", "", "save the captured image to this file and omit image_base64 from the output"),
				clsa("assert", "expected pixel as '#RRGGBB@x,y' (repeatable); fails with PIXEL_ASSERT_FAILED on mismatch"),
				cli("tolerance", "0", "per-channel tolerance for --assert"),
				clb("full-res", "false", "capture at full source resolution (sends max_resolution=0, no downscale cap; the default cap is 640)"),
				cls("region", "", `crop the capture to "x,y,w,h" in source-image pixels (crops first, then --max-resolution applies; --assert coordinates refer to the cropped image)`),
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
			Domain: "editor", Name: "record", PluginCommand: "game_command", WrapOp: "record_frames",
			Summary: "Capture a frame-aligned burst of the running game (one readback per game frame)",
			Timeout: RecordTimeout,
			ResponseNote: `{"captured","frames","frame_deltas_ms","width","height"}; "frames" holds
  base64 PNGs in order. CLI-side flags: --out-dir (save frame_0001.png… locally,
  frames omitted from stdout, adds "files"), --format gif --out <file.gif> (adds
  "saved"/"bytes"), --duration/--fps (frame count = duration×fps), --full-res
  (no per-frame downscale cap).`,
			DocNote: "Captures one readback per game frame — use it to verify per-frame animation content, particles, or projectiles in one call instead of N eval→screenshot round trips. Fails fast with an actionable error when the game's main loop is stalled. Examples: `editor record --frames 60 --out-dir shots/burst1` · `editor record --duration 2 --fps 30 --format gif --out run.gif`.",
			Params: []ParamSpec{
				pi("frames", "frames", false, "", "Capture this many consecutive game frames"),
				pi("max-resolution", "max_resolution", false, "640", "Longest-edge pixel cap per frame (0 = no cap; the CLI-side --full-res flag sends 0)"),
			},
			CLIFlags: []CLIFlagSpec{
				cls("out-dir", "", "save each frame as PNG into this directory (frames omitted from stdout)"),
				cls("out", "", "with --format gif: write the animated GIF to this file"),
				cls("format", "png", "png (per-frame files) | gif (animated)"),
				clf("duration", "0", "capture this many seconds (frame count = duration x --fps)"),
				cli("fps", "0", "frame rate used with --duration"),
				clb("full-res", "false", "capture frames at full source resolution (sends max_resolution=0)"),
			},
		},
		{
			Domain: "editor", Name: "eval", PluginCommand: "game_eval",
			Summary: "Evaluate GDScript code inside the running game",
			Timeout: GameTimeout,
			ResponseNote: `{"result","source"}; result is the value of the code's explicit
  return (null for plain statements). --echo-prints adds "prints": the
  print()/printerr() lines this eval produced.`,
			DocNote: "Example: `editor eval --code 'print($Player.position)' --echo-prints` → `{\"result\":null,\"source\":\"game\",\"prints\":[\"(144, 136)\\n\"]}` — no follow-up `logs read` needed.\nEval code constraints: the code becomes the body of a generated function — keep it flat (no `if`/`for` blocks sharing one line after a colon, e.g. `for x in range(3): var a := 1; if ...` fails to parse); use real newlines and indentation. A parse error returns `EVAL_COMPILE_ERROR`; the game auto-resumes from the debugger break it caused (manual recovery: `project continue`).",
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
			DocNote: "Server-side filtering: `--level`/`--grep`/`--tail` filter before windowing; filtered responses add `matched_count` (post-filter, pre-window size) while `total_count` keeps the raw buffer size. Example: `logs read --source game --level error --tail 20`. Incremental editor-log reads: pass `--since-cursor <n>` and continue from the response's `next_cursor`.",
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
