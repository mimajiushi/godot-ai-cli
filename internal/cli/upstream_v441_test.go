package cli

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
	"github.com/mimajiushi/godot-ai-cli/internal/testutil/mockplugin"
)

// TestInputMouseButtonsWireParam 验证 motion 的 buttons 数组进入既有
// game_command 包装，同时保留 fork 的 affects_mouse_position:false 说明。
func TestInputMouseButtonsWireParam(t *testing.T) {
	op, cmd := leafFor(t, "game", "input-mouse")
	for flag, value := range map[string]string{
		"event":   "motion",
		"buttons": `["left","right"]`,
	} {
		if err := cmd.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	params, err := collectParams(cmd, op)
	if err != nil {
		t.Fatal(err)
	}
	if params["op"] != "input_mouse" {
		t.Fatalf("wrap op = %v", params["op"])
	}
	nested, ok := params["params"].(map[string]any)
	if !ok {
		t.Fatalf("nested params = %v", params)
	}
	if want := []any{"left", "right"}; !reflect.DeepEqual(nested["buttons"], want) {
		t.Errorf("buttons = %#v，want %#v", nested["buttons"], want)
	}
	if !strings.Contains(op.ResponseNote, `"affects_mouse_position": false`) {
		t.Error("fork 的 affects_mouse_position:false 兼容说明丢失")
	}
}

// TestScreenshotCinematicParamValidation 覆盖尺寸/落盘参数的正负组合，
// 包括经 --params 传入的 float64 JSON 数字，而非只测 typed flags。
func TestScreenshotCinematicParamValidation(t *testing.T) {
	valid := map[string]any{
		"source": "cinematic", "width": 1280, "height": 720,
		"save_to": "res://shots/camera.png", "overwrite": true,
	}
	if err := validateScreenshotOutputParams(valid); err != nil {
		t.Fatalf("合法 cinematic 参数被拒：%v", err)
	}
	withJSONNumbers := map[string]any{"source": "cinematic", "width": 640.0, "height": 360.0}
	if err := validateScreenshotOutputParams(withJSONNumbers); err != nil {
		t.Fatalf("JSON 整数形式的宽高被拒：%v", err)
	}

	cases := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{name: "缺少 height", params: map[string]any{"source": "cinematic", "width": 640}, want: "together"},
		{name: "缺少 width", params: map[string]any{"source": "cinematic", "height": 360}, want: "together"},
		{name: "非 cinematic 尺寸", params: map[string]any{"source": "viewport", "width": 640, "height": 360}, want: "require --source cinematic"},
		{name: "非 cinematic 落盘", params: map[string]any{"source": "game", "save_to": "res://shot.png"}, want: "require --source cinematic"},
		{name: "overwrite 无目标", params: map[string]any{"source": "cinematic", "overwrite": true}, want: "requires --save-to"},
		{name: "宽度过小", params: map[string]any{"source": "cinematic", "width": 0, "height": 360}, want: "from 1 to 4096"},
		{name: "高度过大", params: map[string]any{"source": "cinematic", "width": 640, "height": 4097}, want: "from 1 to 4096"},
		{name: "非整数", params: map[string]any{"source": "cinematic", "width": 640.5, "height": 360}, want: "whole number"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateScreenshotOutputParams(tc.params)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v，want 包含 %q", err, tc.want)
			}
		})
	}
}

// TestScreenshotCinematicFlagsKeepForkExtras 同时走上游 save_to/overwrite
// 与 fork 的 --out/--full-res/--region，确认两组通道不互相吞参。
func TestScreenshotCinematicFlagsKeepForkExtras(t *testing.T) {
	d, plugin := startScreenshotDaemon(t, false)
	outFile := filepath.Join(t.TempDir(), "nested", "preview.png")
	out, err := runScreenshotArgs(t, d.HTTPPort(),
		"--source", "cinematic", "--width", "2", "--height", "1",
		"--save-to", "res://shots/full.png", "--overwrite",
		"--out", outFile, "--full-res", "--region", "0,0,2,1")
	if err != nil {
		t.Fatalf("组合截图参数失败：%v\n%s", err, out)
	}
	wire := lastScreenshotWire(t, plugin)
	for key, want := range map[string]any{
		"width": float64(2), "height": float64(1), "max_resolution": float64(0),
		"save_to": "res://shots/full.png", "overwrite": true,
	} {
		if !reflect.DeepEqual(wire[key], want) {
			t.Errorf("wire %s = %#v，want %#v", key, wire[key], want)
		}
	}
	if _, err := os.Stat(outFile); err != nil {
		t.Errorf("fork --out 未落盘：%v", err)
	}
}

// TestScreenshotSaveToReadiness 仅 save_to 落盘路径触发写门禁；
// 同一 playing 会话里的普通 cinematic 截图仍必须直达插件。
func TestScreenshotSaveToReadiness(t *testing.T) {
	d, err := daemon.Start(context.Background(), daemon.Config{HTTPPort: 0, WSPort: 0, Version: "test"})
	if err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = d.Shutdown(ctx)
	})
	plugin := mockplugin.Dial(t, d.Bridge().Addr(), d.Bridge().WSCapability, map[string]any{"readiness": "playing"})
	plugin.SetResponder(func(command string, _ map[string]any) *mockplugin.Response {
		switch command {
		case "get_editor_state":
			return &mockplugin.Response{Data: map[string]any{
				"readiness": "playing", "game_status": map[string]any{"status": "live"},
			}}
		case "take_screenshot":
			return &mockplugin.Response{Data: map[string]any{
				"format": "png", "width": 2, "height": 1,
				"image_base64": "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshotFixturePNG(t)),
			}}
		default:
			return nil
		}
	})

	out, err := runScreenshotArgs(t, d.HTTPPort(), "--source", "cinematic",
		"--width", "2", "--height", "1", "--save-to", "res://shots/full.png")
	if err == nil || !strings.Contains(out, "EDITOR_NOT_READY") {
		t.Fatalf("save_to 未被 playing 写门禁拒绝：err=%v\n%s", err, out)
	}
	if got := plugin.Count("take_screenshot"); got != 0 {
		t.Fatalf("被拒的 save_to 截图到达插件 %d 次", got)
	}

	out, err = runScreenshotArgs(t, d.HTTPPort(), "--source", "cinematic", "--width", "2", "--height", "1")
	if err != nil {
		t.Fatalf("无 save_to 的只读截图被误拒：%v\n%s", err, out)
	}
	if got := plugin.Count("take_screenshot"); got != 1 {
		t.Errorf("只读截图到达插件 %d 次，want 1", got)
	}
}
