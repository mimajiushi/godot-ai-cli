## Locates the godot-ai-cli executable for the plugin-spawned daemon
## (godot-ai-cli fork).
##
## When the startup walk finds the backend port free, the fork prefers
## spawning the Go daemon (`godot-ai-cli serve`) over the upstream Python
## server: a plugin-spawned daemon is adopted by a later CLI launch
## (EnsureRunning matches version/ws_port), so a manually opened editor
## session stays reusable — a spawned Python server would collide with that
## launch as FOREIGN_SERVER. Only when this lookup comes up empty does the
## walk fall back to the upstream Python spawn.
##
## Two tiers, in order:
##   1. GODOT_AI_CLI_BIN — explicit override; honored only when it names an
##      existing file, so a stale value can never shadow a working PATH.
##   2. PATH scan for the platform's exe name (godot-ai-cli.exe on Windows,
##      godot-ai-cli elsewhere) — plain FileAccess checks, no subprocess.
##
## 定位本身保持纯 GDScript（无 shell-out）：find_bin 运行在 startup walk
## 的主线程上，plain OS.get_environment 安全（从不派发到 #691 worker）。
## 例外是 spawn 守卫的 `probe_version_output`（`<bin> -v` 子进程）——
## 它只能由调用方放进 _run_blocking 的 worker 线程执行。

extends RefCounted

const McpVersionCompat := preload("res://addons/godot_ai/utils/version_compat.gd")

const ENV_BIN_OVERRIDE := "GODOT_AI_CLI_BIN"
const _EXE_WINDOWS := "godot-ai-cli.exe"
const _EXE_POSIX := "godot-ai-cli"


## Resolve the binary against the live process environment. Returns "" when
## no tier hits — the caller then falls back to the upstream Python spawn.
static func find_bin() -> String:
	return find_bin_in(
		OS.get_environment(ENV_BIN_OVERRIDE),
		OS.get_environment("PATH"),
		OS.get_name()
	)


## Pure core, split from find_bin so tests can drive every tier without
## mutating the process environment. `platform` matches OS.get_name()
## values ("Windows" selects the .exe name and ";" PATH separator).
static func find_bin_in(bin_override: String, path_env: String, platform: String) -> String:
	if not bin_override.is_empty() and FileAccess.file_exists(bin_override):
		return bin_override
	var exe_name := _EXE_WINDOWS if platform == "Windows" else _EXE_POSIX
	var sep := ";" if platform == "Windows" else ":"
	for raw_dir in path_env.split(sep, false):
		var dir := raw_dir.strip_edges()
		if dir.is_empty():
			continue
		var candidate := dir.path_join(exe_name)
		if FileAccess.file_exists(candidate):
			return candidate
	return ""


# ---- spawn 守卫（版本验证） ---------------------------------------------
#
# beta.13 事故回归：PATH 里的 godot-ai-cli 可能捆绑了与本插件不同版本的
# 插件代码；盲目 spawn 会主动制造一个错配 daemon。spawn 前先执行
# `<bin> -v` 并解析 `bundled plugin: godot-ai vX.Y.Z`，与本插件
# plugin.cfg 版本精确相等才允许 spawn。

## 执行 `<bin> -v` 并返回原始 stdout（含 stderr 合并不可得——只取
## stdout；cobra 的 version 输出写在 stdout）。子进程调用——调用方必须
## 把它放进 _run_blocking 的 worker 线程，禁止在主线程直接调用。
## 进程启动失败/超时由 OS.execute 的返回码体现：非 OK 时输出为空串。
static func probe_version_output(bin: String) -> String:
	var output: Array = []
	var exit_code := OS.execute(bin, ["-v"], output, true)
	if exit_code != 0:
		return ""
	return str(output[0]) if not output.is_empty() else ""


## 从 `<bin> -v` 输出中解析捆绑插件版本。匹配
## `bundled plugin:      godot-ai vX.Y.Z ...` 行（Go 侧
## internal/cli/root.go versionTemplate），取 v 后第一段连续版本字符。
## 解析失败返回 ""。
static func parse_bundled_plugin_version(version_output: String) -> String:
	for line in version_output.split("\n", false):
		var idx := line.find("bundled plugin:")
		if idx < 0:
			continue
		var rest := line.substr(idx + "bundled plugin:".length()).strip_edges()
		if not rest.begins_with("godot-ai"):
			continue
		rest = rest.substr("godot-ai".length()).strip_edges()
		if rest.begins_with("v") or rest.begins_with("V"):
			rest = rest.substr(1)
		## 版本串到第一个空格/括号为止（模板尾部还有 "(forked, …)" 说明）
		var token := rest.split(" ")[0].split(")")[0].strip_edges()
		var parsed := McpVersionCompat.parse_semver(token)
		if parsed.is_empty():
			return ""
		return "%d.%d.%d" % [int(parsed["major"]), int(parsed["minor"]), int(parsed["patch"])]
	return ""


## 纯决策函数：只有 `<bin> -v` 输出里的 bundled plugin 版本与本插件版本
## 精确相等才允许 spawn。解析失败（含 dev 构建的占位输出）一律拒绝——
## 落入既有的无 daemon 流程，而不是赌一个版本不明的二进制。
static func should_spawn(bin_version_output: String, own_version: String) -> bool:
	var bundled := parse_bundled_plugin_version(bin_version_output)
	if bundled.is_empty() or own_version.strip_edges().is_empty():
		return false
	return bundled == own_version.strip_edges()
