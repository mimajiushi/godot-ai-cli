// Markdown op-catalog generation (`commands --format md`): regenerates the
// skill's references/commands.md from the op table, so syncing the doc after
// a CLI update is one command instead of a hand diff. The prose constants
// below are the single source of truth for the catalog's hand-written
// sections — edit them here, never in the generated file (the release
// doc-coverage gate diffs the generated output against the committed file).
package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/mimajiushi/godot-ai-cli/internal/ops"
)

// mdPreamblePaths is the "## Path and param conventions" section body.
const mdPreamblePaths = "- **Node paths are scene-root-absolute**: `/Root/Child` — e.g. `--path /Level1/Player` in a scene whose root is named `Level1`. Responses emit this same `/Root/...` form (`path`, `parent_path`, …). A bare `Level1/Player` (no leading slash, root name first) does NOT resolve — it reads as \"child `Level1` of the root\" and fails with `NODE_NOT_FOUND`.\n" +
	"- **`\"\"` (empty string) means the scene root** for parent-style params: `node create` with no `--parent-path` (or `--parent-path \"\"`) parents to the root. To name the root itself as a target use `/Root` (e.g. `/Level1`).\n" +
	"- **Resources always use `res://` project paths**: scenes, scripts, materials, themes, textures (`--path res://ui/main.tscn`).\n" +
	"- **`--params '<json>'` merge semantics**: the JSON object is the base of the wire params; explicit typed flags override colliding keys. Optional flags at their zero value stay off the wire unless passed via `--params`.\n" +
	"- **`scene create` switches the edited scene immediately** — the new scene is already open when the call returns; a following `scene open` of the same path is a no-op answering `\"settle\":\"already_current\"`.\n" +
	"- **`material get` reads SAVED resource files only**: `--path` must be an on-disk `.tres` / `.material` / `.res`. In-memory or node-attached materials are not readable through it — inspect those via the saved `.tscn` or `node get-properties --path /Root/Node`.\n" +
	"- **Mutation responses may carry a `reason` field** (e.g. `\"reason\":\"File save cannot be undone via editor undo\"` on `scene save`): it explains the accompanying `\"undoable\":false` — informational, not an error."

// mdConventionsBefore / mdConventionsMiddle / mdConventionsAfter are the
// constant bullets of "Conventions applying to every op", split around the
// two GENERATED bullets (long-ops list and CLI-side extras list) so those
// stay mechanically derived from the op table.
const mdConventionsBefore = "- Output is one JSON object on stdout; exit 0 on success. Errors are `{\"status\":\"error\",\"error\":{\"code\",\"message\",\"data\"}}` with exit 1. Add `--pretty` before the subcommand for indented JSON.\n" +
	"- Every op also accepts `--session <id>` (pin to one connected editor when several are attached) and `--params '<json>'` (base wire params; explicit flags override colliding keys).\n" +
	"- Optional flags left at their zero value are omitted from the wire params.\n" +
	"- `[write]` ops are gated on editor writability: while the editor is importing or playing they fail with `EDITOR_NOT_READY` (see references/troubleshooting.md)."

const mdConventionsMiddle = "- Daemon-level flags (`--http-port`) are accepted by every op command. Port resolution: explicit `--http-port` > port recorded by the last `launch`/`serve` (`last-daemon.json` in the user cache dir) > default 8000, with the default retried when the recorded port is unreachable. So after a custom-port launch you can omit `--http-port` entirely."

const mdConventionsAfter = "- Boolean flags take no space-separated value: write `--pressed` / `--pressed=false`, never `--pressed false` (the two-token form is auto-corrected when unambiguous, but any other stray positional fails with a steering error)."

// mdNonOpLeaves is the closing paragraph naming the CLI leaves that are not
// OpSpec-backed. Keep in sync with ops.HandWiredLeaves and the root command.
const mdNonOpLeaves = "Non-op leaves (not in this catalog): `session list` / `session activate` (daemon-side), `custom list` / `custom invoke` (third-party editor tools), `call <plugin_command>` (escape hatch), `image palette` / `image probe` / `image grid-detect` / `image view` (local texture palette analysis / pixel sampling / sprite-sheet grid detection / nearest-neighbor upscale for inspection — no editor needed), `tilemap dump` / `tilemap render` (local .tscn TileMapLayer decode / PNG composite — no editor needed), plus `launch` (pins the daemon ports per project in `.godot/godot_ai_ports.json`, never the global EditorSettings; `--upgrade-daemon` replaces a mismatched old daemon WITHOUT quitting its editors, then waits (up to 15s) for the kept editors to reconnect and reuses their sessions instead of spawning duplicates; `EDITOR_ALREADY_OPEN` blocks double-opening a project another daemon already hosts — `--force-spawn` overrides at file-lock/save risk) / `stop` (quits CLI-launched editor sessions, shuts the daemon down, and removes the per-project port pins; user-opened editors are kept and reported as `kept_sessions` — `--all` quits every session, `--session <id>` quits exactly one regardless of origin) / `status` (daemon + sessions with per-session `origin`, plus `known_daemons`: every recorded daemon on this machine probed live, and `ports_override_active` covering both per-project port files and legacy global pins) / `serve` / `godot detect` / `godot use` / `plugin install` / `update` / `version` / `commands`."

