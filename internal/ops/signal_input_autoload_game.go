package ops

// signalOps: signal introspection and wiring.
func signalOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "signal", Name: "list", PluginCommand: "list_signals",
			Summary: "List a node's signals",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Scene path of the node"),
				pb("include-editor", "include_editor", false, "false", "Include editor-internal connections"),
			},
		},
		{
			Domain: "signal", Name: "connect", PluginCommand: "connect_signal",
			Summary: "Connect a signal to a target method",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Scene path of the emitting node"),
				ps("signal", "signal", true, "", "Signal name"),
				ps("target", "target", true, "", "Scene path of the target node"),
				ps("method", "method", true, "", "Method name on the target"),
			},
		},
		{
			Domain: "signal", Name: "disconnect", PluginCommand: "disconnect_signal",
			Summary: "Disconnect a signal from a target method",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Scene path of the emitting node"),
				ps("signal", "signal", true, "", "Signal name"),
				ps("target", "target", true, "", "Scene path of the target node"),
				ps("method", "method", true, "", "Method name on the target"),
			},
		},
	}
}

// inputMapOps: project input actions and event bindings.
func inputMapOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "input-map", Name: "list", PluginCommand: "list_actions",
			Summary: "List project input actions and their bindings",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				pb("include-builtin", "include_builtin", false, "false", "Include Godot's built-in ui_* actions"),
				ps("action", "action", false, "", "Only list actions whose name matches this glob, e.g. move_*"),
			},
		},
		{
			Domain: "input-map", Name: "add-action", PluginCommand: "add_action",
			Summary: "Add an input action (fails if it exists)",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("action", "action", true, "", "Action name"),
				pf("deadzone", "deadzone", false, "0.5", "Analog deadzone"),
			},
		},
		{
			Domain: "input-map", Name: "ensure-action", PluginCommand: "ensure_action",
			Summary: "Add an input action if missing (idempotent)",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("action", "action", true, "", "Action name"),
				pf("deadzone", "deadzone", false, "0.5", "Analog deadzone"),
			},
		},
		{
			Domain: "input-map", Name: "remove-action", PluginCommand: "remove_action",
			Summary: "Remove an input action and its bindings",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("action", "action", true, "", "Action name"),
			},
		},
		{
			Domain: "input-map", Name: "bind-event", PluginCommand: "bind_event",
			Summary: "Bind an input event to an action (fails on duplicates)",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("action", "action", true, "", "Action name"),
				ps("event-type", "event_type", true, "", "key | mouse_button | joy_button | joy_axis"),
				ps("keycode", "keycode", false, "", "Godot keycode for event-type=key, e.g. A, Space, F1"),
				pi("button", "button", false, "", "Button index for mouse_button (1=left) / joy_button"),
				pi("axis", "axis", false, "", "JoyAxis index for joy_axis, e.g. 0 = left stick X"),
				pf("axis-value", "axis_value", false, "", "Axis value for joy_axis (default 1.0 plugin-side)"),
				pb("ctrl", "ctrl", false, "", "Require Ctrl for event-type=key"),
				pb("alt", "alt", false, "", "Require Alt for event-type=key"),
				pb("shift", "shift", false, "", "Require Shift for event-type=key"),
			},
		},
		{
			Domain: "input-map", Name: "ensure-binding", PluginCommand: "ensure_binding",
			Summary: "Bind an input event if not already bound (idempotent)",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("action", "action", true, "", "Action name"),
				ps("event-type", "event_type", true, "", "key | mouse_button | joy_button | joy_axis"),
				pf("deadzone", "deadzone", false, "0.5", "Analog deadzone"),
				ps("keycode", "keycode", false, "", "Godot keycode for event-type=key, e.g. A, Space, F1"),
				pi("button", "button", false, "", "Button index for mouse_button (1=left) / joy_button"),
				pi("axis", "axis", false, "", "JoyAxis index for joy_axis, e.g. 0 = left stick X"),
				pf("axis-value", "axis_value", false, "", "Axis value for joy_axis (default 1.0 plugin-side)"),
				pb("ctrl", "ctrl", false, "", "Require Ctrl for event-type=key"),
				pb("alt", "alt", false, "", "Require Alt for event-type=key"),
				pb("shift", "shift", false, "", "Require Shift for event-type=key"),
			},
		},
	}
}

