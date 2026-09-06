@tool
class_name McpPortPins
extends RefCounted

## godot-ai-cli fork：项目级端口钉值解析（纯函数模块，无编辑器依赖）。
##
## launch 把端口钉值写进项目文件 `<project>/.godot/godot_ai_ports.json`
## （{"http_port":…,"ws_port":…}，默认端口也写；stop 时删除），不再写
## 全局 EditorSettings——多项目多 daemon 并存时，全局钉值会把别的项目
## 打开的编辑器引向错误端口。本模块是插件读取侧的统一入口，解析顺序：
##   项目文件（http_port/ws_port 两键齐全且均为合法端口才整体生效）
##   > EditorSettings 覆盖（http/ws 各自独立生效）
##   > 默认值
## 文件缺失/损坏/键不全一律静默回落下一级，不报错、不阻塞编辑器启动。

const PROJECT_PORTS_FILE := "res://.godot/godot_ai_ports.json"
const KEY_HTTP_PORT := "http_port"
const KEY_WS_PORT := "ws_port"
const MIN_PORT := 1024
const MAX_PORT := 65535


## 读取项目端口钉值文件。缺失/打不开/JSON 损坏/根节点非 Dictionary
## 一律返回 {}——调用方据此静默回落到 EditorSettings/默认值。
## `path` 可注入，测试用 user:// 临时文件驱动，不碰真实项目文件。
static func read_project_pins(path: String = PROJECT_PORTS_FILE) -> Dictionary:
	if not FileAccess.file_exists(path):
		return {}
	var f := FileAccess.open(path, FileAccess.READ)
	if f == null:
		return {}
	var text := f.get_as_text()
	f.close()
	var parsed: Variant = JSON.parse_string(text)
	if parsed is Dictionary:
		return parsed
	return {}


## 纯决策函数（fork delta 的核心，供测试直接注入三档输入）：
##   file_dict   —— 项目文件解析结果（缺失/损坏传 {}）
##   editor_http / editor_ws —— EditorSettings 覆盖值，0 表示未设置或越界
## 项目文件只在两键齐全且均为合法端口时整体生效：半残文件视为损坏，
## 避免 http 走钉值、ws 走默认的撕裂组合（两端口必须同属一个 daemon）。
static func resolve_ports(
	file_dict: Dictionary,
	editor_http: int,
	editor_ws: int,
	default_http: int,
	default_ws: int
) -> Dictionary:
	var file_http: Variant = file_dict.get(KEY_HTTP_PORT)
	var file_ws: Variant = file_dict.get(KEY_WS_PORT)
	if _is_valid_pin(file_http) and _is_valid_pin(file_ws):
		return {"http_port": int(file_http), "ws_port": int(file_ws)}
	return {
		"http_port": editor_http if _in_range(editor_http) else default_http,
		"ws_port": editor_ws if _in_range(editor_ws) else default_ws,
	}


## 合法钉值：JSON 数字（int 或整值 float）且落在可用端口区间。
## 字符串/非整 float/越界值一律无效，触发回落。
static func _is_valid_pin(value: Variant) -> bool:
	if value is int:
		return _in_range(value)
	if value is float:
		var p := int(value)
		return value == float(p) and _in_range(p)
	return false


static func _in_range(port: int) -> bool:
	return port >= MIN_PORT and port <= MAX_PORT
