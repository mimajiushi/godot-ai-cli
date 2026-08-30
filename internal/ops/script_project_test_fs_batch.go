package ops

// scriptOps: GDScript lifecycle (create/patch carry per-write diagnostics).
func scriptOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "script", Name: "create", PluginCommand: "create_script",
			Summary: "Create a GDScript file (response includes per-write diagnostics)",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "res:// path of the new script"),
				ps("content", "content", false, "", "Full script source"),
			},
		},
		{
			Domain: "script", Name: "patch", PluginCommand: "patch_script",
			Summary: "Anchor-edit a GDScript file (old_text → new_text)",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "res:// path of the script"),
				ps("old-text", "old_text", true, "", "Exact text to replace"),
				ps("new-text", "new_text", true, "", "Replacement text"),
				pb("replace-all", "replace_all", false, "false", "Replace every occurrence"),
			},
		},
		{
			Domain: "script", Name: "read", PluginCommand: "read_script",
			Summary: "Read a GDScript source file",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("path", "path", true, "", "res:// path of the script"),
			},
		},
		{
			Domain: "script", Name: "attach", PluginCommand: "attach_script",
			Summary: "Attach a script to a node",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Scene path of the node"),
				ps("script-path", "script_path", true, "", "res:// path of the script"),
			},
		},
		{
			Domain: "script", Name: "detach", PluginCommand: "detach_script",
			Summary: "Detach the script from a node",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Scene path of the node"),
			},
		},
		{
			Domain: "script", Name: "find-symbols", PluginCommand: "find_symbols",
			Summary: "List the symbols (functions, vars, signals) of a script",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("path", "path", true, "", "res:// path of the script"),
			},
		},
	}
}

// projectOps: project settings and run/stop.
func projectOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "project", Name: "settings-get", PluginCommand: "get_project_setting",
			Summary: "Read one project setting by key",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("key", "key", true, "", "ProjectSettings key, e.g. application/config/name"),
			},
		},
		{
			Domain: "project", Name: "settings-set", PluginCommand: "set_project_setting",
			Summary: "Write one project setting",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("key", "key", true, "", "ProjectSettings key"),
				pj("value", "value", true, "JSON value to store"),
			},
		},
		{
			Domain: "project", Name: "run", PluginCommand: "run_project",
			Summary: "Play the project and wait briefly for game liveness",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("mode", "mode", false, "main", "main | current | custom scene mode"),
				ps("scene", "scene", false, "", "res:// scene for mode=custom"),
				pb("autosave", "autosave", false, "true", "Persist in-memory edits before running"),
			},
		},
		{
			Domain: "project", Name: "stop", PluginCommand: "stop_project",
			Summary: "Stop the running game",
			Timeout: DefaultTimeout,
		},
	}
}

// testOps: GDScript test suites (McpTestSuite) run inside the editor.
func testOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "test", Name: "run", PluginCommand: "run_tests",
			Summary: "Run GDScript test suites in the editor",
			Timeout: TestRunTimeout,
			Params: []ParamSpec{
				ps("suite", "suite", false, "", "Run only this suite"),
				ps("test-name", "test_name", false, "", "Run only this test"),
				ps("exclude-test-name", "exclude_test_name", false, "", "Skip this test"),
				pb("verbose", "verbose", false, "false", "Verbose per-test output"),
			},
		},
		{
			Domain: "test", Name: "results-get", PluginCommand: "get_test_results",
			Summary: "Read the most recent test run's results",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				pb("verbose", "verbose", false, "false", "Verbose per-test output"),
			},
		},
	}
}

// filesystemOps: project filesystem access. search_filesystem is
// registered under the upstream project handler but exposed here.
func filesystemOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "filesystem", Name: "read-text", PluginCommand: "read_file",
			Summary: "Read a project text file",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("path", "path", true, "", "res:// path"),
			},
		},
		{
			Domain: "filesystem", Name: "write-text", PluginCommand: "write_file",
			Summary: "Write a project text file",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "res:// path"),
				ps("content", "content", false, "", "File content"),
			},
		},
		{
			Domain: "filesystem", Name: "reimport", PluginCommand: "reimport",
			Summary: "Reimport assets (textures, models, audio — NOT scripts)",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				pj("paths", "paths", true, `JSON array of res:// paths`),
			},
		},
		{
			Domain: "filesystem", Name: "scan", PluginCommand: "scan_filesystem",
			Summary: "Scan the project filesystem and settle imports",
			Timeout: ScanTimeout,
		},
		{
			Domain: "filesystem", Name: "search", PluginCommand: "search_filesystem",
			Summary: "Search project files by name, type, and path prefix",
			Timeout: DefaultTimeout,
			Params: []ParamSpec{
				ps("name", "name", false, "", "File name substring"),
				ps("type", "type", false, "", "Resource type filter"),
				ps("path", "path", false, "", "res:// path prefix filter"),
				pi("offset", "offset", false, "0", "Skip this many matches"),
				pi("limit", "limit", false, "100", "Maximum matches to return"),
			},
		},
	}
}

// batchOps: atomic multi-command execution with rollback on first error.
func batchOps() []OpSpec {
	return []OpSpec{
		{
			Domain: "batch", Name: "execute", PluginCommand: "batch_execute",
			Summary: "Run multiple plugin commands atomically (rollback on first error); use --file or --params",
			Timeout: BatchTimeout, Write: true,
			Params: []ParamSpec{
				pj("commands", "commands", false, `JSON array of {"command": ..., "params": {...}} (or pass --file)`),
				pb("undo", "undo", false, "true", "Roll back applied commands on first error"),
			},
		},
	}
}