// autoloadOps: project autoload singletons.
func autoloadOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "autoload", Name: "list", PluginCommand: "list_autoloads",
			Summary: "List the project's autoload singletons",
			Timeout: DefaultTimeout,
		},
		{
			Domain: "autoload", Name: "add", PluginCommand: "add_autoload",
			Summary: "Register an autoload singleton",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("name", "name", true, "", "Autoload name"),
				ps("path", "path", true, "", "res:// path of the script or scene"),
				pb("singleton", "singleton", false, "true", "Register as a singleton"),
			},
		},
		{
			Domain: "autoload", Name: "remove", PluginCommand: "remove_autoload",
			Summary: "Remove an autoload",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("name", "name", true, "", "Autoload name"),
			},
		},
	}
}

// gameOps: introspect and drive the RUNNING game. Every op routes through
// the game_command wrapper (WrapOp → {"op": ..., "params": {...}}).
func gameOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "game", Name: "get-scene-tree", PluginCommand: "game_command", WrapOp: "get_scene_tree",
			Summary: "Scene tree of the running game",
			Timeout: GameTimeout,
			Params: []ParamSpec{
				pi("depth", "depth", false, "10", "Maximum depth below the root"),
				ps("root-path", "root_path", false, "", "Subtree root (default: game root)"),
				ps("name", "name", false, "", "Only include nodes whose name matches this glob (non-matching subtrees are still traversed; hits carry full paths)"),
			},
		},
		{
			Domain: "game", Name: "get-node-info", PluginCommand: "game_command", WrapOp: "get_node_info",
			Summary: "Property snapshot of a node in the running game",
			Timeout: GameTimeout,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Node path in the running game"),
				pb("include-properties", "include_properties", false, "true", "Include the property dump"),
				pj("fields", "fields", false, `JSON array of property names to keep in the properties dump, e.g. ["position","visible"] (unresolved names are reported in unknown_fields)`),
			},
		},
		{
			Domain: "game", Name: "get-ui-elements", PluginCommand: "game_command", WrapOp: "get_ui_elements",
			Summary: "UI element tree of the running game",
			Timeout: GameTimeout,
			Params: []ParamSpec{
				ps("root-path", "root_path", false, "", "Subtree root (default: game root)"),
				pb("include-hidden", "include_hidden", false, "false", "Include hidden controls"),
				pb("include-disabled", "include_disabled", false, "true", "Include disabled controls"),
				pi("max-depth", "max_depth", false, "10", "Maximum depth below the root"),
			},
		},
		{
			Domain: "game", Name: "input-key", PluginCommand: "game_command", WrapOp: "input_key",
			Summary: "Send a key press/release to the running game",
			Timeout: GameTimeout,
			Params: []ParamSpec{
				ps("key", "key", true, "", "Key name, e.g. Space, A, Escape"),
				pb("pressed", "pressed", false, "true", "true = press, false = release"),
				pb("echo", "echo", false, "false", "Mark the event as an echo"),
			},
		},
		{
			Domain: "game", Name: "input-mouse", PluginCommand: "game_command", WrapOp: "input_mouse",
			Summary: "Send a mouse event to the running game",
			Timeout: GameTimeout,
			// fork 补丁：合成事件不进 viewport 鼠标状态，返回值里明说
			ResponseNote: `Synthetic events do NOT enter the viewport mouse state — the payload carries "affects_mouse_position": false and get_global_mouse_position() is unchanged. To actually move the aim use game input-warp; read back with game get-mouse.`,
			Params: []ParamSpec{
				ps("event", "event", true, "", "button | motion"),
				pj("position", "position", false, `JSON {"x":..,"y":..} position`),
				ps("button", "button", false, "left", "left | right | middle | wheel_up | wheel_down"),
				pb("pressed", "pressed", false, "true", "true = press, false = release"),
			},
		},
		{
			// fork 补丁（game_helper.gd input_warp）：鼠标瞄准类玩法的真实鼠标控制
			Domain: "game", Name: "input-warp", PluginCommand: "game_command", WrapOp: "input_warp",
			Summary:      "Warp the OS mouse cursor so the game's aim actually follows (Input.warp_mouse + coordinate echo)",
			Timeout:      GameTimeout,
			ResponseNote: `Echoes all three coordinate spaces: mouse_window (window client pixels — what Input.warp_mouse takes), mouse_canvas (viewport/stretch space), mouse_world (what get_global_mouse_position() returns). clamped:true + actual position when the OS clamped the warp (target outside window/screen).`,
			Params: []ParamSpec{
				pj("position", "position", true, `JSON {"x":..,"y":..} target position`),
				ps("space", "space", false, "window", "Coordinate space of --position: window (client pixels) | canvas (viewport/stretch) | world (game world)"),
			},
		},
		{
			// fork 补丁（game_helper.gd get_mouse）：瞄准到位自检
			Domain: "game", Name: "get-mouse", PluginCommand: "game_command", WrapOp: "get_mouse",
			Summary:      "Read the current mouse position in window/canvas/world coordinate spaces",
			Timeout:      GameTimeout,
			ResponseNote: `Returns mouse_window / mouse_canvas / mouse_world — use after input-warp (or during play) to verify the aim is where the script intends.`,
		},
		{
			Domain: "game", Name: "input-gamepad", PluginCommand: "game_command", WrapOp: "input_gamepad",
			Summary: "Send a gamepad event to the running game",
			Timeout: GameTimeout,
			Params: []ParamSpec{
				pi("device", "device", false, "0", "Gamepad device id"),
				ps("control", "control", false, "button", "button | axis"),
				pi("index", "index", false, "0", "Button or axis index"),
				pb("pressed", "pressed", false, "true", "true = press, false = release"),
				pf("value", "value", false, "0.0", "Axis value for control=axis"),
			},
		},
		{
			Domain: "game", Name: "input-action", PluginCommand: "game_command", WrapOp: "input_action",
			Summary: "Press/release an input action in the running game",
			Timeout: GameTimeout,
			Params: []ParamSpec{
				ps("action", "action", true, "", "Input action name"),
				pb("pressed", "pressed", false, "true", "true = press, false = release"),
				pf("strength", "strength", false, "1.0", "Action strength (analog)"),
			},
		},
		{
			Domain: "game", Name: "input-sequence", PluginCommand: "game_command", WrapOp: "input_sequence",
			Summary: "Drive a frame-timed action timeline in the running game",
			Timeout: InputSequenceTimeout,
			Params: []ParamSpec{
				pj("steps", "steps", true, `JSON array of {"at_frame": N, "action": "...", "pressed": bool, "strength": f}`),
				pi("settle-frames", "settle_frames", false, "0", "Frames to settle after the last step"),
			},
		},
		{
			Domain: "game", Name: "input-state", PluginCommand: "game_command", WrapOp: "input_state",
			Summary: "Read currently pressed input actions in the running game",
			Timeout: GameTimeout,
			Params: []ParamSpec{
				pj("actions", "actions", false, "JSON array of action names (default: all)"),
			},
		},
		{
			Domain: "game", Name: "debug-draw", PluginCommand: "game_command", WrapOp: "debug_draw",
			Summary: "Toggle engine debug rendering (collision shapes, paths, navigation) in the running game",
			Timeout: GameTimeout,
			ResponseNote: `{"debug_collisions_hint","debug_paths_hint","debug_navigation_hint"} —
  the current states after applying the given flags. Pair with editor screenshot
  --source game (or editor record) to verify collision-shape fit visually.`,
			DocNote: "Debug outlines ARE included in the game framebuffer capture; a capture flagged `stale_frame` predates your change (frozen/backgrounded game), retry with a live loop.",
			Params: []ParamSpec{
				ps("collisions", "collisions", false, "", "on | off (omit to leave unchanged)"),
				ps("paths", "paths", false, "", "on | off (omit to leave unchanged)"),
				ps("navigation", "navigation", false, "", "on | off (omit to leave unchanged)"),
			},
		},
		{
			// 上游 v4 原生命令（非 game_command 包装）：走编辑器调试桥的
			// suspend/resume/next_frame/debug_status。
			Domain: "game", Name: "debug-control", PluginCommand: "game_debug_control",
			Summary: "Suspend/resume/step the running game via the editor debugger bridge",
			Timeout: GameTimeout,
			ResponseNote: `{"action","status","frames_advanced"} — debug_status reports
  whether the game is currently suspended; next_frame advances exactly one frame
  while suspended.`,
			Params: []ParamSpec{
				ps("action", "action", true, "", "suspend | resume | next_frame | debug_status"),
			},
		},
	}
}
