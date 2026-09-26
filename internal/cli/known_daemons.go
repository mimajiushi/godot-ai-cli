// Known-daemon discovery: with several daemon versions/ports coexisting on
// one machine, launch and status need a view beyond the single recorded
// port. Every daemon writes <user cache dir>/godot-ai-cli/daemon-<port>.json
// while running (removed on clean shutdown, so a leftover file means a
// crashed/killed daemon), and launch/serve keep last-daemon.json. This file
// enumerates those records and probes them with a tight timeout — every
// probe is best-effort: a dead or foreign occupant is skipped, never an
// error source.
package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mimajiushi/godot-ai-cli/internal/capability"
	"github.com/mimajiushi/godot-ai-cli/internal/pluginmeta"
)

// knownDaemonProbeTimeout bounds one probe of a recorded daemon; a local
// daemon answers in microseconds, so 300ms already means "not there".
const knownDaemonProbeTimeout = 300 * time.Millisecond

// knownDaemonProbeClient is shared by every known-daemon probe (status /
// sessions / health). Separate from apiClient: these probes must stay cheap
// even when a recorded port is a black hole.
var knownDaemonProbeClient = &http.Client{Timeout: knownDaemonProbeTimeout}

// knownDaemonRecord is one recorded daemon identity. The per-port pid files
// carry pid/http_port/ws_port/version/started_at; last-daemon.json adds the
// last launched project.
type knownDaemonRecord struct {
	PID       int    `json:"pid"`
	HTTPPort  int    `json:"http_port"`
	WSPort    int    `json:"ws_port"`
	Version   string `json:"version"`
	StartedAt string `json:"started_at"`
	Project   string `json:"project,omitempty"`
}

// daemonRecordsDir 是 daemon-*.json 记录目录：与 enumerateKnownDaemons
// 同一取值（走 userCacheDir seam，测试可隔离）。daemon.PIDFilePath 直接
// 调 os.UserCacheDir() 不经 seam，测试里会和枚举层错位——凡 CLI 侧要
// 定位/删除记录文件的地方一律用这里的路径。
func daemonRecordsDir() string {
	dir, err := userCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "godot-ai-cli")
}

// daemonRecordPath 返回一条端口记录的落盘路径（文件不存在也无妨）。
func daemonRecordPath(httpPort int) string {
	return filepath.Join(daemonRecordsDir(), fmt.Sprintf("daemon-%d.json", httpPort))
}

// enumerateKnownDaemons merges the per-port pid files and the last-daemon
// record into one deterministic list keyed by HTTP port. Corrupt or
// port-less files are skipped; the pid file wins on a collision, with the
// last-daemon record's project filled in.
func enumerateKnownDaemons() []knownDaemonRecord {
	base := daemonRecordsDir()

	byPort := map[int]knownDaemonRecord{}
	if entries, err := os.ReadDir(base); err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, "daemon-") || !strings.HasSuffix(name, ".json") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(base, name))
			if err != nil {
				continue
			}
			var rec knownDaemonRecord
			if err := json.Unmarshal(data, &rec); err != nil || rec.HTTPPort <= 0 {
				continue
			}
			byPort[rec.HTTPPort] = rec
		}
	}
	if last, ok := readLastDaemon(); ok {
		if rec, exists := byPort[last.HTTPPort]; exists {
			rec.Project = last.Project
			byPort[last.HTTPPort] = rec
		} else {
			byPort[last.HTTPPort] = knownDaemonRecord{
				HTTPPort:  last.HTTPPort,
				WSPort:    last.WSPort,
				Project:   last.Project,
				StartedAt: last.StartedAt,
			}
		}
	}

	out := make([]knownDaemonRecord, 0, len(byPort))
	for _, rec := range byPort {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].HTTPPort < out[j].HTTPPort })
	return out
}

