@tool
class_name McpVersionCompat
extends RefCounted

## 插件↔daemon 版本兼容判定的单一口径（3.2.6 插件 + 3.2.7 daemon 线上
## 事故后引入——此前两侧都要求精确相等，patch 漂移直接硬阻断卡死 dock）。
##
## 契约（与 Go 侧 internal/pluginmeta 逐字一致）：
##   - major 与 minor 都相等即兼容（3.2.6 ↔ 3.2.7 允许连接，仅软警告）；
##   - minor 或 major 不等（3.2.x ↔ 3.3.0、3.x ↔ 4.x）拒绝；
##   - 预发布/构建后缀（-dev、-beta、+local）不参与放宽：任一侧带后缀
##     且双方字符串不完全相等时一律按错配处理——dev 构建的代码可能与
##     发布版不同，沿用旧的严格口径；
##   - 解析失败（空串、畸形）视为 unknown，不兼容。
##
## 纯函数工具：不读环境、不碰网络，供 server_lifecycle（adoption 探针、
## 握手校验）、mcp_dock（软警告展示）、cli_daemon（spawn 守卫）共用。

## 解析 "vX.Y.Z[-prerelease][+build]"，返回
## {"major", "minor", "patch", "suffix"}；畸形返回 {}。
## suffix 仅保留 "-" 之后的预发布段（"+build" 元数据直接丢弃）。
static func parse_semver(version: String) -> Dictionary:
	var v := version.strip_edges()
	if v.begins_with("v") or v.begins_with("V"):
		v = v.substr(1)
	if v.is_empty():
		return {}
	var plus := v.find("+")
	if plus >= 0:
		v = v.substr(0, plus)
	var suffix := ""
	var dash := v.find("-")
	if dash >= 0:
		suffix = v.substr(dash + 1)
		v = v.substr(0, dash)
	var parts := v.split(".")
	if parts.size() != 3:
		return {}
	var nums: Array[int] = []
	for p in parts:
		## 严格三段非负整数（禁止前导零）——版本门绝不靠猜
		if not p.is_valid_int() or int(p) < 0 or str(int(p)) != p:
			return {}
		nums.append(int(p))
	return {"major": nums[0], "minor": nums[1], "patch": nums[2], "suffix": suffix}


## 兼容判定：major 与 minor 相等（且后缀口径见 compatibility）。
static func versions_compatible(a: String, b: String) -> bool:
	return bool(compatibility(a, b).get("compatible", false))


## 完整判定，返回 {"compatible": bool, "reason": String}。reason 取值：
##   exact            —— 字符串精确相等
##   patch_mismatch   —— 兼容：major.minor 相等、patch 漂移
##   minor_mismatch   —— 不兼容：minor 不等
##   major_mismatch   —— 不兼容：major 不等
##   version_mismatch —— 不兼容：dev/预发布后缀错配（沿用旧严格口径）
##   unknown          —— 不兼容：任一侧为空或解析失败
static func compatibility(actual_version: String, expected_version: String) -> Dictionary:
	if actual_version.strip_edges().is_empty() or expected_version.strip_edges().is_empty():
		return {"compatible": false, "reason": "unknown"}
	if actual_version == expected_version:
		return {"compatible": true, "reason": "exact"}
	var a := parse_semver(actual_version)
	var b := parse_semver(expected_version)
	if a.is_empty() or b.is_empty():
		return {"compatible": false, "reason": "unknown"}
	if not str(a["suffix"]).is_empty() or not str(b["suffix"]).is_empty():
		return {"compatible": false, "reason": "version_mismatch"}
	if int(a["major"]) != int(b["major"]):
		return {"compatible": false, "reason": "major_mismatch"}
	if int(a["minor"]) != int(b["minor"]):
		return {"compatible": false, "reason": "minor_mismatch"}
	return {"compatible": true, "reason": "patch_mismatch"}


## 兼容但不等（patch 漂移）时的 dock 软警告文案；不适用（精确相等或
## 不兼容）时返回 ""。按漂移方向给出对齐动作：server 更新→升插件；
## plugin 更新→升 daemon。
static func compatible_mismatch_note(server_version: String, plugin_version: String) -> String:
	var c := compatibility(server_version, plugin_version)
	if not bool(c.get("compatible", false)) or str(c.get("reason", "")) == "exact":
		return ""
	var s := parse_semver(server_version)
	var p := parse_semver(plugin_version)
	if int(s["patch"]) > int(p["patch"]):
		return (
			"server v%s ≠ plugin v%s (compatible) — some ops may be missing; "
			+ "update the plugin via `godot-ai-cli plugin install` and restart the editor"
		) % [server_version, plugin_version]
	return (
		"server v%s ≠ plugin v%s (compatible) — some ops may be missing; "
		+ "update the daemon (`godot-ai-cli update`) or restart it"
	) % [server_version, plugin_version]


## minor/major 硬阻断文案尾部追加的版本对齐指引。
static func incompatible_align_hint() -> String:
	return (
		"run `godot-ai-cli plugin install --project <project dir>` "
		+ "and restart the editor, or match the daemon version"
	)
