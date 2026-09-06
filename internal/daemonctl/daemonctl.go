// Package daemonctl manages the lifecycle of the godot-ai-cli backend
// daemon: probing whether one is already running and spawning one
// detached when it is not.
package daemonctl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/mimajiushi/godot-ai-cli/internal/daemon"
	"github.com/mimajiushi/godot-ai-cli/internal/pluginmeta"
)

const (
	// probeTimeout bounds one status probe; a local daemon answers in
	// microseconds, so 300ms already means "not there".
	probeTimeout = 300 * time.Millisecond
	// spawnWait bounds the total wait for a freshly spawned daemon to come
	// up (cold binary start + bind + pid file).
	spawnWait = 10 * time.Second
	// spawnPollInterval is the delay between readiness polls after spawn.
	spawnPollInterval = 200 * time.Millisecond
)

// probeClient is shared by all status probes.
var probeClient = &http.Client{Timeout: probeTimeout}

// spawnServe starts `godot-ai-cli serve` detached. It is a variable so
// tests can substitute the standard TestHelperProcess pattern.
var spawnServe = defaultSpawnServe

// ForeignServerError reports that an incompatible godot-ai server — e.g.
// the upstream Python backend — occupies the port: it answers
// /godot-ai/status with name "godot-ai" but has no /godot-ai/cli/* API.
// It is never killed; the remedy is stopping it or choosing other ports.
type ForeignServerError struct {
	HTTPPort int
}

// Error implements the error interface.
func (e *ForeignServerError) Error() string {
	return fmt.Sprintf(
		"an incompatible godot-ai (Python) server already occupies port %d: it answers /godot-ai/status but provides no /godot-ai/cli API — stop it (e.g. from the Godot plugin dock) or pass --http-port/--ws-port to run alongside it; refusing to kill it",
		e.HTTPPort,
	)
}

// DaemonMismatchError reports that an existing godot-ai-cli daemon answers
// on the requested HTTP port but runs with a different WS port or an
// INCOMPATIBLE plugin version (major.minor differs) than requested.
// Adopting it would make the plugin reject the handshake
// (ws_port_mismatch / version mismatch), so the caller must stop the
// running daemon first instead of silently inheriting its config. A
// patch-level version drift is NOT a mismatch: the daemon is adopted and
// the caller surfaces a stale hint (same major.minor rule as the
// plugin↔daemon handshake).
type DaemonMismatchError struct {
	HTTPPort         int
	RunningWSPort    int
	RequestedWSPort  int
	RunningVersion   string
	RequestedVersion string
}

// Error implements the error interface, naming every mismatching value.
func (e *DaemonMismatchError) Error() string {
	var mismatch string
	switch {
	case e.RunningWSPort != e.RequestedWSPort && e.RunningVersion != e.RequestedVersion:
		mismatch = fmt.Sprintf("it runs with ws_port %d (requested %d) and version %s (requested %s)",
			e.RunningWSPort, e.RequestedWSPort, e.RunningVersion, e.RequestedVersion)
	case e.RunningWSPort != e.RequestedWSPort:
		mismatch = fmt.Sprintf("it runs with ws_port %d (requested %d)", e.RunningWSPort, e.RequestedWSPort)
	default:
		mismatch = fmt.Sprintf("it runs with version %s (requested %s)", e.RunningVersion, e.RequestedVersion)
	}
	return fmt.Sprintf(
		"a godot-ai-cli daemon already runs on http port %d but %s — adopting it would break the plugin handshake; run `godot-ai-cli stop --http-port %d` first, then launch again",
		e.HTTPPort, mismatch, e.HTTPPort,
	)
}

// probeState classifies what answers on the HTTP port.
type probeState int

const (
	probeUnreachable    probeState = iota // nothing answers
	probeOurs                             // our daemon: status + cli/health ok
	probeForeignGodotAI                   // godot-ai status but no /cli API (upstream Python)
	probeForeignOther                     // some other process entirely
)

// probeInfo captures what answers on the HTTP port, including the details
// an adoption decision needs (running ws_port and server version).
type probeInfo struct {
	state   probeState
	name    string // /godot-ai/status name field
	wsPort  int    // /godot-ai/status ws_port field
	version string // /godot-ai/cli/health version field
}

// probe classifies the occupant of httpPort.
func probe(httpPort int) probeInfo {
	name, wsPort, answered := probeStatus(httpPort)
	if !answered {
		return probeInfo{state: probeUnreachable}
	}
	info := probeInfo{name: name, wsPort: wsPort}
	if name != "godot-ai" {
		info.state = probeForeignOther
		return info
	}
	// A compatible-looking answer is not enough: the upstream Python
	// server names itself godot-ai too. Only OUR daemon serves the
	// /godot-ai/cli/* API.
	version, ok := healthProbe(httpPort)
	if !ok {
		info.state = probeForeignGodotAI
		return info
	}
	info.state = probeOurs
	info.version = version
	return info
}

// healthProbe reports whether /godot-ai/cli/health answers with status ok,
// returning the daemon's advertised version.
func healthProbe(httpPort int) (version string, ok bool) {
	resp, err := probeClient.Get(fmt.Sprintf("http://127.0.0.1:%d/godot-ai/cli/health", httpPort))
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	var body struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", false
	}
	if body.Status != "ok" {
		return "", false
	}
	return body.Version, true
}

