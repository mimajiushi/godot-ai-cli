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
## Pure GDScript (no shell-outs): runs on the startup walk's main thread, so
## plain OS.get_environment is safe (never dispatched to a #691 worker).

extends RefCounted

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
