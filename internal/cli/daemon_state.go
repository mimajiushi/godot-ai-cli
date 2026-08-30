// Last-daemon port memory: launch/serve record the daemon they brought up
// in <user cache dir>/godot-ai-cli/last-daemon.json so the daemon-facing
// one-shot commands (status, stop, ops, call, ...) can find it without a
// repeated --http-port flag. The record is a hint only — a stale or corrupt
// file is silently tolerated (overwritten by the next launch, and port
// resolution always falls back to the default port).
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
)

// userCacheDir resolves the OS user cache dir. It is a variable so tests
// can substitute a t.TempDir() and stay hermetic (same pattern as
// daemonctl.spawnServe).
var userCacheDir = os.UserCacheDir

// lastDaemonRecord is the persisted identity of the most recently launched
// daemon.
type lastDaemonRecord struct {
	HTTPPort  int    `json:"http_port"`
	WSPort    int    `json:"ws_port"`
	Project   string `json:"project,omitempty"`
	StartedAt string `json:"started_at"`
}

// lastDaemonPath is where the record lives:
// <user cache dir>/godot-ai-cli/last-daemon.json (the same base dir the
// daemon uses for its per-port PID files).
func lastDaemonPath() string {
	dir, err := userCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "godot-ai-cli", "last-daemon.json")
}

// writeLastDaemon records the ports of the daemon launch/serve just brought
// up. Stale records are simply overwritten.
func writeLastDaemon(rec lastDaemonRecord) error {
	if rec.StartedAt == "" {
		rec.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	path := lastDaemonPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// readLastDaemon loads the recorded daemon identity. A missing, corrupt, or
// port-less file reports !ok — the record is a hint, never an error source.
func readLastDaemon() (lastDaemonRecord, bool) {
	data, err := os.ReadFile(lastDaemonPath())
	if err != nil {
		return lastDaemonRecord{}, false
	}
	var rec lastDaemonRecord
	if err := json.Unmarshal(data, &rec); err != nil || rec.HTTPPort <= 0 {
		return lastDaemonRecord{}, false
	}
	return rec, true
}

// removeLastDaemon deletes the record; a missing file is not an error.
func removeLastDaemon() error {
	if err := os.Remove(lastDaemonPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// daemonPortCandidates orders the HTTP ports a daemon-facing one-shot
// command probes:
//
//  1. an explicit --http-port flag wins alone — the user named that port
//  2. the port recorded by the last launch/serve, then the default port as
//     fallback (the recorded daemon may be gone while a default one runs)
//  3. the default port alone when no record exists
func daemonPortCandidates(cmd *cobra.Command) []int {
	if cmd.Flags().Changed("http-port") {
		if port, err := cmd.Flags().GetInt("http-port"); err == nil {
			return []int{port}
		}
	}
	if rec, ok := readLastDaemon(); ok {
		if rec.HTTPPort == daemon.DefaultHTTPPort {
			return []int{daemon.DefaultHTTPPort}
		}
		return []int{rec.HTTPPort, daemon.DefaultHTTPPort}
	}
	return []int{daemon.DefaultHTTPPort}
}

// resolveDaemonPort probes the candidates in order and returns the first
// port whose daemon answers, plus every port tried (the not-running error
// names them so users can see where the command looked).
func resolveDaemonPort(cmd *cobra.Command) (port int, tried []int, ok bool) {
	for _, p := range daemonPortCandidates(cmd) {
		tried = append(tried, p)
		if daemonReachable(p) {
			return p, tried, true
		}
	}
	return 0, tried, false
}

// describePorts renders a tried-port list for error and help text.
func describePorts(tried []int) string {
	parts := make([]string, len(tried))
	for i, p := range tried {
		parts[i] = strconv.Itoa(p)
	}
	return strings.Join(parts, ", ")
}

// requireDaemonPort resolves the daemon HTTP port for a daemon-facing
// one-shot command and renders the standard not-running envelope (naming
// every port tried) when nothing answers.
func requireDaemonPort(cmd *cobra.Command) (int, error) {
	port, tried, ok := resolveDaemonPort(cmd)
	if !ok {
		return 0, jsonError(cmd, "DAEMON_NOT_RUNNING",
			fmt.Sprintf("no godot-ai daemon answers on http port(s): %s", describePorts(tried)),
			map[string]any{
				"hint":        "Run: godot-ai-cli launch --project <path>",
				"ports_tried": tried,
				"retryable":   true,
			})
	}
	return port, nil
}