// foreignOccupantError maps a foreign probe state to the right error.
func foreignOccupantError(state probeState, httpPort int, name string) error {
	if state == probeForeignGodotAI {
		return &ForeignServerError{HTTPPort: httpPort}
	}
	return fmt.Errorf("port %d is occupied by a foreign process (probe answered name=%q, want %q); refusing to kill it — pick another --http-port", httpPort, name, "godot-ai")
}

// EnsureRunning guarantees a compatible daemon answers on cfg.HTTPPort.
//
// If our daemon already runs (status name "godot-ai" AND a healthy
// /godot-ai/cli/health) it is adopted — but only when its ws_port matches
// and its version is major.minor-compatible with the requested
// configuration; a mismatch yields a DaemonMismatchError because adopting
// it would break the plugin handshake. A patch-level version drift is
// adopted (the CLI warns about the staleness instead).
// A godot-ai-named server WITHOUT the /cli API is the upstream Python
// backend and yields a ForeignServerError; any other occupant yields a
// generic refusal. Neither is ever killed. Only a truly unreachable port
// triggers the spawn path: the current executable as
// `serve --http-port X --ws-port Y` DETACHED, then up to 10s of readiness
// polls.
func EnsureRunning(ctx context.Context, cfg daemon.Config) (bool, error) {
	httpPort := cfg.HTTPPort
	if httpPort == 0 {
		httpPort = daemon.DefaultHTTPPort
	}
	wsPort := cfg.WSPort
	if wsPort == 0 {
		wsPort = daemon.DefaultWSPort
	}
	version := cfg.Version
	if version == "" {
		version = pluginmeta.PluginVersion()
	}

	switch info := probe(httpPort); info.state {
	case probeOurs:
		if err := adoptionMismatch(httpPort, wsPort, version, info); err != nil {
			return false, err
		}
		return true, nil
	case probeForeignGodotAI:
		return false, &ForeignServerError{HTTPPort: httpPort}
	case probeForeignOther:
		return false, foreignOccupantError(probeForeignOther, httpPort, info.name)
	}

	if err := spawnServe(httpPort, wsPort); err != nil {
		return false, fmt.Errorf("spawn daemon: %w", err)
	}

	deadline := time.Now().Add(spawnWait)
	for {
		switch info := probe(httpPort); info.state {
		case probeOurs:
			if err := adoptionMismatch(httpPort, wsPort, version, info); err != nil {
				return false, err
			}
			return true, nil
		case probeForeignGodotAI, probeForeignOther:
			// The spawn lost the bind race (or the port was foreign all
			// along): never kill the occupant.
			return false, foreignOccupantError(info.state, httpPort, info.name)
		}
		if time.Now().After(deadline) {
			return false, fmt.Errorf("daemon did not become ready on port %d within %s; check that `godot-ai-cli serve` starts cleanly", httpPort, spawnWait)
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(spawnPollInterval):
		}
	}
}

// adoptionMismatch rejects adopting a running daemon whose WS port differs
// from the requested configuration, or whose plugin version is INCOMPATIBLE
// (major.minor drift — the same gate the plugin handshake enforces). A
// patch-level version drift is adopted: the CLI surfaces a stale hint
// instead of hard-failing every launch after a CLI update. Fields the
// daemon does not advertise (0 / "") cannot prove a mismatch and are
// tolerated.
func adoptionMismatch(httpPort, wsPort int, version string, info probeInfo) error {
	wsMismatch := info.wsPort != 0 && info.wsPort != wsPort
	versionMismatch := !versionCompatible(info.version, version)
	if !wsMismatch && !versionMismatch {
		return nil
	}
	return &DaemonMismatchError{
		HTTPPort:         httpPort,
		RunningWSPort:    info.wsPort,
		RequestedWSPort:  wsPort,
		RunningVersion:   info.version,
		RequestedVersion: version,
	}
}

// versionCompatible reports whether a running daemon's advertised version
// may be adopted for a launch requesting version `requested`: identical or
// major.minor-equal (patch drift freely accepted, mirroring the plugin
// handshake gate). An unadvertised ("") version cannot prove a mismatch and
// is tolerated; an UNPARSEABLE side keeps the legacy exact-equality refusal
// — the version gate must never guess.
func versionCompatible(running, requested string) bool {
	if running == "" || running == requested {
		return true
	}
	r, rerr := pluginmeta.ParseSemver(running)
	q, qerr := pluginmeta.ParseSemver(requested)
	if rerr != nil || qerr != nil {
		return false
	}
	return pluginmeta.Compatible(r, q)
}

// probeStatus GETs /godot-ai/status. answered reports whether ANY HTTP
// response arrived (even a foreign one); name and wsPort are the body's
// fields (zero values when absent).
func probeStatus(httpPort int) (name string, wsPort int, answered bool) {
	resp, err := probeClient.Get(fmt.Sprintf("http://127.0.0.1:%d/godot-ai/status", httpPort))
	if err != nil {
		return "", 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, true // HTTP answered, but not a healthy daemon
	}
	var body struct {
		Name   string `json:"name"`
		WSPort int    `json:"ws_port"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", 0, true
	}
	return body.Name, body.WSPort, true
}

// defaultSpawnServe starts the current executable as a detached
// `serve --http-port X --ws-port Y` process that outlives the caller.
func defaultSpawnServe(httpPort, wsPort int) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "serve",
		"--http-port", strconv.Itoa(httpPort),
		"--ws-port", strconv.Itoa(wsPort),
	)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = detachedSysProcAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	// The daemon is meant to outlive us; drop the handle without waiting.
	return cmd.Process.Release()
}
