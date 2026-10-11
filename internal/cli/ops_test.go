package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
	"github.com/mimajiushi/godot-ai-cli/internal/ops"
	"github.com/mimajiushi/godot-ai-cli/internal/testutil/mockplugin"
)

// listenFree reserves an ephemeral loopback listener (close it to free the
// port for the test's use).
func listenFree(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

// itoa keeps flag argument assembly readable.
func itoa(n int) string { return strconv.Itoa(n) }

// TestCommandTreeCoversAllOps: every table op and every hand-wired leaf
// resolves in the generated command tree.
func TestCommandTreeCoversAllOps(t *testing.T) {
	root := NewRootCommand()
	for _, op := range ops.All() {
		cmd, _, err := root.Find([]string{op.Domain, op.Name})
		if err != nil || cmd == nil || cmd.Name() != op.Name {
			t.Errorf("op %s %s not reachable in the command tree (err=%v)", op.Domain, op.Name, err)
			continue
		}
		for _, p := range op.Params {
			if cmd.Flags().Lookup(p.Flag) == nil {
				t.Errorf("op %s %s: flag --%s not registered", op.Domain, op.Name, p.Flag)
			}
		}
		if cmd.Flags().Lookup("session") == nil || cmd.Flags().Lookup("params") == nil {
			t.Errorf("op %s %s: shared --session/--params flags missing", op.Domain, op.Name)
		}
		// 共享文件通道（需求 R-4 问题1）：PS 5.1 剥引号，每个 op 都必须能用
		// --params-file 把 JSON 载荷从文件送进来。
		if cmd.Flags().Lookup("params-file") == nil {
			t.Errorf("op %s %s: shared --params-file flag missing", op.Domain, op.Name)
		}
	}
	for _, leaf := range ops.HandWiredLeaves {
		parts := strings.Split(leaf, " ")
		cmd, _, err := root.Find(parts)
		if err != nil || cmd == nil || cmd.Name() != parts[len(parts)-1] {
			t.Errorf("hand-wired leaf %q not reachable (err=%v)", leaf, err)
		}
	}
}

// leafFor builds the leaf command of one op for collectParams tests.
func leafFor(t *testing.T, domain, name string) (ops.OpSpec, *cobra.Command) {
	t.Helper()
	op, ok := ops.Lookup(domain, name)
	if !ok {
		t.Fatalf("op %s/%s missing", domain, name)
	}
	return op, newOpCommand(op)
}

// TestCollectParamsZeroOmission: optional flags left at their kind's zero
// value stay off the wire; explicit values and non-zero defaults are sent.
func TestCollectParamsZeroOmission(t *testing.T) {
	op, cmd := leafFor(t, "node", "create")
	if err := cmd.Flags().Set("type", "Sprite2D"); err != nil {
		t.Fatal(err)
	}
	params, err := collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["type"] != "Sprite2D" {
		t.Errorf("type = %v", params["type"])
	}
	for _, omitted := range []string{"name", "parent_path", "scene_path", "scene_file"} {
		if _, ok := params[omitted]; ok {
			t.Errorf("zero-value param %q leaked onto the wire: %v", omitted, params)
		}
	}
}

// TestCollectParamsParamsBaseAndOverride: --params supplies the base
// object; explicit flags override colliding keys.
func TestCollectParamsParamsBaseAndOverride(t *testing.T) {
	op, cmd := leafFor(t, "node", "set-property")
	must := func(flag, value string) {
		t.Helper()
		if err := cmd.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	must("params", `{"path":"Root/A","property":"position","value":[0,0],"extra":"kept"}`)
	must("value", `[1,2]`) // flag overrides the colliding --params key
	params, err := collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["extra"] != "kept" {
		t.Errorf("--params base key lost: %v", params)
	}
	arr, ok := params["value"].([]any)
	if !ok || len(arr) != 2 || arr[0] != 1.0 {
		t.Errorf("flag override lost: %v", params["value"])
	}
}

// TestCollectParamsRequiredAndJSON: missing required flags and invalid
// JSON values are errors before anything hits the wire.
func TestCollectParamsRequiredAndJSON(t *testing.T) {
	op, cmd := leafFor(t, "node", "set-property")
	if _, err := collectParams(cmd, op); err == nil {
		t.Error("missing required flags accepted")
	}

	op, cmd = leafFor(t, "node", "set-property")
	for flag, value := range map[string]string{
		"path": "Root", "property": "name", "value": "{not json",
	} {
		if err := cmd.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := collectParams(cmd, op); err == nil {
		t.Error("invalid JSON --value accepted")
	}
}

// TestCollectParamsNodeRef: node set-property --node-ref expands to the
// $node reference encoding as the wire value, satisfies the required
// --value check, and loses to an explicit --value (documented override).
func TestCollectParamsNodeRef(t *testing.T) {
	op, cmd := leafFor(t, "node", "set-property")
	must := func(c *cobra.Command, flag, value string) {
		t.Helper()
		if err := c.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	must(cmd, "path", "/Root/Player")
	must(cmd, "property", "target")
	must(cmd, "node-ref", "../Sprite2D")
	params, err := collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := params["value"].(map[string]any)
	if !ok || ref["$node"] != "../Sprite2D" {
		t.Errorf("value = %v, want {\"$node\":\"../Sprite2D\"}", params["value"])
	}

	// An explicit --value overrides the --node-ref base.
	op, cmd = leafFor(t, "node", "set-property")
	must(cmd, "path", "/Root/Player")
	must(cmd, "property", "target")
	must(cmd, "node-ref", "../Sprite2D")
	must(cmd, "value", `{"$node":"../Other"}`)
	params, err = collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	ref, ok = params["value"].(map[string]any)
	if !ok || ref["$node"] != "../Other" {
		t.Errorf("explicit --value did not win: %v", params["value"])
	}

	// An explicitly empty --node-ref is an input error, not a silent drop.
	op, cmd = leafFor(t, "node", "set-property")
	must(cmd, "path", "/Root/Player")
	must(cmd, "property", "target")
	must(cmd, "node-ref", "")
	if _, err := collectParams(cmd, op); err == nil {
		t.Error("empty --node-ref accepted")
	}
}

// TestCollectParamsNodeRefJSONPassthrough: the raw $node JSON form passes
// --value straight through as a JSON object (no CLI-side interception).
func TestCollectParamsNodeRefJSONPassthrough(t *testing.T) {
	op, cmd := leafFor(t, "node", "set-property")
	for flag, value := range map[string]string{
		"path": "/Root/Player", "property": "target", "value": `{"$node":"../Sprite2D"}`,
	} {
		if err := cmd.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	params, err := collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := params["value"].(map[string]any)
	if !ok || len(ref) != 1 || ref["$node"] != "../Sprite2D" {
		t.Errorf("value = %v, want the $node object verbatim", params["value"])
	}
}

// TestCollectParamsWrapOp: game-domain ops wrap their params into the
// game_command envelope {"op": ..., "params": {...}}.
func TestCollectParamsWrapOp(t *testing.T) {
	op, cmd := leafFor(t, "game", "input-key")
	if err := cmd.Flags().Set("key", "Space"); err != nil {
		t.Fatal(err)
	}
	params, err := collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["op"] != "input_key" {
		t.Fatalf("op = %v", params["op"])
	}
	nested, ok := params["params"].(map[string]any)
	if !ok || nested["key"] != "Space" {
		t.Errorf("nested params = %v", params)
	}
}

// TestCollectParamsNullParams: --params 'null' unmarshals to a nil map;
// collectParams must re-initialize it so the later writes (batch --file,
// non-zero flag defaults) cannot panic.
func TestCollectParamsNullParams(t *testing.T) {
	// batch execute --params null --file: the commands write lands cleanly.
	op, cmd := leafFor(t, "batch", "execute")
	file := filepath.Join(t.TempDir(), "cmds.json")
	if err := os.WriteFile(file, []byte(`[{"command":"editor_state"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("params", "null"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("file", file); err != nil {
		t.Fatal(err)
	}
	params, err := collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	commands, ok := params["commands"].([]any)
	if !ok || len(commands) != 1 {
		t.Errorf("commands = %v", params["commands"])
	}

	// scene get-hierarchy --params null: the non-zero depth default lands.
	op, cmd = leafFor(t, "scene", "get-hierarchy")
	if err := cmd.Flags().Set("params", "null"); err != nil {
		t.Fatal(err)
	}
	params, err = collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["depth"] != 10 {
		t.Errorf("depth = %v, want the non-zero default 10", params["depth"])
	}
}

// TestCollectParamsPropertiesFile: resource create --properties-file 把文件里的
// JSON object 作为 params["properties"] 的基值（需求 resource-create-payload-file：
// PowerShell 5.1 会剥掉 --properties 原生传参里的引号，文件通道绕开 shell）；
// 显式 --properties flag 仍然优先；缺失/非 object/坏 JSON 三种文件错误都必须是
// collectParams 阶段的干净报错，而不是落到 daemon 侧。
func TestCollectParamsPropertiesFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "props.json")
	if err := os.WriteFile(good, []byte(`{"enemy_type":0,"display_name":"普通源石虫"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// 正常路径：文件内容成为 properties 基值。
	op, cmd := leafFor(t, "resource", "create")
	if err := cmd.Flags().Set("type", "EnemyConfig"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("properties-file", good); err != nil {
		t.Fatal(err)
	}
	params, err := collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	props, ok := params["properties"].(map[string]any)
	if !ok || props["enemy_type"] != 0.0 || props["display_name"] != "普通源石虫" {
		t.Errorf("properties = %v", params["properties"])
	}

	// 显式 --properties 优先于文件（与 --params 基值语义一致）。
	op, cmd = leafFor(t, "resource", "create")
	if err := cmd.Flags().Set("type", "EnemyConfig"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("properties-file", good); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("properties", `{"enemy_type":9}`); err != nil {
		t.Fatal(err)
	}
	params, err = collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	props, ok = params["properties"].(map[string]any)
	if !ok || props["enemy_type"] != 9.0 {
		t.Errorf("explicit --properties must win over --properties-file, got %v", params["properties"])
	}

	// 三种文件错误：缺失 / 非 object / 坏 JSON。
	for name, content := range map[string]string{
		"array": `[1,2]`,
		"bad":   `{not json`,
	} {
		bad := filepath.Join(dir, name+".json")
		if err := os.WriteFile(bad, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		op, cmd = leafFor(t, "resource", "create")
		if err := cmd.Flags().Set("type", "EnemyConfig"); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Flags().Set("properties-file", bad); err != nil {
			t.Fatal(err)
		}
		if _, err := collectParams(cmd, op); err == nil {
			t.Errorf("%s content accepted: %s", name, content)
		}
	}
	op, cmd = leafFor(t, "resource", "create")
	if err := cmd.Flags().Set("type", "EnemyConfig"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("properties-file", filepath.Join(dir, "missing.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := collectParams(cmd, op); err == nil {
		t.Error("missing --properties-file accepted")
	}

	// PowerShell 5.1 的 Set-Content -Encoding utf8 会写 UTF-8 BOM——必须容忍。
	bom := filepath.Join(dir, "bom.json")
	if err := os.WriteFile(bom, append([]byte("\xef\xbb\xbf"), []byte(`{"enemy_type":3}`)...), 0o644); err != nil {
		t.Fatal(err)
	}
	op, cmd = leafFor(t, "resource", "create")
	if err := cmd.Flags().Set("type", "EnemyConfig"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("properties-file", bom); err != nil {
		t.Fatal(err)
	}
	params, err = collectParams(cmd, op)
	if err != nil {
		t.Fatalf("BOM-prefixed --properties-file rejected: %v", err)
	}
	if props, ok := params["properties"].(map[string]any); !ok || props["enemy_type"] != 3.0 {
		t.Errorf("BOM-prefixed properties = %v", params["properties"])
	}
}

// TestCollectParamsGlobalParamsFile: 全局 --params-file 把文件里的 JSON object
// 作为最低优先级的基值（需求 R-4 问题1：PS 5.1 会剥掉命令行载荷里的 ASCII 双
// 引号，含引号的 JSON 只能从文件读）；优先级固定为 文件 → --params → 显式 flag。
// 缺失/非 object/坏 JSON/JSON null 四种输入都必须在 collectParams 阶段报错，且
// 报文带 --params-file 前缀。
func TestCollectParamsGlobalParamsFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	// 基值：只给文件，三个键全部落到 wire params（含带 ASCII 双引号的字符串）。
	base := write("base.json", `{"path":"FromFile","property":"modulate","value":"\"quoted\"","scene_file":"guard.tscn"}`)
	op, cmd := leafFor(t, "node", "set-property")
	if err := cmd.Flags().Set("params-file", base); err != nil {
		t.Fatal(err)
	}
	params, err := collectParams(cmd, op)
	if err != nil {
		t.Fatalf("--params-file payload rejected: %v", err)
	}
	if params["path"] != "FromFile" || params["property"] != "modulate" || params["scene_file"] != "guard.tscn" {
		t.Errorf("file base params = %v", params)
	}
	if params["value"] != `"quoted"` {
		t.Errorf("value = %v, want the quoted JSON string from the file", params["value"])
	}

	// 优先级：--params 逐键覆盖文件基值，显式 flag 再覆盖两者。
	op, cmd = leafFor(t, "node", "set-property")
	if err := cmd.Flags().Set("params-file", base); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("params", `{"path":"FromParams"}`); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("value", `"FromFlag"`); err != nil {
		t.Fatal(err)
	}
	params, err = collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["path"] != "FromParams" {
		t.Errorf("path = %v, want --params to override the file base", params["path"])
	}
	if params["value"] != "FromFlag" {
		t.Errorf("value = %v, want the explicit --value flag to win", params["value"])
	}
	if params["property"] != "modulate" {
		t.Errorf("property = %v, want the file-only key to survive the merge", params["property"])
	}

	// BOM：PS 5.1 的 Set-Content -Encoding utf8 会写 UTF-8 BOM，必须容忍。
	bom := write("bom.json", "\xef\xbb\xbf"+`{"path":"FromBomFile","property":"modulate","value":1}`)
	op, cmd = leafFor(t, "node", "set-property")
	if err := cmd.Flags().Set("params-file", bom); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("property", "modulate"); err != nil {
		t.Fatal(err)
	}
	params, err = collectParams(cmd, op)
	if err != nil {
		t.Fatalf("BOM-prefixed --params-file rejected: %v", err)
	}
	if params["path"] != "FromBomFile" {
		t.Errorf("BOM-prefixed params = %v", params)
	}

	// 四种坏输入：文件里的顶层必须是 JSON object。
	for name, content := range map[string]string{
		"array.json":  `[1,2]`,
		"scalar.json": `"just a string"`,
		"null.json":   `null`,
		"broken.json": `{not json`,
	} {
		op, cmd = leafFor(t, "node", "set-property")
		if err := cmd.Flags().Set("params-file", write(name, content)); err != nil {
			t.Fatal(err)
		}
		if _, err := collectParams(cmd, op); err == nil {
			t.Errorf("--params-file accepted %s content: %s", name, content)
		} else if !strings.Contains(err.Error(), "--params-file") {
			t.Errorf("error for %s lacks the --params-file prefix: %v", name, err)
		}
	}
	op, cmd = leafFor(t, "node", "set-property")
	if err := cmd.Flags().Set("params-file", filepath.Join(dir, "missing.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := collectParams(cmd, op); err == nil {
		t.Error("missing --params-file accepted")
	} else if !strings.Contains(err.Error(), "--params-file") {
		t.Errorf("missing-file error lacks the --params-file prefix: %v", err)
	}
}

// TestCollectParamsNodeValueFile: node set-property --value-file（需求 R-2）把
// 文件内容当作 value 的 JSON 基值（任意 JSON 类型），显式 --value 优先；
// --node-ref 比文件更具体，同时给出时由它覆盖。缺文件/非 JSON 都要干净报错。
func TestCollectParamsNodeValueFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	// object 值：文件里的 {"x":1,"y":2} 成为 value（PS 5.1 下命令行传不了）。
	valueFile := write("value.json", `{"x":1,"y":2}`)
	op, cmd := leafFor(t, "node", "set-property")
	if err := cmd.Flags().Set("path", "Sprite"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("property", "modulate"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("value-file", valueFile); err != nil {
		t.Fatal(err)
	}
	params, err := collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	obj, ok := params["value"].(map[string]any)
	if !ok || obj["x"] != 1.0 || obj["y"] != 2.0 {
		t.Errorf("value from file = %v", params["value"])
	}

	// 带 ASCII 双引号的字符串值原样落地，不被 shell 剥引号影响。
	quoted := write("quoted.json", `"a\"b"`)
	op, cmd = leafFor(t, "node", "set-property")
	if err := cmd.Flags().Set("path", "Sprite"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("property", "name"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("value-file", quoted); err != nil {
		t.Fatal(err)
	}
	params, err = collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["value"] != `a"b` {
		t.Errorf("quoted value from file = %v, want a\"b", params["value"])
	}

	// 显式 --value 优先于文件基值。
	op, cmd = leafFor(t, "node", "set-property")
	if err := cmd.Flags().Set("path", "Sprite"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("property", "modulate"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("value-file", valueFile); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("value", `{"x":9}`); err != nil {
		t.Fatal(err)
	}
	params, err = collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if obj, ok := params["value"].(map[string]any); !ok || obj["x"] != 9.0 {
		t.Errorf("explicit --value must win over --value-file, got %v", params["value"])
	}

	// --node-ref 是更具体的简写：同时给出时覆盖文件基值。
	op, cmd = leafFor(t, "node", "set-property")
	if err := cmd.Flags().Set("path", "Sprite"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("property", "target"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("value-file", valueFile); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("node-ref", "../Sprite2D"); err != nil {
		t.Fatal(err)
	}
	params, err = collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if ref, ok := params["value"].(map[string]any); !ok || ref["$node"] != "../Sprite2D" {
		t.Errorf("--node-ref must win over --value-file, got %v", params["value"])
	}

	// 缺文件 / 非 JSON 文件都必须报错且带 flag 前缀。
	op, cmd = leafFor(t, "node", "set-property")
	if err := cmd.Flags().Set("path", "Sprite"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("property", "modulate"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("value-file", filepath.Join(dir, "missing.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := collectParams(cmd, op); err == nil {
		t.Error("missing --value-file accepted")
	} else if !strings.Contains(err.Error(), "--value-file") {
		t.Errorf("missing-file error lacks the --value-file prefix: %v", err)
	}

	op, cmd = leafFor(t, "node", "set-property")
	if err := cmd.Flags().Set("path", "Sprite"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("property", "modulate"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("value-file", write("raw.txt", `{x:1}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := collectParams(cmd, op); err == nil {
		t.Error("non-JSON --value-file accepted")
	}
}

// TestCollectParamsPatchTextFiles: script patch --old-file/--new-file（需求
// R-4 问题1）文本直读——ASCII 双引号、反斜杠、换行原样保留（不做 JSON 解析），
// 显式 --old-text/--new-text 优先，且只给文件时必需的 old_text/new_text 检查
// 必须通过（这正是 PS 5.1 下的可用形态）。
func TestCollectParamsPatchTextFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	// 含 ASCII 双引号与制表符/换行的锚点：逐字符相等，不做 JSON 解析。
	oldText := "\tif result.has(\"ok\"):\n\t\tprint(\"ok\")\n"
	oldFile := write("old.txt", oldText)
	newFile := write("new.txt", "\tif result.has(\"ok\") and result[\"ok\"]:\n\t\tprint(\"ok\")\n")

	op, cmd := leafFor(t, "script", "patch")
	if err := cmd.Flags().Set("path", "res://a.gd"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("old-file", oldFile); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("new-file", newFile); err != nil {
		t.Fatal(err)
	}
	params, err := collectParams(cmd, op)
	if err != nil {
		t.Fatalf("--old-file/--new-file must satisfy the required text params: %v", err)
	}
	if params["old_text"] != oldText {
		t.Errorf("old_text = %q, want %q", params["old_text"], oldText)
	}
	if params["new_text"] != "\tif result.has(\"ok\") and result[\"ok\"]:\n\t\tprint(\"ok\")\n" {
		t.Errorf("new_text = %q", params["new_text"])
	}

	// BOM 剥除：PS 5.1 写文件会带 BOM，留在锚点里永远匹配不上。
	op, cmd = leafFor(t, "script", "patch")
	if err := cmd.Flags().Set("path", "res://a.gd"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("old-file", write("bom.txt", "\xef\xbb\xbf"+`print("hi")`)); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("new-text", "pass"); err != nil {
		t.Fatal(err)
	}
	params, err = collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["old_text"] != `print("hi")` {
		t.Errorf("BOM-prefixed old_text = %q", params["old_text"])
	}

	// 显式 flag 优先于文件通道（两侧各自独立）。
	op, cmd = leafFor(t, "script", "patch")
	if err := cmd.Flags().Set("path", "res://a.gd"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("old-file", oldFile); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("new-file", newFile); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("old-text", "explicit old"); err != nil {
		t.Fatal(err)
	}
	params, err = collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["old_text"] != "explicit old" {
		t.Errorf("explicit --old-text must win over --old-file, got %q", params["old_text"])
	}
	if params["new_text"] != "\tif result.has(\"ok\") and result[\"ok\"]:\n\t\tprint(\"ok\")\n" {
		t.Errorf("--new-file must still apply when only --old-text is explicit, got %q", params["new_text"])
	}

	// 两侧都缺 → 仍然是「缺必需 flag」的干净报错（不因新通道而放行）。
	op, cmd = leafFor(t, "script", "patch")
	if err := cmd.Flags().Set("path", "res://a.gd"); err != nil {
		t.Fatal(err)
	}
	if _, err := collectParams(cmd, op); err == nil {
		t.Error("patch without either text channel accepted")
	}

	// 缺文件 → 带 --old-file 前缀的报错。
	op, cmd = leafFor(t, "script", "patch")
	if err := cmd.Flags().Set("path", "res://a.gd"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("old-file", filepath.Join(dir, "missing.txt")); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("new-text", "pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := collectParams(cmd, op); err == nil {
		t.Error("missing --old-file accepted")
	} else if !strings.Contains(err.Error(), "--old-file") {
		t.Errorf("missing-file error lacks the --old-file prefix: %v", err)
	}
}

// TestOpExamplesParamsDemo: ops without required params get a second
// example line teaching the --params JSON form with one real optional key;
// ops with required params keep the single flag-form line; ops without any
// string/int param stay bare (a made-up key would teach the wrong thing).
func TestOpExamplesParamsDemo(t *testing.T) {
	// node create: no required params, first string param "type" carries a
	// "(default: Node)" usage hint → demo uses it.
	op, ok := ops.Lookup("node", "create")
	if !ok {
		t.Fatal("node create missing")
	}
	got := opExamples(op)
	want := "godot-ai-cli node create\n  godot-ai-cli node create --params '{\"type\":\"Node\"}'"
	if got != want {
		t.Errorf("node create examples = %q, want %q", got, want)
	}

	// scene get-hierarchy: int param with a non-zero default → typed value.
	op, _ = ops.Lookup("scene", "get-hierarchy")
	got = opExamples(op)
	want = "godot-ai-cli scene get-hierarchy\n  godot-ai-cli scene get-hierarchy --params '{\"depth\":10}'"
	if got != want {
		t.Errorf("scene get-hierarchy examples = %q, want %q", got, want)
	}

	// editor state: no params at all → bare call only.
	op, _ = ops.Lookup("editor", "state")
	if got := opExamples(op); got != "godot-ai-cli editor state" {
		t.Errorf("editor state examples = %q", got)
	}

	// editor monitors: its only param is JSON kind → bare call only.
	op, _ = ops.Lookup("editor", "monitors")
	if got := opExamples(op); got != "godot-ai-cli editor monitors" {
		t.Errorf("editor monitors examples = %q", got)
	}

	// node set-property: required params → unchanged single flag-form line.
	op, _ = ops.Lookup("node", "set-property")
	got = opExamples(op)
	want = "godot-ai-cli node set-property --path <string> --property <string> --value '<json>'"
	if got != want {
		t.Errorf("node set-property examples = %q, want %q", got, want)
	}
}

// TestOpExamplesParamsDemoKeysAreReal is the table-wide invariant: every
// generated --params demo line parses as JSON and carries exactly one key
// that is an optional string/int wire param of that op.
func TestOpExamplesParamsDemoKeysAreReal(t *testing.T) {
	for _, op := range ops.All() {
		ex := opExamples(op)
		idx := strings.Index(ex, " --params '")
		if idx < 0 {
			continue
		}
		raw := strings.TrimSuffix(ex[idx+len(" --params '"):], "'")
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Errorf("op %s %s: demo --params is not JSON: %q", op.Domain, op.Name, raw)
			continue
		}
		if len(m) != 1 {
			t.Errorf("op %s %s: demo carries %d keys, want 1", op.Domain, op.Name, len(m))
			continue
		}
		for key := range m {
			real := false
			for _, p := range op.Params {
				if p.Param == key && !p.Required && (p.Kind == ops.KindString || p.Kind == ops.KindInt) {
					real = true
				}
			}
			if !real {
				t.Errorf("op %s %s: demo key %q is not an optional string/int param", op.Domain, op.Name, key)
			}
		}
	}
}

// TestExecuteOpDaemonDown: ops against a dead daemon get the standard
// not-running envelope with the launch hint.
func TestExecuteOpDaemonDown(t *testing.T) {
	// Find a port nothing listens on.
	ln := listenFree(t)
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"editor", "state", "--http-port", itoa(port)})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a non-nil error against a dead daemon")
	}
	if !strings.Contains(buf.String(), "DAEMON_NOT_RUNNING") ||
		!strings.Contains(buf.String(), "godot-ai-cli launch") {
		t.Errorf("output missing the not-running envelope or launch hint:\n%s", buf.String())
	}
}

// TestExecuteOpRoundTrip drives the full path against a real daemon with a
// mock plugin: flags become wire params, write gating sets write:true, and
// the data payload is printed.
func TestExecuteOpRoundTrip(t *testing.T) {
	d, err := daemon.Start(context.Background(), daemon.Config{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	plugin := mockplugin.Dial(t, d.Bridge().Addr(), d.Bridge().WSCapability, nil)
	plugin.SetResponder(func(command string, params map[string]any) *mockplugin.Response {
		return &mockplugin.Response{Data: map[string]any{"echo": params["property"]}}
	})

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{
		"node", "set-property", "--http-port", itoa(d.HTTPPort()),
		"--path", "Root", "--property", "name", "--value", `"Hero"`,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), `"echo":"name"`) {
		t.Errorf("data payload not printed:\n%s", buf.String())
	}

	got := plugin.Received()
	if len(got) != 1 || got[0].Command != "set_property" {
		t.Fatalf("mock received %v", got)
	}
	params := got[0].Params
	if params["path"] != "Root" || params["property"] != "name" || params["value"] != "Hero" {
		t.Errorf("wire params = %v", params)
	}
}

// TestCallEscapeHatch: `call` forwards an arbitrary command with raw
// params, and plugin errors surface as the daemon's envelope + exit 1.
func TestCallEscapeHatch(t *testing.T) {
	d, err := daemon.Start(context.Background(), daemon.Config{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	plugin := mockplugin.Dial(t, d.Bridge().Addr(), d.Bridge().WSCapability, nil)
	plugin.SetResponder(func(command string, params map[string]any) *mockplugin.Response {
		return &mockplugin.Response{
			Status: "error",
			Error:  map[string]any{"code": "UNKNOWN_COMMAND", "message": "nope", "data": map[string]any{"retryable": false}},
		}
	})

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"call", "whatever", "--http-port", itoa(d.HTTPPort())})
	err = cmd.Execute()
	if err == nil {
		t.Fatal("expected the plugin error to exit non-zero")
	}
	if !strings.Contains(buf.String(), "UNKNOWN_COMMAND") {
		t.Errorf("error envelope not printed:\n%s", buf.String())
	}
	if got := plugin.Received(); len(got) != 1 || got[0].Command != "whatever" {
		t.Errorf("mock received %v", got)
	}
}

// TestCustomListRoundTrip: the catalog the plugin pushes via
// custom_tools_changed events is served back by `custom list`.
func TestCustomListRoundTrip(t *testing.T) {
	d, err := daemon.Start(context.Background(), daemon.Config{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	plugin := mockplugin.Dial(t, d.Bridge().Addr(), d.Bridge().WSCapability, nil)
	plugin.PushEvent("custom_tools_changed", map[string]any{
		"tools": []any{map[string]any{"name": "my_tool", "enabled": true}},
	})
	// The event is processed asynchronously on the WS read loop.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if tools, ok := d.Bridge().CustomTools(""); ok && len(tools) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("custom tool catalog never cached")
		}
		time.Sleep(20 * time.Millisecond)
	}

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"custom", "list", "--http-port", itoa(d.HTTPPort())})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("custom list: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "my_tool") {
		t.Errorf("catalog not printed:\n%s", buf.String())
	}
}

// TestBoolFlagValueCompat: the space-separated bool form `--pressed false`
// is rewritten to the flag's value and lands on the wire, while stray
// positionals fail with a steering error naming the --flag=false form.
func TestBoolFlagValueCompat(t *testing.T) {
	d, err := daemon.Start(context.Background(), daemon.Config{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	plugin := mockplugin.Dial(t, d.Bridge().Addr(), d.Bridge().WSCapability, nil)
	plugin.SetResponder(func(command string, params map[string]any) *mockplugin.Response {
		return &mockplugin.Response{Data: map[string]any{"ok": true}}
	})

	// `--pressed false` → wire pressed=false (wrapped under game_command).
	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{
		"game", "input-action", "--http-port", itoa(d.HTTPPort()),
		"--action", "move_right", "--pressed", "false",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("--pressed false rejected: %v\n%s", err, buf.String())
	}
	got := plugin.Received()
	if len(got) != 1 || got[0].Command != "game_command" {
		t.Fatalf("mock received %v", got)
	}
	nested, ok := got[0].Params["params"].(map[string]any)
	if !ok || nested["pressed"] != false {
		t.Errorf("wire params = %v, want nested pressed=false", got[0].Params)
	}

	// A stray non-bool positional gets the steering error (no daemon needed).
	cmd = NewRootCommand()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"game", "input-action", "--action", "move_right", "--pressed", "junk"})
	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--pressed=false") {
		t.Errorf("stray positional error does not steer to --pressed=false: %v", err)
	}

	// Two bool flags explicitly set: ambiguous, no conversion — plain error.
	cmd = NewRootCommand()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"game", "input-key", "--key", "D", "--pressed", "--echo", "false"})
	if err = cmd.Execute(); err == nil {
		t.Error("ambiguous bool positional accepted (two bool flags changed)")
	}

	// Bare `--pressed` alone keeps working (no positional at all).
	op, leaf := leafFor(t, "game", "input-action")
	if err := leaf.Flags().Set("action", "move_right"); err != nil {
		t.Fatal(err)
	}
	if err := leaf.Flags().Set("pressed", "true"); err != nil {
		t.Fatal(err)
	}
	if err := boolFlagValueArgs(op)(leaf, nil); err != nil {
		t.Errorf("no positional args must pass: %v", err)
	}
}

// TestCollectParamsFilterFlags: the new filter/echo flags land on the wire
// when set and stay off it at their zero values.
func TestCollectParamsFilterFlags(t *testing.T) {
	must := func(cmd *cobra.Command, flag, value string) {
		t.Helper()
		if err := cmd.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}

	// logs read --level/--grep/--tail pass through…
	op, cmd := leafFor(t, "logs", "read")
	must(cmd, "level", "error")
	must(cmd, "grep", "kaboom")
	must(cmd, "tail", "20")
	params, err := collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["level"] != "error" || params["grep"] != "kaboom" || params["tail"] != 20 {
		t.Errorf("logs read filter params = %v", params)
	}

	// …and stay off the wire at their zero values (declared non-zero
	// defaults like count/source are still sent, as before).
	op, cmd = leafFor(t, "logs", "read")
	params, err = collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	for _, omitted := range []string{"level", "grep", "tail", "since_run_id", "since_cursor"} {
		if _, ok := params[omitted]; ok {
			t.Errorf("zero-value param %q leaked onto the wire: %v", omitted, params)
		}
	}
	if params["count"] != 50 || params["source"] != "plugin" {
		t.Errorf("declared defaults lost: %v", params)
	}

	// editor eval --echo-prints → echo_prints:true; default off → omitted.
	op, cmd = leafFor(t, "editor", "eval")
	must(cmd, "code", "print(1+1)")
	must(cmd, "echo-prints", "true")
	params, err = collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["echo_prints"] != true {
		t.Errorf("echo_prints = %v", params)
	}
	op, cmd = leafFor(t, "editor", "eval")
	must(cmd, "code", "print(1+1)")
	params, err = collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := params["echo_prints"]; ok {
		t.Errorf("echo_prints leaked at default: %v", params)
	}

	// input-map list --action glob.
	op, cmd = leafFor(t, "input-map", "list")
	must(cmd, "action", "move_*")
	params, err = collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["action"] != "move_*" {
		t.Errorf("input-map list action = %v", params)
	}

	// game get-scene-tree --name is wrapped under game_command params.
	op, cmd = leafFor(t, "game", "get-scene-tree")
	must(cmd, "name", "Player*")
	params, err = collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	nested, ok := params["params"].(map[string]any)
	if !ok || nested["name"] != "Player*" || nested["depth"] != 10 {
		t.Errorf("get-scene-tree wire params = %v", params)
	}

	// game get-node-info --fields is a JSON array param.
	op, cmd = leafFor(t, "game", "get-node-info")
	must(cmd, "path", "Player")
	must(cmd, "fields", `["position","visible"]`)
	params, err = collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	nested, ok = params["params"].(map[string]any)
	fields, ok2 := nested["fields"].([]any)
	if !ok || !ok2 || len(fields) != 2 || fields[0] != "position" {
		t.Errorf("get-node-info wire params = %v", params)
	}
}

// TestCollectParamsSpriteframesOps: the three spriteframes ops serialize
// their flags onto the wire (defaults for fps/speed/loop included).
func TestCollectParamsSpriteframesOps(t *testing.T) {
	must := func(cmd *cobra.Command, flag, value string) {
		t.Helper()
		if err := cmd.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}

	op, cmd := leafFor(t, "resource", "spriteframes-from-sheet")
	must(cmd, "resource-path", "res://assets/hero.tres")
	must(cmd, "texture", "res://assets/hero.png")
	must(cmd, "cell", "32x32")
	must(cmd, "rows", "0:idle,1:walk")
	params, err := collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["cell"] != "32x32" || params["rows"] != "0:idle,1:walk" ||
		params["fps"] != 8.0 || params["loop"] != true {
		t.Errorf("spriteframes-from-sheet params = %v", params)
	}

	op, cmd = leafFor(t, "resource", "spriteframes-add-animation")
	must(cmd, "resource-path", "res://assets/hero.tres")
	must(cmd, "name", "attack")
	params, err = collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["name"] != "attack" || params["speed"] != 5.0 || params["loop"] != true {
		t.Errorf("spriteframes-add-animation params = %v", params)
	}

	op, cmd = leafFor(t, "resource", "spriteframes-add-frame")
	must(cmd, "resource-path", "res://assets/hero.tres")
	must(cmd, "anim", "idle")
	must(cmd, "texture", "res://assets/hero.png")
	must(cmd, "region", "0,32,32,32")
	params, err = collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["region"] != "0,32,32,32" {
		t.Errorf("spriteframes-add-frame params = %v", params)
	}
	if _, ok := params["at_index"]; ok {
		t.Errorf("at_index leaked at default: %v", params)
	}

	// Required flags are enforced before anything hits the wire.
	op, cmd = leafFor(t, "resource", "spriteframes-from-sheet")
	must(cmd, "texture", "res://assets/hero.png")
	if _, err = collectParams(cmd, op); err == nil {
		t.Error("missing required flags accepted")
	}
}

// contentFileHarness 起一个 mock daemon + 插件，返回「执行一条 CLI 命令」的
// 闭包与插件侧收到的请求。--content-file 的验收要走真实命令路径（标志注册、
// collectParams、回包里的 source_bytes/source_sha256 三处都要覆盖），所以
// 不能只调 collectParams。
func contentFileHarness(t *testing.T) (func(args ...string) (string, error), *mockplugin.Plugin) {
	t.Helper()
	d, err := daemon.Start(context.Background(), daemon.Config{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	plugin := mockplugin.Dial(t, d.Bridge().Addr(), d.Bridge().WSCapability, nil)
	plugin.SetResponder(func(string, map[string]any) *mockplugin.Response {
		return &mockplugin.Response{Data: map[string]any{"echo": true}}
	})

	run := func(args ...string) (string, error) {
		cmd := NewRootCommand()
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs(append(args, "--http-port", itoa(d.HTTPPort())))
		err := cmd.Execute()
		return buf.String(), err
	}
	return run, plugin
}

// TestContentFileChannel：script create / filesystem write-text 的 --content-file
// （需求 R-1）——CLI 侧读 UTF-8 文件、剥掉 BOM、作为 content 下发；成功回包
// 附 source_bytes 与 source_sha256（hex），证明「写进去的就是我准备的内容」。
//
// 载荷刻意用「BOM + 多行 + 中文注释 + 中文字符串常量 + ASCII 双引号」：
// PS 5.1 正是把这些东西从命令行吃掉/改编码的。
func TestContentFileChannel(t *testing.T) {
	dir := t.TempDir()
	source := "; 中文注释：生命值\nconst GREETING = \"游戏结束\"\n\nfunc _ready() -> void:\n\tprint(GREETING)\n"
	file := filepath.Join(dir, "hud.gd")
	if err := os.WriteFile(file, append([]byte("\ufeff"), []byte(source)...), 0o644); err != nil {
		t.Fatal(err)
	}

	run, plugin := contentFileHarness(t)
	out, err := run("script", "create", "--path", "res://scene/ui/hud.gd", "--content-file", file)
	if err != nil {
		t.Fatalf("script create --content-file: %v\n%s", err, out)
	}

	got := plugin.Received()
	if len(got) != 1 || got[0].Command != "create_script" {
		t.Fatalf("mock received %v", got)
	}
	if content, _ := got[0].Params["content"].(string); content != source {
		t.Errorf("wire content = %q, want the BOM-stripped file verbatim", content)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	sum := sha256.Sum256([]byte(source))
	if payload["source_bytes"] != float64(len(source)) {
		t.Errorf("source_bytes = %v, want %d", payload["source_bytes"], len(source))
	}
	if payload["source_sha256"] != fmt.Sprintf("%x", sum) {
		t.Errorf("source_sha256 = %v, want %x", payload["source_sha256"], sum)
	}

	// filesystem write-text 走同一条通道。
	plain := "；纯文本\nline2\n"
	plainFile := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(plainFile, []byte(plain), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = run("filesystem", "write-text", "--path", "res://notes.txt", "--content-file", plainFile)
	if err != nil {
		t.Fatalf("filesystem write-text --content-file: %v\n%s", err, out)
	}
	got = plugin.Received()
	last := got[len(got)-1]
	if last.Command != "write_file" || last.Params["content"] != plain {
		t.Errorf("write_file params = %v", last.Params)
	}
	sum = sha256.Sum256([]byte(plain))
	if !strings.Contains(out, fmt.Sprintf("%x", sum)) {
		t.Errorf("write-text response lacks the source_sha256 of the file:\n%s", out)
	}

	// 空文件是合法输入（写一个空文件），不是「没有内容通道」。
	emptyFile := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(emptyFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = run("filesystem", "write-text", "--path", "res://empty.txt", "--content-file", emptyFile)
	if err != nil {
		t.Fatalf("empty --content-file: %v\n%s", err, out)
	}
	got = plugin.Received()
	if content, ok := got[len(got)-1].Params["content"].(string); !ok || content != "" {
		t.Errorf("empty file must travel as an empty content string, got %#v", got[len(got)-1].Params["content"])
	}
}

// TestContentFileErrors：--content 与 --content-file 互斥（INVALID_PARAMS）、
// 文件读不到要带 flag 前缀报错，且两种情况都不得把命令发到 daemon。
func TestContentFileErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "hud.gd")
	if err := os.WriteFile(file, []byte("pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run, plugin := contentFileHarness(t)
	out, err := run("script", "create", "--path", "res://hud.gd", "--content", "inline", "--content-file", file)
	if err == nil {
		t.Fatal("--content with --content-file was accepted")
	}
	if !strings.Contains(out, "INVALID_PARAMS") || !strings.Contains(out, "mutually exclusive") {
		t.Errorf("conflict envelope missing INVALID_PARAMS/mutually exclusive:\n%s", out)
	}
	out, err = run("filesystem", "write-text", "--path", "res://hud.gd", "--content-file", filepath.Join(dir, "missing.gd"))
	if err == nil {
		t.Fatal("missing --content-file was accepted")
	}
	if !strings.Contains(out, "INVALID_PARAMS") || !strings.Contains(out, "--content-file") {
		t.Errorf("missing-file envelope must name the channel:\n%s", out)
	}
	if n := len(plugin.Received()); n != 0 {
		t.Errorf("rejected invocations still reached the daemon (%d commands)", n)
	}
}

// TestContentFileBeatsParamsBase：--content-file 是显式内容通道，覆盖
// --params/--params-file 里的 content 基值（与 --value-file 等既有文件通道
// 同一条优先级规则）。
func TestContentFileBeatsParamsBase(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "hud.gd")
	if err := os.WriteFile(file, []byte("from-file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run, plugin := contentFileHarness(t)
	out, err := run("script", "create", "--path", "res://hud.gd",
		"--params", `{"content":"from-params","extra":"kept"}`, "--content-file", file)
	if err != nil {
		t.Fatalf("--params + --content-file: %v\n%s", err, out)
	}
	got := plugin.Received()
	if len(got) != 1 {
		t.Fatalf("mock received %v", got)
	}
	if got[0].Params["content"] != "from-file\n" {
		t.Errorf("content = %v, want the file to win over the --params base", got[0].Params["content"])
	}
	if got[0].Params["extra"] != "kept" {
		t.Errorf("--params base keys must survive: %v", got[0].Params)
	}

	// 不给 --content-file 时行为不变：--params 的 content 照旧下发。
	run, plugin = contentFileHarness(t)
	if _, err := run("script", "create", "--path", "res://hud.gd", "--params", `{"content":"from-params"}`); err != nil {
		t.Fatal(err)
	}
	got = plugin.Received()
	if got[0].Params["content"] != "from-params" {
		t.Errorf("plain --params content changed: %v", got[0].Params["content"])
	}
}

// opHelpOutput 渲染一条 op 命令的 --help 文本（cobra 把它写进 OutOrStdout）。
func opHelpOutput(t *testing.T, args ...string) string {
	t.Helper()
	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append(args, "--help"))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("godot-ai-cli %v --help: %v\n%s", args, err, buf.String())
	}
	return buf.String()
}

// TestOpHelpNoteSection：需求 R-4① 的原文要求是「在 game input-action --help 里加
// 一句」——HelpNote 是只进 -h 的短段，必须真的出现在 per-command help 里，且不得
// 把整段（很长的）DocNote 灌进 help。没有 HelpNote 的 op 输出里不许冒出 Note 段。
func TestOpHelpNoteSection(t *testing.T) {
	op, ok := ops.Lookup("game", "input-action")
	if !ok {
		t.Fatal("op game input-action missing")
	}
	if op.HelpNote == "" {
		t.Fatal("game input-action carries no HelpNote — the GUI hint would stay invisible in --help")
	}

	help := opHelpOutput(t, "game", "input-action")
	for _, want := range []string{
		"Note:",
		"Input.is_action_*",
		"game input-key",
		"game input-mouse",
		"game ui-activate",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("game input-action --help missing %q:\n%s", want, help)
		}
	}
	if n := strings.Count(help, "Note:"); n != 1 {
		t.Errorf("game input-action --help has %d Note: sections, want exactly 1:\n%s", n, help)
	}
	if op.DocNote != "" && strings.Contains(help, op.DocNote) {
		t.Error("game input-action --help renders the whole DocNote; HelpNote must stay the short -h-only variant")
	}

	// 没有 HelpNote 的 op：help 里不许新增 Note 段（其余 op 的输出保持原样）。
	if plain := opHelpOutput(t, "node", "create"); strings.Contains(plain, "Note:") {
		t.Errorf("node create --help grew a Note: section:\n%s", plain)
	}
	// 既有 HelpNote 宿主（editor eval / script patch）仍然渲染：渲染入口没被改坏。
	for _, args := range [][]string{{"editor", "eval"}, {"script", "patch"}} {
		host, ok := ops.Lookup(args[0], args[1])
		if !ok || host.HelpNote == "" {
			t.Fatalf("op %s %s lost its HelpNote", args[0], args[1])
		}
		if out := opHelpOutput(t, args...); !strings.Contains(out, host.HelpNote) {
			t.Errorf("%s %s --help no longer renders its HelpNote:\n%s", args[0], args[1], out)
		}
	}
}

// TestHelpNoteStaysOutOfCommandsMarkdown：HelpNote 只服务 per-command --help——
// commands.md 的生成器不消费它，doc 闸门（check-doc-coverage.sh 的 4b 逐字节
// 比对）因此不受影响；DocNote 反过来必须继续进 catalog。
func TestHelpNoteStaysOutOfCommandsMarkdown(t *testing.T) {
	op, ok := ops.Lookup("game", "input-action")
	if !ok || op.HelpNote == "" {
		t.Fatal("game input-action HelpNote missing")
	}
	md := runCommands(t, "--format", "md")
	if strings.Contains(md, op.HelpNote) {
		t.Error("commands --format md rendered the HelpNote — it must stay -h-only")
	}
	if op.DocNote != "" && !strings.Contains(md, op.DocNote) {
		t.Error("commands --format md lost the DocNote prose of game input-action")
	}
}
