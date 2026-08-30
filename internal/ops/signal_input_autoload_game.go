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
			},
		},
		{
			Domain: "game", Name: "get-node-info", PluginCommand: "game_command", WrapOp: "get_node_info",
			Summary: "Property snapshot of a node in the running game",
			Timeout: GameTimeout,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Node path in the running game"),
				pb("include-properties", "include_properties", false, "true", "Include the property dump"),
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
			Params: []ParamSpec{
				ps("event", "event", true, "", "button | motion"),
				pj("position", "position", false, `JSON {"x":..,"y":..} position`),
				ps("button", "button", false, "left", "left | right | middle | wheel_up | wheel_down"),
				pb("pressed", "pressed", false, "true", "true = press, false = release"),
			},
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
	}
}