// printOpsMarkdown renders the skill op-catalog markdown for list (already
// in display order). The output is the complete references/commands.md.
func printOpsMarkdown(w io.Writer, list []ops.OpSpec) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# godot-ai-cli op catalog\n\n")
	fmt.Fprintf(&b, "Generated from `godot-ai-cli commands --format md` (%d ops). Regenerate against a newer binary with:\n\n", len(list))
	b.WriteString("```bash\ngodot-ai-cli commands --format md\n```\n\n")
	b.WriteString("## Path and param conventions\n\n")
	b.WriteString(mdPreamblePaths)
	b.WriteString("\n\nConventions applying to every op:\n\n")
	b.WriteString(mdConventionsBefore)
	b.WriteString("\n")
	b.WriteString(mdLongOpsBullet(list))
	b.WriteString("\n")
	b.WriteString(mdConventionsMiddle)
	b.WriteString("\n")
	if extras := mdCLIExtrasBullet(list); extras != "" {
		b.WriteString(extras)
		b.WriteString("\n")
	}
	b.WriteString(mdConventionsAfter)
	b.WriteString("\n\n")
	b.WriteString(mdNonOpLeaves)
	b.WriteString("\n")

	lastDomain := ""
	domainCount := 0
	for _, op := range list {
		if op.Domain != lastDomain {
			domainCount = 0
			for _, o := range list {
				if o.Domain == op.Domain {
					domainCount++
				}
			}
			plural := "ops"
			if domainCount == 1 {
				plural = "op"
			}
			fmt.Fprintf(&b, "\n## %s (%d %s)\n", op.Domain, domainCount, plural)
			lastDomain = op.Domain
		}
		b.WriteString("\n")
		b.WriteString(mdOpEntry(op))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// mdOpEntry renders one catalog entry: header line, wire line, optional
// CLI-side flags line, optional Response line, optional DocNote prose.
func mdOpEntry(op ops.OpSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### `%s %s` — %s\n", op.Domain, op.Name, op.Summary)
	fmt.Fprintf(&b, "`%s` · %ds", op.PluginCommand, int64(op.Timeout.Seconds()))
	if op.Write {
		b.WriteString(" · **[write]**")
	}
	b.WriteString(" · ")
	b.WriteString(mdParamList(op))
	b.WriteString("\n")
	if len(op.CLIFlags) > 0 {
		b.WriteString("CLI-side flags (not wire params): ")
		parts := make([]string, 0, len(op.CLIFlags))
		for _, f := range op.CLIFlags {
			part := fmt.Sprintf("`--%s` %s", f.Flag, mdKindLabel(f.Kind))
			if f.Default != "" {
				part += fmt.Sprintf(" (default %q)", f.Default)
			}
			part += " — " + f.Usage
			parts = append(parts, part)
		}
		b.WriteString(strings.Join(parts, "; "))
		b.WriteString("\n")
	}
	if op.ResponseNote != "" {
		b.WriteString("Response: ")
		b.WriteString(collapseWhitespace(op.ResponseNote))
		b.WriteString("\n")
	}
	if op.DocNote != "" {
		b.WriteString(op.DocNote)
		b.WriteString("\n")
	}
	return b.String()
}

// mdParamList renders the wire-params segment of an entry's second line.
func mdParamList(op ops.OpSpec) string {
	if len(op.Params) == 0 {
		return "no flags"
	}
	parts := make([]string, 0, len(op.Params))
	for _, p := range op.Params {
		part := fmt.Sprintf("--%s %s", p.Flag, mdKindLabel(p.Kind))
		if p.Required {
			part += " (required)"
		}
		if p.Default != "" {
			part += fmt.Sprintf(" (default %q)", p.Default)
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}

// mdKindLabel renders a param/flag kind for the catalog (stringArray is a
// CLI-only kind; the wire never sees it).
func mdKindLabel(kind string) string {
	if kind == ops.KindStringArray {
		return "string[]"
	}
	return kind
}

// mdLongOpsBullet derives the "Long ops" bullet from the op table: every op
// whose budget is at least the screenshot-tier 30s.
func mdLongOpsBullet(list []ops.OpSpec) string {
	var parts []string
	for _, op := range list {
		if op.Timeout >= ops.ScreenshotTimeout {
			parts = append(parts, fmt.Sprintf("`%s %s` %ds", op.Domain, op.Name, int64(op.Timeout.Seconds())))
		}
	}
	if len(parts) == 0 {
		return "- Timeouts are the daemon-side per-op budget; all ops listed here use the 8s default."
	}
	return "- Timeouts are the daemon-side per-op budget. Long ops: " + strings.Join(parts, ", ") + "."
}

// mdCLIExtrasBullet derives the CLI-side-extras bullet from the op table
// (flag names only; each op entry carries the full usage text).
func mdCLIExtrasBullet(list []ops.OpSpec) string {
	var parts []string
	for _, op := range list {
		if len(op.CLIFlags) == 0 {
			continue
		}
		names := make([]string, 0, len(op.CLIFlags))
		for _, f := range op.CLIFlags {
			names = append(names, "`--"+f.Flag+"`")
		}
		parts = append(parts, fmt.Sprintf("`%s %s` also accepts %s", op.Domain, op.Name, strings.Join(names, ", ")))
	}
	if len(parts) == 0 {
		return ""
	}
	return "- CLI-side extras not in the wire params: " + strings.Join(parts, "; ") + ". Each is documented on its op entry below and in `<domain> <op> -h`."
}

// collapseWhitespace flattens a multi-line ResponseNote onto one catalog line.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
