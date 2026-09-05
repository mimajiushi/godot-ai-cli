@tool
extends RefCounted

const ErrorCodes := preload("res://addons/godot_ai/utils/error_codes.gd")

## godot-ai-cli fork patch: SpriteFrames per-frame and sprite-sheet batch
## editing. Dedicated tools rather than property sets because animations and
## frames are method calls (add_animation/add_frame), not properties —
## resource_create's `properties` dict can't reach them.
##
## Every op loads the .tres, duplicates it before mutating (so open scenes
## holding the cached Resource don't silently change and a failed save can't
## corrupt the in-memory cache), and persists via McpResourceIO.save_to_disk —
## the same shape as curve_handler.set_points. File edits are not undoable.

var _undo_redo: EditorUndoRedoManager
var _connection: McpConnection


func _init(undo_redo: EditorUndoRedoManager, connection: McpConnection = null) -> void:
	_undo_redo = undo_redo
	_connection = connection


func add_animation(params: Dictionary) -> Dictionary:
	var resource_path := str(params.get("resource_path", ""))
	var anim_name := str(params.get("name", ""))
	if anim_name.is_empty():
		return ErrorCodes.make(ErrorCodes.MISSING_REQUIRED_PARAM, "Missing required param: name")

	var loaded = _load_spriteframes(resource_path)
	if loaded is Dictionary:
		return loaded
	var frames: SpriteFrames = loaded

	if frames.has_animation(anim_name):
		return ErrorCodes.make(
			ErrorCodes.INVALID_PARAMS,
			"Animation '%s' already exists in %s — rebuild it in place with spriteframes_from_sheet, or remove it manually first"
			% [anim_name, resource_path]
		)
	frames.add_animation(anim_name)
	frames.set_animation_speed(anim_name, float(params.get("speed", 5.0)))
	frames.set_animation_loop(anim_name, bool(params.get("loop", true)))
	return McpResourceIO.save_to_disk(frames, resource_path, true, "SpriteFrames", {
		"animation": anim_name,
		"animation_count": frames.get_animation_names().size(),
		"reason": "File save is persistent; edit the .tres file manually to revert",
	}, _connection)


func add_frame(params: Dictionary) -> Dictionary:
	var resource_path := str(params.get("resource_path", ""))
	var anim := str(params.get("anim", ""))
	var texture_path := str(params.get("texture", ""))
	if anim.is_empty():
		return ErrorCodes.make(ErrorCodes.MISSING_REQUIRED_PARAM, "Missing required param: anim")
	if texture_path.is_empty():
		return ErrorCodes.make(ErrorCodes.MISSING_REQUIRED_PARAM, "Missing required param: texture")

	var loaded = _load_spriteframes(resource_path)
	if loaded is Dictionary:
		return loaded
	var frames: SpriteFrames = loaded

	if not frames.has_animation(anim):
		return ErrorCodes.make(
			ErrorCodes.VALUE_OUT_OF_RANGE,
			"Animation '%s' not found in %s — add it first with resource spriteframes-add-animation"
			% [anim, resource_path]
		)

	var frame_tex := _load_frame_texture(texture_path, str(params.get("region", "")))
	if frame_tex is Dictionary:
		return frame_tex

	## -1 appends; a non-negative index inserts there (SpriteFrames.add_frame).
	var at_index := int(params.get("at_index", -1))
	frames.add_frame(anim, frame_tex, at_index)
	return McpResourceIO.save_to_disk(frames, resource_path, true, "SpriteFrames", {
		"animation": anim,
		"frame_count": frames.get_frame_count(anim),
		"reason": "File save is persistent; edit the .tres file manually to revert",
	}, _connection)