// probeKnownDaemonGET fetches one endpoint from a recorded daemon with the
// tight probe timeout. ok=false covers every failure shape (unreachable,
// non-JSON, non-200) — probes never fail the caller.
//
// v4 起 /godot-ai/status 需要 Bearer 认证：能读到该端口的 capability
// 记录（本机同账户）就带上，读不到则以匿名身份探（旧版 daemon 与 v3
// Python 服务器仍匿名应答；v4 对端的 401 让调用方按"不可读"处理）。
func probeKnownDaemonGET(httpPort int, path string) (body map[string]any, ok bool) {
	req, err := http.NewRequest(http.MethodGet, daemonURL(httpPort, path), nil)
	if err != nil {
		return nil, false
	}
	if rec, err := capability.Read(httpPort); err == nil && rec != nil {
		req.Header.Set("Authorization", "Bearer "+rec.HTTP)
	}
	resp, err := knownDaemonProbeClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, false
	}
	return out, true
}

// probeDaemonHealth reports whether OUR daemon (not the upstream Python
// server, which has no /godot-ai/cli/* API) answers on httpPort, returning
// its advertised version.
func probeDaemonHealth(httpPort int) (version string, ok bool) {
	body, ok := probeKnownDaemonGET(httpPort, "/godot-ai/cli/health")
	if !ok || body["status"] != "ok" {
		return "", false
	}
	version, _ = body["version"].(string)
	return version, true
}

// semverCompatible reports the major.minor rule for raw version strings;
// an unparseable side is never compatible (same rule as the handshake).
func semverCompatible(a, b string) bool {
	va, err := pluginmeta.ParseSemver(a)
	if err != nil {
		return false
	}
	vb, err := pluginmeta.ParseSemver(b)
	if err != nil {
		return false
	}
	return pluginmeta.Compatible(va, vb)
}

// findCompatibleDaemon scans every known daemon except exceptPort and
// returns the best HEALTHY one whose version is major.minor-compatible
// with requestedVersion — the daemon a DAEMON_MISMATCH error should point
// the user at. An exact version match is preferred over a merely
// compatible one (a compatible-but-older daemon still shows stale
// warnings). The result carries http_port / ws_port / version.
func findCompatibleDaemon(exceptPort int, requestedVersion string) (map[string]any, bool) {
	var compatible map[string]any
	for _, rec := range enumerateKnownDaemons() {
		if rec.HTTPPort == exceptPort {
			continue
		}
		version, ok := probeDaemonHealth(rec.HTTPPort)
		if !ok || !semverCompatible(version, requestedVersion) {
			continue
		}
		candidate := map[string]any{
			"http_port": rec.HTTPPort,
			"ws_port":   knownDaemonWSPort(rec),
			"version":   version,
		}
		if version == requestedVersion {
			return candidate, true
		}
		if compatible == nil {
			compatible = candidate
		}
	}
	if compatible != nil {
		return compatible, true
	}
	return nil, false
}

// knownDaemonWSPort resolves a daemon record's ws_port, preferring the live
// status endpoint (a stale pid file may predate a restart on other ports).
func knownDaemonWSPort(rec knownDaemonRecord) int {
	if status, ok := probeKnownDaemonGET(rec.HTTPPort, "/godot-ai/status"); ok {
		if n, ok := status["ws_port"].(float64); ok && n > 0 {
			return int(n)
		}
	}
	return rec.WSPort
}

