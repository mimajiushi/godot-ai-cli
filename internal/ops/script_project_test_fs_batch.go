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
			CLIFlags: []CLIFlagSpec{
				cls("old-file", "", "Read old_text VERBATIM from this UTF-8 file (a leading BOM is stripped, nothing is JSON-parsed); an explicit --old-text wins. The PowerShell 5.1-safe channel for anchors containing ASCII double quotes"),
				cls("new-file", "", "Read new_text VERBATIM from this UTF-8 file (a leading BOM is stripped, nothing is JSON-parsed); an explicit --new-text wins. Quotes and newlines survive untouched"),
			},
			HelpNote: "Windows PowerShell 5.1 strips the ASCII double quotes out of an argument before this process sees it, so an anchor or replacement containing `\"` passed as `--old-text`/`--new-text` (or inside an inline `--params` payload) arrives mangled: the patch then fails with `old_text not found`, or worse, edits the wrong bytes. Use the file channels: `--old-file <path>` / `--new-file <path>` read both sides VERBATIM (no JSON parsing; quotes, backslashes and newlines survive), and `--params-file <json>` carries a whole JSON payload written by an external tool. Explicit text flags still win over their file channel.",
			DocNote:  "Windows PowerShell 5.1 strips the ASCII double quotes out of an argument before the native process sees it, so an anchor or replacement containing `\"` cannot travel through `--old-text`/`--new-text` (nor through an inline `--params` JSON payload). Use the file channels: `--old-file <path>` / `--new-file <path>` read both sides verbatim (no JSON parsing, quotes and newlines preserved), and `--params-file <json>` carries a whole written-by-a-tool payload. An explicit `--old-text`/`--new-text` still wins when a flag and a file are both given. The response's `diagnostics` describe the file as it now sits on disk; `reload_reason:\"reload_pending\"` together with `reload_pending:true` means the reported reload noise is unconfirmed and the editor's in-memory copy was not refreshed yet, while a genuine parse failure keeps `reload_reason:\"parse_error\"` at error level.",
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
			Domain: "project", Name: "set-main-scene", PluginCommand: "set_main_scene",
			Summary: "Set the project main scene (application/run/main_scene)",
			Timeout: DefaultTimeout, Write: true,
			Params: []ParamSpec{
				ps("path", "path", true, "", "res:// path of an existing PackedScene to boot into"),
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
		{
			Domain: "project", Name: "continue", PluginCommand: "project_continue",
			Summary:      "Resume a game paused at a debugger break (e.g. after a failed eval)",
			Timeout:      DefaultTimeout,
			ResponseNote: `{"continued","was_breaked"}`,
			DocNote:      "A failed eval that parked the game at a debugger break already auto-resumes; use this for breaks the game hit on its own.",
		},
		{
			Domain: "project", Name: "focus", PluginCommand: "project_focus",
			Summary:      "Bring the running game window to the foreground",
			Timeout:      DefaultTimeout,
			ResponseNote: `{"focused"}`,
			DocNote:      "Uses DisplayServer.window_move_to_foreground — the recovery when eval reports the game's main loop not advancing because the game window lost focus. May not take effect while the game is parked at a debugger break; run `project continue` first in that case.",
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
			// fork：字段口径文档化（test-run-response-fields-doc 需求）
			ResponseNote: "Fields: passed / failed / skipped / total / assertions (Σ per-test assertion_count, no --verbose needed) / duration_ms / suite_count / suites_run / edited_scene / game_status (always present: active + status + readiness; a live game makes the no-game-precondition suites fail deterministically). Optional: scene_warning / play_state_warning (failed>0 with a live game) / failures (failed>0) / load_errors / results (only --verbose). Empty-value fields are omitted — absent means none.",
			Params: []ParamSpec{
				ps("suite", "suite", false, "", "Run only this suite"),
				ps("test-name", "test_name", false, "", "Run only this test"),
				ps("exclude-test-name", "exclude_test_name", false, "", "Skip this test"),
				pb("verbose", "verbose", false, "false", "Verbose per-test output"),
				// fork 补丁（test_handler.gd）：live 游戏时 EDITOR_NOT_READY 拒绝
				pb("require-idle-session", "require_idle_session", false, "false", "Refuse with EDITOR_NOT_READY/EDITOR_PLAYING when a game run is live (guards CI from phantom no-game failures)"),
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
		{
			// 上游 v4.2.x 新增：deferred 文件系统变更（30s 预算）。引用检查
			// 在插件侧完成，被引用时拒绝 unless force。
			Domain: "filesystem", Name: "move", PluginCommand: "move_file",
			Summary: "Move a file or directory tree within the project (uid-preserving, reference-checked)",
			Timeout: ScanTimeout, Write: true,
			ResponseNote: `deferred reply; requires a direct command (batch_execute cannot await it). Path-style references block the move — rewrite them to uid:// first.`,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Source res:// file or directory"),
				ps("new-path", "new_path", true, "", "Destination res:// path (parent directory must exist)"),
			},
		},
		{
			Domain: "filesystem", Name: "rename", PluginCommand: "rename_file",
			Summary: "Rename a file or directory in place (uid-preserving, reference-checked)",
			Timeout: ScanTimeout, Write: true,
			ResponseNote: `deferred reply; same reference policy as filesystem move.`,
			Params: []ParamSpec{
				ps("path", "path", true, "", "Source res:// file or directory"),
				ps("new-name", "new_name", true, "", "New bare name (no path separators)"),
			},
		},
		{
			Domain: "filesystem", Name: "remove", PluginCommand: "remove_file",
			Summary: "Remove a file or directory (OS trash by default; reference-checked)",
			Timeout: ScanTimeout, Write: true,
			ResponseNote: `deferred reply. referenced_by lists blockers; pass force=true only for known dangling references. Permanent deletion is file-only.`,
			Params: []ParamSpec{
				ps("path", "path", true, "", "res:// file or directory to remove"),
				pb("permanent", "permanent", false, "false", "Delete permanently instead of OS trash (files only)"),
				pb("force", "force", false, "false", "Proceed despite remaining references (dangling references only)"),
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
			// 命名对照（需求 resource-create-payload-file 附带项）：batch_execute
			// 只认插件命令名，CLI op 名会被拒并在 suggestions 里提示合法插件名。
			DocNote: "Command naming: batch_execute entries take PLUGIN command names (`create_node`), not CLI op names (`node create`) — an unknown name fails with fuzzy `suggestions`. Rule of thumb: the CLI name is `<domain> <kebab-name>`, the plugin name the snake_case verb_noun shown as `Plugin command:` in every op's -h and entry below (`scene open` → `open_scene`); `call <plugin_command>` accepts the plugin form directly.",
			Params: []ParamSpec{
				pj("commands", "commands", false, `JSON array of {"command": ..., "params": {...}} (or pass --file)`),
				pb("undo", "undo", false, "true", "Roll back applied commands on first error"),
			},
			CLIFlags: []CLIFlagSpec{
				cls("file", "", `JSON file containing an array of {"command": ..., "params": {...}}`),
			},
		},
	}
}
