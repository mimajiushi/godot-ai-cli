package ops_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mimajiushi/godot-ai-cli/internal/ops"
)

// TestUpstreamV441CommandMappings 逐项钉住本轮新增的 31 个 CLI 叶子，
// 防止仅靠集合 parity 掩盖 CLI 名称或 wire 命令映射错误。
func TestUpstreamV441CommandMappings(t *testing.T) {
	want := map[string]string{
		"scene close":                      "close_scene",
		"node set-editable-children":       "set_editable_children",
		"filesystem import-get":            "import_get",
		"filesystem import-set":            "import_set",
		"resource create-sprite-frames":    "create_sprite_frames",
		"resource set-properties":          "set_resource_properties",
		"animation tree-point-add":         "animation_tree_point_add",
		"animation tree-point-remove":      "animation_tree_point_remove",
		"animation tree-point-set":         "animation_tree_point_set",
		"animation tree-create":            "animation_tree_create",
		"animation tree-get":               "animation_tree_get",
		"animation tree-node-add":          "animation_tree_node_add",
		"animation tree-node-remove":       "animation_tree_node_remove",
		"animation tree-node-set":          "animation_tree_node_set",
		"animation tree-connect":           "animation_tree_connect",
		"animation tree-disconnect":        "animation_tree_disconnect",
		"animation tree-transition-add":    "animation_tree_transition_add",
		"animation tree-transition-remove": "animation_tree_transition_remove",
		"animation tree-transition-set":    "animation_tree_transition_set",
		"animation library-add":            "animation_library_add",
		"animation library-remove":         "animation_library_remove",
		"animation library-list":           "animation_library_list",
		"audio bus-list":                   "audio_bus_list",
		"audio bus-add":                    "audio_bus_add",
		"audio bus-remove":                 "audio_bus_remove",
		"audio bus-set":                    "audio_bus_set",
		"audio bus-effect-add":             "audio_bus_effect_add",
		"audio bus-effect-set":             "audio_bus_effect_set",
		"audio bus-effect-remove":          "audio_bus_effect_remove",
		"audio bus-layout-save":            "audio_bus_layout_save",
		"tileset create-atlas":             "tileset_create_atlas",
	}
	if len(want) != 31 {
		t.Fatalf("测试表自身应有 31 项，实际 %d", len(want))
	}
	for key, command := range want {
		domain, name, found := strings.Cut(key, " ")
		if !found {
			t.Fatalf("非法测试键 %q", key)
		}
		op, ok := ops.Lookup(domain, name)
		if !ok {
			t.Errorf("缺少新 CLI op %s", key)
			continue
		}
		if op.PluginCommand != command {
			t.Errorf("%s 的 wire 命令 = %q，want %q", key, op.PluginCommand, command)
		}
	}
	if got := len(ops.All()); got != 217 {
		t.Errorf("ops 总数 = %d，want 217", got)
	}
	if got := len(ops.InternalOnly); got != 3 {
		t.Errorf("InternalOnly = %d，want 3（本轮不得新增）", got)
	}
}

// TestForkOnlyOpsRemainExposed 保留 fork 的 11 个私有 CLI op；新上游命令
// 与旧 spriteframes_* 系列契约不同，不能互相替换或去重。
func TestForkOnlyOpsRemainExposed(t *testing.T) {
	for _, key := range []string{
		"node reset-property",
		"game input-warp",
		"game get-mouse",
		"game node-screen-rect",
		"game ui-activate",
		"resource set-property",
		"resource spriteframes-add-animation",
		"resource spriteframes-add-frame",
		"resource spriteframes-from-sheet",
		"resource spriteframes-list-frames",
		"resource spriteframes-swap-frames",
	} {
		domain, name, _ := strings.Cut(key, " ")
		if _, ok := ops.Lookup(domain, name); !ok {
			t.Errorf("fork 私有 op 丢失：%s", key)
		}
	}
}

// TestImportTimeoutBudget 同步 native import 的等待预算为 120 秒；
// 文案必须明确 timeout 不会取消已开始的原生导入。
func TestImportTimeoutBudget(t *testing.T) {
	for _, name := range []string{"import-set", "reimport"} {
		op, ok := ops.Lookup("filesystem", name)
		if !ok {
			t.Fatalf("filesystem %s 不存在", name)
		}
		if op.Timeout != 120*time.Second {
			t.Errorf("filesystem %s timeout = %s，want 120s", name, op.Timeout)
		}
		if !strings.Contains(op.DocNote, "cannot cancel a native import") {
			t.Errorf("filesystem %s 未说明 timeout 不能取消 native import", name)
		}
	}
}

// TestFullResHelpDocumentsInlineByteBudget 防止 full-res 再次被描述为
// 绝对源分辨率：它只取消最长边请求上限，inline PNG 仍受事件字节预算约束。
func TestFullResHelpDocumentsInlineByteBudget(t *testing.T) {
	for _, name := range []string{"screenshot", "record"} {
		op, ok := ops.Lookup("editor", name)
		if !ok {
			t.Fatalf("editor %s 不存在", name)
		}
		text := op.ResponseNote
		for _, flag := range op.CLIFlags {
			if flag.Flag == "full-res" {
				text += "\n" + flag.Usage
			}
		}
		for _, want := range []string{"max_resolution=0", "inline PNG", "MCP/SSE byte budget"} {
			if !strings.Contains(text, want) {
				t.Errorf("editor %s full-res 文案缺少 %q：%s", name, want, text)
			}
		}
		for _, stale := range []string{"no downscale cap", "no per-frame downscale cap", "full source resolution"} {
			if strings.Contains(text, stale) {
				t.Errorf("editor %s 仍含绝对措辞 %q", name, stale)
			}
		}
	}
	screenshot, _ := ops.Lookup("editor", "screenshot")
	if !strings.Contains(screenshot.ResponseNote, "--save-to writes the full render") {
		t.Error("cinematic 原图保存未指向 --save-to")
	}
	record, _ := ops.Lookup("editor", "record")
	if !strings.Contains(record.ResponseNote, "per-frame longest-edge cap") {
		t.Error("record 未说明 per-frame inline 预算所对应的请求上限")
	}
}