// findProjectOnOtherDaemons is launch's double-open guard: it scans every
// known daemon except the one this launch targets and reports the first
// session whose project_path matches projectDir. A daemon that does not
// answer (stale pid file, killed process) is skipped SILENTLY — nothing to
// guard against; a daemon that answers but serves a malformed sessions
// payload earns a warning, because that probe failure could be hiding a
// real editor for this project.
func findProjectOnOtherDaemons(exceptPort int, projectDir string) (hit map[string]any, warnings []string) {
	for _, rec := range enumerateKnownDaemons() {
		if rec.HTTPPort == exceptPort {
			continue
		}
		body, ok := probeKnownDaemonGET(rec.HTTPPort, "/godot-ai/cli/sessions")
		if !ok {
			continue // dead or foreign: nothing to guard against
		}
		list, ok := body["sessions"].([]any)
		if !ok {
			warnings = append(warnings, fmt.Sprintf(
				"double-open check: daemon on http port %d answered with a malformed sessions payload — could not verify no editor for this project is attached to it", rec.HTTPPort))
			continue
		}
		sess := findProjectSession(list, projectDir)
		if sess == nil {
			continue
		}
		version := rec.Version
		if live, ok := probeDaemonHealth(rec.HTTPPort); ok && live != "" {
			version = live
		}
		return map[string]any{
			"editor_pid":       sess["editor_pid"],
			"daemon_http_port": rec.HTTPPort,
			"daemon_version":   version,
			"session_id":       sess["session_id"],
			"retryable":        false,
		}, warnings
	}
	return nil, warnings
}

// versionRelation 标注一条 daemon 记录版本与当前 CLI 自带插件版本的相对
// 关系（需求 editor-attach-and-daemon-discovery §3.4：多版本共存时
// known_daemons 里看不出新旧，排查很难判断该不该复用）。两者都是插件
// 版本（同一 scheme）；任一解析失败标 "unknown"。
func versionRelation(recordVersion string) string {
	rv, err := pluginmeta.ParseSemver(recordVersion)
	if err != nil {
		return "unknown"
	}
	bv, err := pluginmeta.ParseSemver(pluginmeta.PluginVersion())
	if err != nil {
		return "unknown"
	}
	switch pluginmeta.Compare(rv, bv) {
	case 0:
		return "same"
	case -1:
		return "older"
	default:
		return "newer"
	}
}

// knownDaemonsReport builds status's known_daemons array: every recorded
// daemon, probed live. A running daemon reports its live
// version/pid/ws_port and the project paths of its sessions; an unreachable
// one reports the record with running:false (a leftover pid file means a
// daemon died without cleanup — worth surfacing, never blocking). The
// daemon this status call resolved to carries current:true.
func knownDaemonsReport(currentPort int) []any {
	records := enumerateKnownDaemons()
	out := make([]any, 0, len(records))
	for _, rec := range records {
		entry := map[string]any{"http_port": rec.HTTPPort}
		if rec.HTTPPort == currentPort {
			entry["current"] = true
		}
		status, ok := probeKnownDaemonGET(rec.HTTPPort, "/godot-ai/status")
		if !ok || status["name"] != "godot-ai" {
			entry["running"] = false
			entry["version_relation"] = versionRelation(rec.Version)
			if rec.WSPort > 0 {
				entry["ws_port"] = rec.WSPort
			}
			if rec.Version != "" {
				entry["version"] = rec.Version
			}
			if rec.PID > 0 {
				entry["pid"] = rec.PID
			}
			if rec.StartedAt != "" {
				entry["started_at"] = rec.StartedAt
			}
			if rec.Project != "" {
				entry["project"] = rec.Project
			}
			out = append(out, entry)
			continue
		}
		entry["running"] = true
		entry["version"] = status["version"]
		// 活 daemon 以实时版本为准标注新旧（记录文件可能先于重启。
		entry["version_relation"] = versionRelation(fmt.Sprint(status["version"]))
		entry["ws_port"] = status["ws_port"]
		entry["pid"] = status["pid"]
		var projects []string
		if sessions, ok := probeKnownDaemonGET(rec.HTTPPort, "/godot-ai/cli/sessions"); ok {
			if list, ok := sessions["sessions"].([]any); ok {
				for _, item := range list {
					sess, ok := item.(map[string]any)
					if !ok {
						continue
					}
					if pp, _ := sess["project_path"].(string); pp != "" {
						projects = append(projects, pp)
					}
				}
			}
		}
		if projects == nil {
			projects = []string{}
		}
		entry["projects"] = projects
		out = append(out, entry)
	}
	return out
}