func from_sheet(params: Dictionary) -> Dictionary:
	var resource_path := str(params.get("resource_path", ""))
	var texture_path := str(params.get("texture", ""))
	var cell_str := str(params.get("cell", ""))
	var rows_str := str(params.get("rows", ""))
	var fps := float(params.get("fps", 8.0))
	var loop := bool(params.get("loop", true))
	if texture_path.is_empty():
		return ErrorCodes.make(ErrorCodes.MISSING_REQUIRED_PARAM, "Missing required param: texture")
	if cell_str.is_empty():
		return ErrorCodes.make(ErrorCodes.MISSING_REQUIRED_PARAM, "Missing required param: cell")
	if rows_str.is_empty():
		return ErrorCodes.make(ErrorCodes.MISSING_REQUIRED_PARAM, "Missing required param: rows")

	## cell "WxH" — both dimensions must be positive integers.
	var cw := 0
	var ch := 0
	var cell_parts := cell_str.split("x")
	if cell_parts.size() == 2 and cell_parts[0].is_valid_int() and cell_parts[1].is_valid_int():
		cw = int(cell_parts[0])
		ch = int(cell_parts[1])
	if cw <= 0 or ch <= 0:
		return ErrorCodes.make(
			ErrorCodes.INVALID_PARAMS,
			"Invalid cell '%s' — want \"WxH\" with positive integers, e.g. 32x32" % cell_str
		)

	## rows "0:idle,1:walk" — sheet row index to animation name.
	var row_specs: Array[Dictionary] = []
	for pair in rows_str.split(",", false):
		var kv := pair.split(":", false)
		if kv.size() != 2 or not kv[0].is_valid_int() or kv[1].strip_edges().is_empty():
			return ErrorCodes.make(
				ErrorCodes.INVALID_PARAMS,
				"Invalid rows entry '%s' — want \"<row>:<name>\", e.g. 0:idle,1:walk" % pair
			)
		row_specs.append({"row": int(kv[0]), "name": kv[1].strip_edges()})

	var tex: Resource = ResourceLoader.load(texture_path)
	if tex == null or not (tex is Texture2D):
		return ErrorCodes.make(
			ErrorCodes.RESOURCE_NOT_FOUND,
			"Texture not found or not a Texture2D: %s" % texture_path
		)
	var sheet: Texture2D = tex
	var columns := int(sheet.get_width() / cw)
	var row_count := int(sheet.get_height() / ch)
	if columns <= 0:
		return ErrorCodes.make(
			ErrorCodes.VALUE_OUT_OF_RANGE,
			"Cell %dx%d exceeds the sheet width %d of %s" % [cw, ch, sheet.get_width(), texture_path]
		)
	for spec in row_specs:
		if int(spec["row"]) >= row_count:
			return ErrorCodes.make(
				ErrorCodes.VALUE_OUT_OF_RANGE,
				"Row %d out of range — sheet %s has %d row(s) of height %d"
				% [int(spec["row"]), texture_path, row_count, ch]
			)

	## Edit in place when the .tres exists; otherwise start from a fresh
	## SpriteFrames (its implicit "default" animation is dropped).
	var frames: SpriteFrames
	var existed_before := ResourceLoader.exists(resource_path)
	if existed_before:
		var loaded = _load_spriteframes(resource_path)
		if loaded is Dictionary:
			return loaded
		frames = loaded
	else:
		frames = SpriteFrames.new()
		frames.remove_animation(&"default")

	## Rebuild each listed animation idempotently: a same-named animation is
	## removed first, so re-running with a changed cell/fps is an update, not
	## an error. Animations NOT named in rows are left untouched.
	var built: Array[Dictionary] = []
	for spec in row_specs:
		var anim_name: String = spec["name"]
		if frames.has_animation(anim_name):
			frames.remove_animation(anim_name)
		frames.add_animation(anim_name)
		frames.set_animation_speed(anim_name, fps)
		frames.set_animation_loop(anim_name, loop)
		for col in range(columns):
			var atlas := AtlasTexture.new()
			atlas.atlas = sheet
			atlas.region = Rect2(col * cw, int(spec["row"]) * ch, cw, ch)
			frames.add_frame(anim_name, atlas)
		built.append({"name": anim_name, "row": int(spec["row"]), "frames": columns})

	return McpResourceIO.save_to_disk(frames, resource_path, true, "SpriteFrames", {
		"created": not existed_before,
		"cell": {"w": cw, "h": ch},
		"columns": columns,
		"animations": built,
		"reason": "File save is persistent; edit the .tres file manually to revert",
	}, _connection)


## Load an existing SpriteFrames and duplicate it before mutation. Returns the
## duplicate on success or an error dict on failure (caller checks `is Dictionary`).
func _load_spriteframes(resource_path: String) -> Variant:
	var rpath_err = McpPathValidator.loadable_error(resource_path, "resource_path")
	if rpath_err != null:
		return rpath_err
	if not ResourceLoader.exists(resource_path):
		return ErrorCodes.make(ErrorCodes.RESOURCE_NOT_FOUND, "Resource not found: %s" % resource_path)
	var loaded: Resource = ResourceLoader.load(resource_path)
	if loaded == null:
		return ErrorCodes.make(
			ErrorCodes.INTERNAL_ERROR,
			"Failed to load %s (file exists but load returned null — may be corrupt)" % resource_path
		)
	if not (loaded is SpriteFrames):
		return ErrorCodes.make(
			ErrorCodes.WRONG_TYPE,
			"Resource is %s — must be SpriteFrames" % loaded.get_class()
		)
	return loaded.duplicate()


## Load the frame texture; with a region string, wrap it in an AtlasTexture.
## Returns a Texture2D on success or an error dict on failure.
func _load_frame_texture(texture_path: String, region_str: String) -> Variant:
	var tex: Resource = ResourceLoader.load(texture_path)
	if tex == null or not (tex is Texture2D):
		return ErrorCodes.make(
			ErrorCodes.RESOURCE_NOT_FOUND,
			"Texture not found or not a Texture2D: %s" % texture_path
		)
	if region_str.is_empty():
		return tex
	var rect = _parse_rect(region_str)
	if rect == null:
		return ErrorCodes.make(
			ErrorCodes.INVALID_PARAMS,
			"Invalid region '%s' — want \"x,y,w,h\", e.g. 0,32,32,32" % region_str
		)
	var atlas := AtlasTexture.new()
	atlas.atlas = tex
	atlas.region = rect
	return atlas


## "x,y,w,h" → Rect2; null on malformed input.
static func _parse_rect(s: String) -> Variant:
	var parts := s.split(",")
	if parts.size() != 4:
		return null
	var nums: Array[float] = []
	for p in parts:
		var piece := p.strip_edges()
		if not piece.is_valid_float():
			return null
		nums.append(float(piece))
	return Rect2(nums[0], nums[1], nums[2], nums[3])
