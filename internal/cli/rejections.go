// 握手拒绝记录的 CLI 侧消费（需求 handshake-rejection-visibility）：
// daemon 记下每次被拒绝的 v4 握手（版本不匹配/格式错误/编辑器版本越线/
// 重复 session 等），CLI 经免认证的 cli 端点拉取，让 status / launch --attach /
// plugin install 能回答「编辑器明明活着、daemon 也活着，为什么 sessions 是
// 空的」。所有消费点都按 best-effort 处理：旧 daemon 没有该端点、探测失败
// 一律返回 nil，绝不把「看不见拒绝原因」升级为命令失败。
package cli

import (
	"sort"
	"strings"
)

// rejectionsEndpoint 是 daemon 暴露最近握手拒绝的 cli 面路径（与
// sessions/health 同级免认证端点）。
const rejectionsEndpoint = "/godot-ai/cli/rejections"

// maxMergedRejections 约束 status 合并输出的条数：诊断线索，不是日志转储。
const maxMergedRejections = 5

// recentRejectionsFrom 拉取 httpPort 对应 daemon 的最近握手拒绝记录
// （新→旧）。daemon 无此端点（beta.25 之前）、探测失败或载荷畸形都返回 nil。
func recentRejectionsFrom(httpPort int) []any {
	body, ok := probeKnownDaemonGET(httpPort, rejectionsEndpoint)
	if !ok {
		return nil
	}
	list, _ := body["rejections"].([]any)
	return list
}

// mergedRecentRejections 汇总每个活着的 known daemon 的拒绝记录，按时间
// 新→旧排序并截断。status 用它把「本机任意 daemon 上的拒绝」一次说清——
// 现场里被拒绝的编辑器连的往往是另一个端口上的 daemon（需求 §4.1）。
func mergedRecentRejections(live []map[string]any) []any {
	var all []any
	for _, entry := range live {
		// knownDaemonsReport 构造的 map 里 http_port 是 int（未经 JSON
		// 往返）；经 printJSON 输出的则是 float64——两种都要认。
		var port int
		switch v := entry["http_port"].(type) {
		case int:
			port = v
		case float64:
			port = int(v)
		default:
			continue
		}
		all = append(all, recentRejectionsFrom(port)...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		ai, _ := all[i].(map[string]any)["at"].(string)
		aj, _ := all[j].(map[string]any)["at"].(string)
		return ai > aj // RFC3339 同 offset 字典序即时间序（均为 UTC Z）
	})
	if len(all) > maxMergedRejections {
		all = all[:maxMergedRejections]
	}
	return all
}

// inMemoryPluginVersions 收集「本工程在 daemon 眼里跑的是什么插件版本」：
// 已连接 session 的 plugin_version + 被拒握手的 peer_version。磁盘版本
// 已对齐但这里有值且不等时，说明编辑器内存里还是旧代码——plugin install /
// launch 据此给出「完全重启编辑器」的 warning（需求 §4.2）。
func inMemoryPluginVersions(httpPort int, projectDir string) []map[string]any {
	var out []map[string]any
	if body, ok := probeKnownDaemonGET(httpPort, "/godot-ai/cli/sessions"); ok {
		if list, ok := body["sessions"].([]any); ok {
			for _, item := range list {
				sess, ok := item.(map[string]any)
				if !ok {
					continue
				}
				pp, _ := sess["project_path"].(string)
				pv, _ := sess["plugin_version"].(string)
				if pv != "" && sameProjectPath(pp, projectDir) {
					out = append(out, map[string]any{
						"source": "connected_session", "version": pv,
						"editor_pid": sess["editor_pid"],
					})
				}
			}
		}
	}
	for _, r := range recentRejectionsFrom(httpPort) {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		pp, _ := m["project_path"].(string)
		pv, _ := m["peer_version"].(string)
		if pv != "" && sameProjectPath(pp, projectDir) {
			// probe_version_mismatch 是插件在 HTTP 探针阶段自阻后的主动
			// 自报（从未发起 WS 握手），source 与握手被拒区分开（需求
			// handshake-rejection-live-reachability）。
			source := "rejected_handshake"
			if m["reason"] == "probe_version_mismatch" {
				source = "probe_rejection"
			}
			out = append(out, map[string]any{
				"source": source, "version": pv,
				"editor_pid": m["editor_pid"], "at": m["at"],
			})
		}
	}
	return out
}

// editorRestartWarning 渲染「磁盘插件已对齐但编辑器内存仍是旧版本」的
// warning 文案与下一步（需求 §4.2 的验收形状）。没有任何不一致时返回
// 空串与 nil。
func editorRestartWarning(installed string, inMemory []map[string]any) (string, []string) {
	byVersion := map[string]bool{}
	for _, m := range inMemory {
		if v, _ := m["version"].(string); v != "" && v != installed {
			byVersion[v] = true
		}
	}
	if len(byVersion) == 0 {
		return "", nil
	}
	versions := make([]string, 0, len(byVersion))
	for v := range byVersion {
		versions = append(versions, v)
	}
	sort.Strings(versions)
	warning := ("PROJECT_PLUGIN_MISMATCH: 磁盘插件已是 " + installed + "，但编辑器内存中仍是 " +
		strings.Join(versions, "/") +
		"。必须【完全退出并重新打开编辑器】才会生效（filesystem scan / reload-plugin 不会替换已加载的插件代码）。")
	return warning, []string{
		"完全退出 Godot 编辑器",
		"重新打开（建议用 godot-ai-cli launch --project <dir> 以便复用同一 daemon）",
	}
}
