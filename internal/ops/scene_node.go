package ops

// sceneOps: scene tree reads and scene file management.
func sceneOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "scene", Name: "get-hierarchy", PluginCommand: "get_scene_tree",
			Summary: "Paginated scene tree walk of the edited scene",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				pi("depth", "depth", false, "10", "Maximum depth below the root"),
				pi("offset", "offset", false, "0", "Skip this many nodes (pagination)"),
				pi("limit", "limit", false, "100", "Maximum nodes to return (pagination)"),
			},
		},
		{
			Domain: "scene", Name: "get-roots", PluginCommand: "get_open_scenes",
			Summary: "List the scenes currently open in editor tabs",
			Timeout: DefaultTimeout,
		},
		{
			Domain: "scene", Name: "create", PluginCommand: "create_scene",
			Summary: "Create a new scene file with a given root node type",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "res:// path of the new scene"),
				ps("root-type", "root_type", false, "Node3D", "Root node class (e.g. Node3D, Node2D, Control)"),
				ps("root-name", "root_name", false, "", "Root node name (default: derived from path)"),
			},
		},
		{
			Domain: "scene", Name: "open", PluginCommand: "open_scene",
			Summary: "Open a scene in the editor",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "res:// path of the scene to open"),
				pb("force-reload", "force_reload", false, "false", "Reload from disk, discarding unsaved changes"),
			},
			DocNote: "The response data may carry a `warning` string: the scene file on disk is NEWER than what the editor had loaded (external edit, another tool wrote it), so the open picked up on-disk content that may differ from the editor's previous in-memory state. Informational, not an error — save via `scene save` or re-open with `--force-reload` to settle the state explicitly.",
		},
		{
			Domain: "scene", Name: "save", PluginCommand: "save_scene",
			Summary: "Save the currently edited scene",
			Timeout: DefaultTimeout, Write: true,
			ResponseNote: `{"path","undoable","reason","written_paths":[...],"resaved_resources":[...]}` +
				` — written_paths lists the files THIS call wrote explicitly (for scene save that is always exactly [path]), resaved_resources lists the resource files the editor re-serialized (or newly created) as a side effect while saving — their on-disk size/mtime changed even though the call never named them. "write_track_truncated":true is appended when the scan hit its file cap, which makes resaved_resources a partial result (written_paths stays exact).`,
			DocNote: "Saving a scene is not a single-file write: the editor re-serializes the scene AND any resource it has loaded, which is how hand-written comments in an unrelated .tres can vanish and values equal to a script default get dropped without anyone asking for it. `resaved_resources` turns that side effect into a checkable fact: the scan snapshots every resource-ish file under the project (`.tscn`/`.scn`/`.tres`/`.res`/`.material`/`.theme`, hidden directories skipped) right before and right after the save and reports the ones that changed. Four limits worth knowing: the diff only walks the AFTER snapshot, so a resource DELETED during the save window is not reported; a resource NEWLY CREATED in that window does appear in `resaved_resources` (it was not there before the save); and detection is size+mtime based (Godot's FileAccess.get_modified_time is second-granular), so a file rewritten twice inside the same second to the same size is not detected. The test is a `(mtime,size)` SUPERSET in the other direction too: a file whose size or mtime moved while its bytes stayed identical is still listed, so a `resaved_resources` entry proves a file was TOUCHED, not that its content changed; compare hashes (`filesystem snapshot` / `filesystem changed`) when that distinction matters.",
		},
		{
			Domain: "scene", Name: "save-as", PluginCommand: "save_scene_as",
			Summary: "Save the current scene to a new path",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "res:// target path"),
			},
		},
	}
}

