## Feature switches for the godot-ai-cli fork.
##
## Centralizes every "disabled in the fork" decision in one tiny file so
## fork patches stay one-liners and upstream diffs remain easy to read.
## Each switch is a function (not a constant) so GDScript's analyzer does
## not flag patched call sites as UNREACHABLE_CODE.
##
## Upstream project: https://github.com/hi-godot/godot-ai (MIT).

extends RefCounted


## Always true in the fork for the DOCK/dev-server surfaces: the
## godot-ai-cli Go daemon owns the backend endpoints, so the plugin must
## never spawn the upstream Python dev server from the dock's dev-server
## buttons, and the dev-mode toggle / Setup section stay hidden. The
## startup walk is no longer gated on this: since 3.2.7 it spawns the Go
## daemon itself when a godot-ai-cli binary is found (see the marked
## `godot-ai-cli daemon first` patch in utils/server_lifecycle.gd),
## falling back to the upstream Python spawn only when no binary exists.
static func external_daemon_mode() -> bool:
	return true


## Always true in the fork: MCP client configuration (Configure/Remove,
## drift reconfigure, per-client rows) is handled by the CLI/skill, not
## by in-editor UI or wire commands.
static func mcp_client_config_disabled() -> bool:
	return true
