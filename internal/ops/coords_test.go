package ops_test

import (
	"strings"
	"testing"

	"github.com/mimajiushi/godot-ai-cli/internal/ops"
)

// TestScreenshotCoordsOpSurface 钉住 R-3/R-2 在 ops 表上的可见面：
// editor screenshot 新增的 CLI 侧 flag（coords/baseline/diff-threshold/
// diff-out），以及 game node-screen-rect 这条与截图坐标系对齐的只读查询。
// 不碰 parity_test.go 的固定 pin——新 op 复用已注册的 game_command 包装。
func TestScreenshotCoordsOpSurface(t *testing.T) {
	shot, ok := ops.Lookup("editor", "screenshot")
	if !ok {
		t.Fatal("editor screenshot missing from the table")
	}
	wantFlags := map[string]string{
		"coords":         "string",
		"baseline":       "string",
		"diff-threshold": "float",
		"diff-out":       "string",
	}
	gotFlags := map[string]string{}
	for _, f := range shot.CLIFlags {
		gotFlags[f.Flag] = f.Kind
	}
	for flag, kind := range wantFlags {
		if gotFlags[flag] != kind {
			t.Errorf("editor screenshot --%s kind = %q, want %q", flag, gotFlags[flag], kind)
		}
	}
	// The coordinate metadata is part of the documented response surface.
	for _, want := range []string{"canvas_scale", "canvas_size"} {
		if !strings.Contains(shot.ResponseNote, want) {
			t.Errorf("screenshot response note must document %s: %q", want, shot.ResponseNote)
		}
	}

	rect, ok := ops.Lookup("game", "node-screen-rect")
	if !ok {
		t.Fatal("game node-screen-rect missing from the table")
	}
	if rect.PluginCommand != "game_command" || rect.WrapOp != "node_screen_rect" {
		t.Errorf("node-screen-rect routes as %s/%s, want game_command/node_screen_rect",
			rect.PluginCommand, rect.WrapOp)
	}
	if rect.Write {
		t.Error("node-screen-rect is a read-only query and must not be write-gated")
	}
	if len(rect.Params) != 1 || rect.Params[0].Param != "path" || !rect.Params[0].Required {
		t.Errorf("node-screen-rect params = %+v, want exactly one required path", rect.Params)
	}
	for _, want := range []string{"rect_kind", "canvas_rect", "image_rect", "scale"} {
		if !strings.Contains(rect.ResponseNote, want) {
			t.Errorf("node-screen-rect response note must document %s: %q", want, rect.ResponseNote)
		}
	}
}