// liveDaemonEntries 从 knownDaemonsReport 的结果里挑出活着的 daemon，
// 收成 {http_port, ws_port, version?} 的轻量列表——status 在目标端口无
// daemon 时用它回答「本机还有谁活着」（需求 editor-attach-and-daemon-discovery
// §3.1：hint 只允许在确实一个活 daemon 都没有时提 launch）。
func liveDaemonEntries(known []any) []map[string]any {
	var live []map[string]any
	for _, e := range known {
		m, ok := e.(map[string]any)
		if !ok || m["running"] != true {
			continue
		}
		entry := map[string]any{"http_port": m["http_port"], "ws_port": m["ws_port"]}
		if v, ok := m["version"].(string); ok && v != "" {
			entry["version"] = v
		}
		live = append(live, entry)
	}
	return live
}

// pruneDaemonRecords 删除 daemon-*.json 里探测不应答的死记录（需求
// editor-attach-and-daemon-discovery §3.4：%LOCALAPPDATA%\godot-ai-cli 下
// 死记录只增不减）。活记录与 last-daemon.json 绝不触碰——记录是 daemon
// 自己清理的，这里只收拾「死无葬身之地」的残留。返回删除与保留端口。
func pruneDaemonRecords() (pruned []int, kept []int) {
	for _, rec := range enumerateKnownDaemons() {
		if _, ok := probeDaemonHealth(rec.HTTPPort); ok {
			kept = append(kept, rec.HTTPPort)
			continue
		}
		// 以 Remove 的结果为唯一判据：记录文件根本不存在（端口仅由
		// last-daemon.json 指认）时 IsNotExist——没有可删之物，既不记
		// pruned 也不记 kept（last-daemon.json 按设计绝不触碰，把它报成
		// pruned 会造成「重复 prune 永远非空」的虚报，需求
		// status-prune-phantom-lastdaemon）；真正删掉才记 pruned。
		err := os.Remove(daemonRecordPath(rec.HTTPPort))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			kept = append(kept, rec.HTTPPort) // 删不掉的保留，报错交给 warnings
			continue
		}
		pruned = append(pruned, rec.HTTPPort)
	}
	return pruned, kept
}

// shutdownDaemonKeepEditors is the --upgrade-daemon migration step: it
// shuts the daemon on httpPort down WITHOUT the quit_editor round a full
// `stop` performs — every connected editor process is kept (a compatible
// plugin reconnects to the next daemon on the same ports by itself). It
// returns the identity of every session connected when the shutdown was
// requested ({editor_pid, project_path, plugin_version}——「升级前本工程
// 编辑器已连接」的判定证据，需求 upgrade-daemon-unconnected-editor §3.2
// 的升级前/后守卫区分靠它），并等待端口停止应答。
func shutdownDaemonKeepEditors(httpPort int) (kept []map[string]any, err error) {
	if body, ok := probeKnownDaemonGET(httpPort, "/godot-ai/cli/sessions"); ok {
		if list, ok := body["sessions"].([]any); ok {
			for _, item := range list {
				sess, ok := item.(map[string]any)
				if !ok {
					continue
				}
				kept = append(kept, map[string]any{
					"editor_pid":     sess["editor_pid"],
					"project_path":   sess["project_path"],
					"plugin_version": sess["plugin_version"],
				})
			}
		}
	}
	if _, err := postDaemonJSON(httpPort, "/godot-ai/cli/shutdown", map[string]any{}, 5*time.Second); err != nil {
		return kept, fmt.Errorf("shutdown request: %w", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for daemonReachable(httpPort) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if daemonReachable(httpPort) {
		return kept, fmt.Errorf("daemon on http port %d still answers 5s after shutdown", httpPort)
	}
	return kept, nil
}
