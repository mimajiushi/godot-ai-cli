## Plugin-side telemetry helper — STRIPPED in the godot-ai-cli fork.
##
## The upstream implementation relays plugin-only events (dock startup,
## self-update outcome, plugin reload, dev-server toggle) to the Python
## MCP server via `send_event("plugin_event", {...})`. The fork removes
## reporting entirely: no event is buffered, sent, or persisted, and no
## data ever leaves the editor. This file keeps the original public
## interface as no-ops so callers (plugin.gd, mcp_dock.gd, handlers,
## update_reload_runner.gd) need no changes.
##
## Upstream project: https://github.com/hi-godot/godot-ai (MIT).

extends RefCounted

## Allowlist kept for interface parity with upstream; unused in the fork.
const _ALLOWED_EVENTS := [
	"dock_startup",
	"plugin_reload",
	"self_update",
	"dev_server_toggle",
]

const _MAX_BUFFER := 32

## EditorSetting key kept for interface parity; the fork never writes it.
const PENDING_PLUGIN_RELOAD_KEY := "godot_ai/pending_plugin_reload_event"


## No-op in the fork: pending reload events are never recorded.
static func record_pending_plugin_reload(_source: String) -> void:
	pass


## Read + clear an EditorSetting JSON-encoded event payload. Unchanged
## from upstream: `plugin.gd::_flush_pending_self_update_telemetry`
## calls this directly, so the read-and-clear behavior is preserved
## (it only touches local EditorSettings — no reporting involved).
static func _drain_editor_setting_dict(key: String):
	var settings := EditorInterface.get_editor_settings()
	if settings == null:
		return null
	if not settings.has_setting(key):
		return null
	var raw := str(settings.get_setting(key))
	settings.set_setting(key, "")
	if raw == "":
		return null
	var parsed = JSON.parse_string(raw)
	if typeof(parsed) != TYPE_DICTIONARY:
		return null
	return parsed


var _pending: Array = []  ## Never populated in the fork; kept for the test seam.

func _init(_connection) -> void:
	## Deliberately no state kept and no signal subscription: events are
	## dropped unconditionally, so there is nothing to flush.
	pass


## No-op in the fork: every event is dropped before buffering or sending.
func record_event(_name: String, _data: Dictionary = {}) -> void:
	pass


func _on_connection_state_changed(_is_open: bool) -> void:
	pass


func _flush() -> void:
	_pending.clear()


## No-op in the fork: nothing is ever sent over the connection.
func _send_one(_name: String, _data: Dictionary) -> void:
	pass

# --- convenience emitters (all no-ops in the fork) ----------------------

func record_dock_startup(_extra: Dictionary = {}) -> void:
	pass


func record_self_update(
	_status: String,
	_from_version: String = "",
	_to_version: String = "",
	_error: String = "",
) -> void:
	pass


func record_dev_server_toggle(_action: String) -> void:
	pass


## Drains (clears) any stale pending key left by an upstream install so
## state stays clean, but never records an event.
func flush_pending_plugin_reload() -> void:
	_drain_editor_setting_dict(PENDING_PLUGIN_RELOAD_KEY)

# --- test seam -------------------------------------------------------------

## Test seam kept for interface parity; arguments are ignored in the fork
## because events are never buffered or sent regardless of state.
func _test_set_state(_connection, _disabled: bool) -> void:
	_pending.clear()


func _test_pending_count() -> int:
	return _pending.size()
