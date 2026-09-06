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

// enumerateKnownDaemons merges the per-port pid files and the last-daemon
// record into one deterministic list keyed by HTTP port. Corrupt or
// port-less files are skipped; the pid file wins on a collision, with the
// last-daemon record's project filled in.
func enumerateKnownDaemons() []knownDaemonRecord {
	dir, err := userCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	base := filepath.Join(dir, "godot-ai-cli")

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
func probeKnownDaemonGET(httpPort int, path string) (body map[string]any, ok bool) {
	resp, err := knownDaemonProbeClient.Get(daemonURL(httpPort, path))
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
// returns the first HEALTHY one whose version is major.minor-compatible
// with requestedVersion — the daemon a DAEMON_MISMATCH error should point
// the user at. The result carries http_port / ws_port / version.
func findCompatibleDaemon(exceptPort int, requestedVersion string) (map[string]any, bool) {
	for _, rec := range enumerateKnownDaemons() {
		if rec.HTTPPort == exceptPort {
			continue
		}
		version, ok := probeDaemonHealth(rec.HTTPPort)
		if !ok || !semverCompatible(version, requestedVersion) {
			continue
		}
		wsPort := rec.WSPort
		if status, ok := probeKnownDaemonGET(rec.HTTPPort, "/godot-ai/status"); ok {
			// The live ws_port beats the record's (a stale pid file may
			// predate a restart on other ports).
			if n, ok := status["ws_port"].(float64); ok && n > 0 {
				wsPort = int(n)
			}
		}
		return map[string]any{
			"http_port": rec.HTTPPort,
			"ws_port":   wsPort,
			"version":   version,
		}, true
	}
	return nil, false
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

// shutdownDaemonKeepEditors is the --upgrade-daemon migration step: it
// shuts the daemon on httpPort down WITHOUT the quit_editor round a full
// `stop` performs — every connected editor process is kept (a compatible
// plugin reconnects to the next daemon on the same ports by itself). It
// returns how many editor sessions were connected when the shutdown was
// requested, and waits until the port stops answering.
func shutdownDaemonKeepEditors(httpPort int) (editors int, err error) {
	if body, ok := probeKnownDaemonGET(httpPort, "/godot-ai/cli/sessions"); ok {
		if list, ok := body["sessions"].([]any); ok {
			editors = len(list)
		}
	}
	if _, err := postDaemonJSON(httpPort, "/godot-ai/cli/shutdown", map[string]any{}, 5*time.Second); err != nil {
		return editors, fmt.Errorf("shutdown request: %w", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for daemonReachable(httpPort) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if daemonReachable(httpPort) {
		return editors, fmt.Errorf("daemon on http port %d still answers 5s after shutdown", httpPort)
	}
	return editors, nil
}
