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
		},
		{
			Domain: "scene", Name: "save", PluginCommand: "save_scene",
			Summary: "Save the currently edited scene",
			Timeout: DefaultTimeout, Write: true,
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
