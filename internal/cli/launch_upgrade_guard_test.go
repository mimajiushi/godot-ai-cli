package cli

import (
	"strings"
	"testing"

	"github.com/mimajiushi/godot-ai-cli/internal/pluginmeta"
	"github.com/mimajiushi/godot-ai-cli/plugin"
)

// launch --upgrade-daemon 的升级前/后守卫区分（需求
// upgrade-daemon-unconnected-editor）：升级前本工程编辑器已连接、升级后
// 未重连 → 「升级成功 + warning」的 ok 载荷；从未连接的第三方编辑器保持
// fail-closed。完整升级链路（杀旧 daemon→起新 daemon）需要真实进程，
// 由回归注册表 RS 条目活体回放覆盖；这里钉决策与载荷形状。

// TestKeptSessionForProject：只有 project_path 匹配本工程的保留会话才算
// 「升级前已连接」的证据——别的工程共享旧 daemon 的会话绝不能误判。
func TestKeptSessionForProject(t *testing.T) {
	kept := []map[string]any{
		{"project_path": "/other/project/", "editor_pid": float64(1)},
		{"project_path": "/my/project/", "editor_pid": float64(2), "plugin_version": "4.1.0"},
	}
	got := keptSessionForProject(kept, "/my/project")
	if got == nil || got["editor_pid"] != float64(2) {
		t.Errorf("keptSessionForProject = %v", got)
	}
	if keptSessionForProject(kept, "/third/project") != nil {
		t.Error("a project with no kept session must not match")
	}
	if keptSessionForProject(nil, "/my/project") != nil {
		t.Error("nil kept list must not match")
	}
	if keptSessionForProject([]map[string]any{{"editor_pid": 1}}, "/my/project") != nil {
		t.Error("a session without project_path must not match")
	}
}

// TestUpgradeKeptEditorPayload：升级后守卫的 ok 载荷形状——status:ok
// （exit 0）、daemon_upgraded:true、editor_reconnected:false、新 daemon
// 身份、kept_editors、重启编辑器的 next_steps、插件步骤对象不丢；daemon
// 端口不可达时 recent_rejections 静默缺席（best-effort）。
func TestUpgradeKeptEditorPayload(t *testing.T) {
	dir := newLaunchTestProject(t)
	opts := launchOptions{httpPort: 1, wsPort: 2} // 端口 1 不可达：recent_rejections 缺席
	editors := []map[string]any{{"pid": 5020, "project": dir}}
	kept := map[string]any{
		"editor_pid": float64(5020), "project_path": dir, "plugin_version": "4.1.0",
	}

	payload := upgradeKeptEditorPayload(opts, dir, editors, kept, []string{"prior"}, "",
		plugin.InstallResult{Version: pluginmeta.PluginVersion()}, plugin.Plan{})

	if payload["status"] != "ok" {
		t.Errorf("status = %v, want ok（整体 error 会让调用方误判升级失败）", payload["status"])
	}
	if payload["daemon_upgraded"] != true || payload["editor_reconnected"] != false {
		t.Errorf("daemon_upgraded/editor_reconnected = %v/%v", payload["daemon_upgraded"], payload["editor_reconnected"])
	}
	daemonInfo, _ := payload["daemon"].(map[string]any)
	if daemonInfo["version"] != pluginmeta.PluginVersion() || daemonInfo["http_port"] != 1 || daemonInfo["ws_port"] != 2 {
		t.Errorf("daemon = %v, want the NEW daemon's identity", daemonInfo)
	}
	if keptEditors, _ := payload["kept_editors"].([]map[string]any); len(keptEditors) != 1 {
		t.Errorf("kept_editors = %v", payload["kept_editors"])
	}
	steps, _ := payload["next_steps"].([]string)
	if len(steps) == 0 || !strings.Contains(steps[0], "完全退出") {
		t.Errorf("next_steps = %v, want the editor-restart guidance", steps)
	}
	warnings, _ := payload["warnings"].([]string)
	if len(warnings) != 2 || !strings.Contains(warnings[1], "did not reconnect") ||
		!strings.Contains(warnings[1], "4.1.0") || !strings.Contains(warnings[1], pluginmeta.PluginVersion()) {
		t.Errorf("warnings = %v, want the reconnect warning naming both versions", warnings)
	}
	pluginStep, _ := payload["plugin"].(map[string]any)
	if pluginStep["to"] == "" || pluginStep["to"] == nil {
		t.Errorf("plugin step object must survive the guard path: %v", pluginStep)
	}
	if _, present := payload["recent_rejections"]; present {
		t.Errorf("recent_rejections must stay absent when the daemon does not answer: %v", payload["recent_rejections"])
	}
}
