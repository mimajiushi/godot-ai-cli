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
			ResponseNote: `{"format","width","height","frames_drawn","canvas_size","canvas_scale","note","image_base64"};
  image_base64 is a data URI ("data:image/png;base64,..."). canvas_scale is the
  canvas → captured-image factor (image pixel = canvas coordinate × canvas_scale;
  [1,1] for editor viewport sources, the window stretch for --source game), so
  --region/--assert stay in image pixels unless --coords canvas converts them.
  The CLI-side flags --out (save to file, adds "saved"/"bytes"), --assert
  '#RRGGBB@x,y' (pixel check, --tolerance, adds "passed"/"samples") and
  --baseline (local diff against a known frame, adds "diff_ratio"; fails with
  BASELINE_DIFF_FAILED) consume the image locally and omit image_base64 from the
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
				cls("coords", "image", "coordinate space of --region/--assert: image (source-image pixels, default; --assert then refers to the cropped image) | canvas (absolute game canvas coordinates - multiplied by the response's canvas_scale). --coords canvas forces the whole frame (sends max_resolution=0, so --max-resolution is ignored) and keeps the crop at source resolution so image_region/scale map exactly onto the returned pixels; it echoes coord_space/canvas_region/image_region/scale"),
				cls("baseline", "", "compare the final image (after --region/--max-resolution) with this baseline PNG; a size mismatch or diff_ratio > --diff-threshold fails with BASELINE_DIFF_FAILED carrying diff_ratio and sample points"),
				clf("diff-threshold", "0", "maximum tolerated diff_ratio (0..1) for --baseline; 0 = any differing pixel fails"),
				cls("diff-out", "", "with --baseline: write a diff-annotated PNG (differing pixels marked #FF00FF) to this file, even when the comparison fails"),
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
  print()/printerr() lines this eval produced. Errors are
  {"status":"error","error":{code,message,data}}; a compile failure
  (EVAL_COMPILE_ERROR) adds data.code_echo (the exact code the plugin
  compiled), data.parse_errors (the engine's Parse Error lines), data.hint
  and data.game_status.`,
			// -h 专属注意事项（catalog 不收，避免与 DocNote 重复）：Windows
			// PowerShell 5.1 会把内嵌双引号从原生程序参数里吞掉，落库代码与
			// 写法不一致，却只报一个语法错误码——这是本项目实测踩过的坑。
			HelpNote: "Exactly ONE code source may be given: --code, --code-file, --code-stdin\n" +
				"  or --code-b64 (two at once is EVAL_CODE_SOURCE_CONFLICT, never a silent\n" +
				"  priority rule).\n" +
				"  Windows: PowerShell 5.1 strips embedded double quotes from arguments passed\n" +
				"  to native programs, so `--code 'return \"abc\".length()'` reaches the plugin as\n" +
				"  `return abc.length()` and fails to parse. Escape them (\\\") or skip the shell\n" +
				"  entirely with --code-file / --code-stdin. The failing reply echoes the code it\n" +
				"  actually compiled as error.data.code_echo, which shows the stripped form.",
			Params: []ParamSpec{
				ps("code", "code", true, "", "GDScript source to evaluate in the game context"),
				pb("echo-prints", "echo_prints", false, "false", `Also return the print()/printerr() lines produced during this eval as "prints"`),
				// fork 补丁（editor_handler.gd）：编辑器进程编译检查，不碰游戏
				pb("syntax-only", "syntax_only", false, "false", "Compile-check only in the editor process — no execution, no debugger break, no running game required (ok:true or EVAL_COMPILE_ERROR with parse_errors)"),
			},
			// CLI-side code channels: the code travels as the one `code` wire
			// param, so the plugin-facing contract is unchanged.
			CLIFlags: []CLIFlagSpec{
				cls("code-file", "", "read the GDScript source from this UTF-8 file (a leading BOM is tolerated) instead of --code"),
				clb("code-stdin", "false", "read the GDScript source from stdin instead of --code (a terminal stdin is an error, never a hang)"),
				cls("code-b64", "", "base64-encoded GDScript source: fallback for shells/pipelines that cannot carry quotes"),
			},
			DocNote: "Example: `editor eval --code 'print($Player.position)' --echo-prints` → `{\"result\":null,\"source\":\"game\",\"prints\":[\"(144, 136)\\n\"]}` — no follow-up `logs read` needed.\nEval code constraints: the code becomes the body of a generated function. GDScript's inline suite after a colon extends to the END OF THE LINE: `for i in range(5): k += 1; return k` COMPILES but the `return` runs INSIDE the loop (result 1, not 5) — single-line --code with a loop/branch silently misbehaves instead of erroring (verified Godot 4.7.2). Keep compound bodies on their own lines: write multi-line snippets to a file and pass --code-file (or --code-stdin / --code-b64). A genuine parse error returns `EVAL_COMPILE_ERROR`; the game auto-resumes from the debugger break it caused (manual recovery: `project continue`).\nQuoting: prefer `--code-file` (or `--code-stdin`) whenever the snippet contains double quotes — Windows PowerShell 5.1 strips them from native-program arguments, and the failure surfaces only as a parse error on a snippet you never wrote. base64 (`--code-b64`) is the fallback for pipelines that cannot carry a file.",
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