// nodeOps: node creation, search, mutation, and organization.
func nodeOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "node", Name: "create", PluginCommand: "create_node",
			Summary: "Create a node in the edited scene",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("type", "type", false, "", "Node class (default: Node)"),
				ps("name", "name", false, "", "Node name (default: class-derived)"),
				ps("parent-path", "parent_path", false, "", "Parent node path (default: scene root)"),
				ps("scene-path", "scene_path", false, "", "Instantiate this scene instead of a bare class"),
				ps("scene-file", "scene_file", false, "", "Guard: only apply when this scene is being edited"),
			},
		},
		{
			Domain: "node", Name: "find", PluginCommand: "find_nodes",
			Summary: "Find nodes by name, type, and/or group",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("name", "name", false, "", "Name substring/pattern to match"),
				ps("type", "type", false, "", "Class filter"),
				ps("group", "group", false, "", "Group filter"),
				pi("offset", "offset", false, "0", "Skip this many matches"),
				pi("limit", "limit", false, "100", "Maximum matches to return"),
			},
		},
		{
			Domain: "node", Name: "get-properties", PluginCommand: "get_node_properties",
			Summary: "Full property snapshot of a node",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Scene path of the node"),
				pj("fields", "fields", false, `JSON array of property names to include (default: all)`),
			},
		},
		{
			Domain: "node", Name: "get-children", PluginCommand: "get_children",
			Summary: "List a node's direct children (name, type, path)",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Scene path of the parent node"),
			},
		},
		{
			Domain: "node", Name: "get-groups", PluginCommand: "get_groups",
			Summary: "List a node's group memberships",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Scene path of the node"),
			},
		},
		{
			Domain: "node", Name: "delete", PluginCommand: "delete_node",
			Summary: "Delete a node from the edited scene",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Scene path of the node"),
				ps("scene-file", "scene_file", false, "", "Guard: only apply when this scene is being edited"),
			},
		},
		{
			Domain: "node", Name: "reparent", PluginCommand: "reparent_node",
			Summary: "Move a node under a new parent",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Scene path of the node"),
				ps("new-parent", "new_parent", true, "", "Scene path of the new parent"),
				ps("scene-file", "scene_file", false, "", "Guard: only apply when this scene is being edited"),
			},
		},
		{
			Domain: "node", Name: "set-property", PluginCommand: "set_property",
			Summary: "Set one property on a node",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Scene path of the node"),
				ps("property", "property", true, "", "Property name"),
				pj("value", "value", true, `JSON value, e.g. 42, "text", [1,2], {"x":1,"y":2}`),
				ps("scene-file", "scene_file", false, "", "Guard: only apply when this scene is being edited"),
			},
			CLIFlags: []CLIFlagSpec{
				cls("node-ref", "", "Shorthand for a Node reference value: `--node-ref ../Sprite2D` sends value as {\"$node\":\"../Sprite2D\"} (an explicit --value wins when both are given)"),
				cls("value-file", "", "Read --value from this UTF-8 file as raw JSON (any JSON type; a leading BOM is tolerated) instead of the command line; an explicit --value wins. The PowerShell 5.1-safe channel for JSON values carrying ASCII double quotes"),
			},
			DocNote: "Node-typed properties (e.g. `RemoteTransform2D.remote_path`-style NodePath exports, or `@export var target: Node2D` object slots) take a node REFERENCE, not plain JSON: `--value '{\"$node\":\"../Sprite2D\"}'` (or the `--node-ref ../Sprite2D` shorthand). The path resolves RELATIVE TO THE TARGET NODE (`--path`), so `../Sprite2D` on `/Root/Player` points at `/Root/Sprite2D`; scene-root-absolute forms like `/Root/Sprite2D` work too. After `scene save` the reference serializes exactly like a hand-dragged inspector assignment: NodePath-typed properties become a `NodePath(\"../Sprite2D\")` plus a `node_paths` entry in the .tscn, object-typed ones a NodePath the loader resolves. A reference that does not resolve fails with `NODE_NOT_FOUND`; a non-string or malformed `$node` payload fails with `INVALID_PARAMS`. Example: `node set-property --path /Root/Player --property target --node-ref ../Sprite2D`.",
		},
		{
			// fork 补丁（需求 R-3）：把属性恢复成"序列化默认值"并消掉 .tscn 里的覆盖行。
			// 与 set-property 的区别不是"设成同一个数字"，而是"按引擎的判定规则取默认值"：
			// set-property --value <脚本里写的默认值> 并不保证覆盖行消失（继承场景 A extends B
			// 的默认值来自 B 存过的那一份、子场景实例来自实例栈，脚本成员没有常量初始化器时
			// 脚本默认值也不是手写的那一个）。
			Domain: "node", Name: "reset-property", PluginCommand: "reset_property",
			Summary: "Restore one property to its serialization default and drop the scene override",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Scene path of the node"),
				ps("property", "property", true, "", "Property name"),
				ps("scene-file", "scene_file", false, "", "Guard: only apply when this scene is being edited"),
			},
			ResponseNote: `{"path","property","value","old_value","is_default","removed_override","default_source","pinned","undoable"}` +
				` — value is the restored (default) value, is_default says it now equals the default the packer compares against, removed_override says a stored override existed before the call, default_source names where that default came from ("inheritance_stack"/"instance_stack"/"script"/"class"), and pinned is true when the property is inspector-pinned (a pinned property is written to the .tscn even at its default, so the override line survives).`,
			DocNote: "Companion of `node set-property` for undoing an override instead of changing one: it writes the value Godot's scene packer treats as the default, so the next `scene save` drops the property's line from the .tscn and the node falls back to the script/class default (`@export var level_duration := 60` → an override written as `level_duration = 8` disappears instead of becoming `level_duration = 60`). The default is resolved with the SAME rule the packer uses (PropertyUtils::get_property_default_value): the value stored by the scene's inheritance stack (an edited scene A that extends B takes B's stored value), then by the instancing stack (a node inside an instanced sub-scene takes that sub-scene's stored value — neither is the script's number), else the topmost script's property default (Script.get_property_default_value), else the native class default; the response's `default_source` says which one was used (`inheritance_stack`/`instance_stack`/`script`/`class`). Hard-verifiable: `node reset-property --path . --property level_duration` then `scene save` then grep the .tscn for `level_duration` → no match, and the response reports `is_default:true`,`removed_override:true`. Three caveats, all visible in real behavior: a property the inspector pinned (`_edit_pinned_properties_`) is stored even at its default (reported as `pinned:true`); a property whose usage lacks PROPERTY_USAGE_STORAGE (e.g. `owner`) has no serialized override to drop and is refused with INVALID_PARAMS instead of corrupting the scene; and an Array/Dictionary whose value equals the default but holds a local-to-scene sub-resource is still written by the packer (`SceneState::pack` saves those on purpose), so for that shape the override line legitimately survives a reset.",
		},
		{
			Domain: "node", Name: "rename", PluginCommand: "rename_node",
			Summary: "Rename a node",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Scene path of the node"),
				ps("new-name", "new_name", true, "", "New node name"),
				ps("scene-file", "scene_file", false, "", "Guard: only apply when this scene is being edited"),
			},
		},
		{
			Domain: "node", Name: "duplicate", PluginCommand: "duplicate_node",
			Summary: "Duplicate a node (and its subtree)",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Scene path of the node"),
				ps("name", "name", false, "", "Name for the duplicate"),
				ps("scene-file", "scene_file", false, "", "Guard: only apply when this scene is being edited"),
			},
		},
		{
			Domain: "node", Name: "move", PluginCommand: "move_node",
			Summary: "Move a node to a different index among its siblings",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Scene path of the node"),
				pi("index", "index", true, "", "Target sibling index"),
				ps("scene-file", "scene_file", false, "", "Guard: only apply when this scene is being edited"),
			},
		},
		{
			Domain: "node", Name: "add-to-group", PluginCommand: "add_to_group",
			Summary: "Add a node to a group",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Scene path of the node"),
				ps("group", "group", true, "", "Group name"),
				ps("scene-file", "scene_file", false, "", "Guard: only apply when this scene is being edited"),
			},
		},
		{
			Domain: "node", Name: "remove-from-group", PluginCommand: "remove_from_group",
			Summary: "Remove a node from a group",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Scene path of the node"),
				ps("group", "group", true, "", "Group name"),
				ps("scene-file", "scene_file", false, "", "Guard: only apply when this scene is being edited"),
			},
		},
	}
}
