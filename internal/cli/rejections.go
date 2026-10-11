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
	"time"

	"github.com/mimajiushi/godot-ai-cli/internal/godot"
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

// rejectionPIDAlive 判定一条拒绝记录关联的 editor_pid 是否仍是活着的编辑器
// 进程。判据与 stop 的 aliveEditorPIDs 同源（godot.IsGodotEditorProcess）：
// 单纯 liveness 会被 Windows 的 pid 复用骗过（退出编辑器的 pid 被 svchost
// 之类接管），把一个陌生人当成「被拒绝的编辑器还活着」。查不到（进程已退出、
// 权限受限、平台不支持）一律降级 false——false 是安全的一侧：宁可把一条仍有
// 效的记录当历史，也不误报「它还活着」。
//
// 提成可替换变量只为单测能注入「存活的编辑器」（现场判据需要在盘上真有一个
// 名为 godot* 的进程映像，单测造不出来），与 sessionGoneTimeout / userCacheDir
// 的既有做法一致。
var rejectionPIDAlive = godot.IsGodotEditorProcess

// renderedRecentRejections 是 status 渲染拒绝记录的唯一入口：先汇总本机活
// daemon 的拒绝记录，再给每条补生命周期字段（需求 R-9 (b)），最后回答「是否
// 至少有一条记录仍构成现况」——status 只在它为 true 时才给那条现在时 hint
// （需求 R-9 (a)）。
//
// sessions 是调用方已能确认「该项目当前有什么会话」的那一份列表：status 成功
// 路径传解析到的 daemon 的 sessions（正是回包 sessions 区展示的那一份）；
// daemon 不可达的失败路径传 nil。传 nil 时无从确认「已被就绪会话取代」，此时
// 按文档 (a) 的「该项目当前没有 ready 会话」保守给出 hint——没有证据就不宣布
// 陈旧，绝不谎报「已被取代」。
func renderedRecentRejections(live []map[string]any, sessions []any) ([]any, bool) {
	records := mergedRecentRejections(live)
	if len(records) == 0 {
		return nil, false
	}
	now := time.Now().UTC()
	out := make([]any, 0, len(records))
	actionable := false
	for _, raw := range records {
		rec, ok := raw.(map[string]any)
		if !ok {
			// 畸形条目（daemon 载荷不是对象）：原样透传，并按「无法证明它
			// 已陈旧」保守计入现况。
			out = append(out, raw)
			actionable = true
			continue
		}
		rendered, current := rejectionLifecycle(rec, sessions, now)
		out = append(out, rendered)
		if current {
			actionable = true
		}
	}
	return out, actionable
}

// rejectionLifecycle 给一条拒绝记录补 age_seconds / process_alive /
// superseded_by，并判定这条记录是否仍构成现况（需求 R-9）。
//
// 现况判定严格按文档 (a)：只有当「该项目当前没有 ready 会话」或「记录关联的
// editor_pid 仍存活」时才算现况。项目身份取自记录自己的 project_path（与
// sessions 的 project_path 用同一个 sameProjectPath 比较）；记录没有
// project_path 时无从匹配就绪会话，按「没有 ready 会话」保守处理。daemon 载荷
// 只在本次渲染里使用，所以这里改的是副本，不污染调用方手里的 map。
func rejectionLifecycle(rec map[string]any, sessions []any, now time.Time) (map[string]any, bool) {
	out := make(map[string]any, len(rec)+3)
	for k, v := range rec {
		out[k] = v
	}

	// age_seconds：按 at 在渲染时现算（需求明确「渲染时算」）。at 缺失或不是
	// RFC3339 时整个字段省略——与既有 omitempty 惯例一致，绝不编造年龄。
	if at, _ := rec["at"].(string); at != "" {
		if ts, err := time.Parse(time.RFC3339, at); err == nil {
			age := int64(now.Sub(ts).Seconds())
			if age < 0 {
				age = 0 // 时钟回拨/对端时间在未来：负年龄无意义，夹到 0
			}
			out["age_seconds"] = age
		}
	}

	// process_alive：editor_pid 可用时才扫（没有 pid 就没什么可扫的，字段
	// 省略）；扫不出来降级 false，语义是「当作历史记录」（见 rejectionPIDAlive）。
	pid := 0
	switch v := rec["editor_pid"].(type) {
	case int:
		pid = v
	case float64:
		pid = int(v)
	}
	alive := false
	if pid > 0 {
		alive = rejectionPIDAlive(pid)
		out["process_alive"] = alive
	}

	// superseded_by：该项目当前已有就绪会话时给出 session_id（有则给、无则省）。
	project, _ := rec["project_path"].(string)
	superseded := readySessionIDForProject(sessions, project)
	if superseded != "" {
		out["superseded_by"] = superseded
	}

	// 现况 = 「该项目当前没有 ready 会话」或「关联 pid 仍存活」（文档 (a)）。
	return out, superseded == "" || alive
}

// readySessionIDForProject 返回 sessions 里第一条「project_path 指向
// projectDir 且 readiness == "ready"」的会话 id；没有则返回空串。
//
// 只认 readiness == "ready"（与需求文档「该项目当前 ready 会话」一致）：
// no_scene / importing / playing 都不算「已就绪」，此时宁可不给 superseded_by
// （调用方按「没有 ready 会话」继续看到 hint），也不谎称旧记录已被取代。
func readySessionIDForProject(sessions []any, projectDir string) string {
	if projectDir == "" {
		return ""
	}
	for _, entry := range sessions {
		sess, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if pp, _ := sess["project_path"].(string); !sameProjectPath(pp, projectDir) {
			continue
		}
		if readiness, _ := sess["readiness"].(string); readiness != "ready" {
			continue
		}
		if sid, _ := sess["session_id"].(string); sid != "" {
			return sid
		}
	}
	return ""
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
