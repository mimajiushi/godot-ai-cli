package cli

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestServeStartFailurePrintsError: when the daemon cannot bind (port
// occupied), serve must print the standard error envelope with
// SERVE_START_FAILED and exit non-zero instead of returning a bare error.
func TestServeStartFailurePrintsError(t *testing.T) {
	// Occupy the HTTP port so daemon.Start fails immediately.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	httpPort := ln.Addr().(*net.TCPAddr).Port

	wsLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	wsPort := wsLn.Addr().(*net.TCPAddr).Port
	_ = wsLn.Close()

	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"serve", "--http-port", strconv.Itoa(httpPort), "--ws-port", strconv.Itoa(wsPort)})

	if err := cmd.Execute(); err == nil {
		t.Fatal("serve on an occupied port succeeded, want an error")
	}
	if !strings.Contains(buf.String(), "SERVE_START_FAILED") {
		t.Errorf("output missing SERVE_START_FAILED envelope:\n%s", buf.String())
	}
}

// TestServeRejectsPortZero: port 0 binds an ephemeral port the plugin can
// never find, so the CLI command rejects it up front (daemon.Start keeps
// 0-for-tests behavior).
func TestServeRejectsPortZero(t *testing.T) {
	for _, args := range [][]string{
		{"serve", "--http-port", "0", "--ws-port", "9500"},
		{"serve", "--http-port", "8000", "--ws-port", "0"},
	} {
		cmd := NewRootCommand()
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Errorf("serve %v succeeded, want a rejection", args)
			continue
		}
		if !strings.Contains(buf.String(), "INVALID_PARAMS") ||
			!strings.Contains(buf.String(), "non-zero") {
			t.Errorf("serve %v: output missing the INVALID_PARAMS envelope:\n%s", args, buf.String())
		}
	}
}

// TestWaitForSessionMalformedPayload: a daemon answering with a wrongly
// shaped sessions payload must produce an error, never a panic.
func TestWaitForSessionMalformedPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"sessions":[["bad"]]}`))
	}))
	defer server.Close()
	port := testServerPort(t, server)

	_, err := waitForSession(context.Background(), port, "/any/project", 2*time.Second)
	if err == nil {
		t.Fatal("waitForSession accepted a malformed session entry, want an error")
	}
}

// TestWaitForSessionReturnsSession is the happy path: one listed session
// for the requested project is returned immediately.
func TestWaitForSessionReturnsSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"sessions":[{"session_id":"s1","editor_pid":4321,"active":true,"project_path":"/any/project/"}]}`))
	}))
	defer server.Close()
	port := testServerPort(t, server)

	session, err := waitForSession(context.Background(), port, "/any/project", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if session["session_id"] != "s1" {
		t.Errorf("session = %v", session)
	}
}

// TestWaitForSessionIgnoresOtherProjects: with several projects sharing one
// daemon, a session belonging to a DIFFERENT project must never satisfy the
// wait — otherwise launch reports ready while its own editor never
// connected (the multi-project launch bug).
func TestWaitForSessionIgnoresOtherProjects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"sessions":[{"session_id":"other@1","active":true,"project_path":"/other/project/"}]}`))
	}))
	defer server.Close()
	port := testServerPort(t, server)

	_, err := waitForSession(context.Background(), port, "/my/project", 600*time.Millisecond)
	if err == nil {
		t.Fatal("waitForSession returned on another project's session, want a timeout")
	}
}

// TestWaitForSessionPrefersOwnProject: with a foreign session active, the
// wait must still return OUR project's (inactive) session.
func TestWaitForSessionPrefersOwnProject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"sessions":[` +
			`{"session_id":"other@1","active":true,"project_path":"/other/project/"},` +
			`{"session_id":"mine@2","active":false,"project_path":"/my/project/"}` +
			`]}`))
	}))
	defer server.Close()
	port := testServerPort(t, server)

	session, err := waitForSession(context.Background(), port, "/my/project", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if session["session_id"] != "mine@2" {
		t.Errorf("session = %v, want mine@2", session)
	}
}

// TestSameProjectPath pins the normalization: separators, trailing slashes
// and empty inputs. (Case-insensitivity is Windows-only and covered by the
// conditional case below.)
func TestSameProjectPath(t *testing.T) {
	cases := []struct {
		sessionPath, projectDir string
		want                    bool
	}{
		{"/a/proj/", "/a/proj", true},
		{"/a/proj", "/a/proj/", true},
		{`C:\games\rpg\`, `C:/games/rpg`, true},
		{"/a/proj/", "/a/other", false},
		{"", "/a/proj", false},
		{"/a/proj/", "", false},
		{"/a/proj2/", "/a/proj", false}, // prefix must not match
	}
	for _, c := range cases {
		if got := sameProjectPath(c.sessionPath, c.projectDir); got != c.want {
			t.Errorf("sameProjectPath(%q, %q) = %v, want %v", c.sessionPath, c.projectDir, got, c.want)
		}
	}
	if runtime.GOOS == "windows" && !sameProjectPath(`C:\Games\RPG\`, `c:/games/rpg`) {
		t.Error("sameProjectPath must be case-insensitive on Windows")
	}
}

// TestFindProjectSession: only entries whose project_path matches are
// returned; entries without a string project_path never match.
func TestFindProjectSession(t *testing.T) {
	list := []any{
		map[string]any{"session_id": "foreign@1", "project_path": "/other/"},
		"not-a-map",
		map[string]any{"session_id": "no-path@2"},
		map[string]any{"session_id": "mine@3", "project_path": "/mine/"},
	}
	got := findProjectSession(list, "/mine")
	if got == nil || got["session_id"] != "mine@3" {
		t.Errorf("findProjectSession = %v, want mine@3", got)
	}
	if got := findProjectSession(list, "/absent"); got != nil {
		t.Errorf("findProjectSession for absent project = %v, want nil", got)
	}
	if got := findProjectSession(nil, "/mine"); got != nil {
		t.Errorf("findProjectSession(nil) = %v, want nil", got)
	}
}

// testServerPort extracts the port from an httptest server's URL.
func testServerPort(t *testing.T, server *httptest.Server) int {
	t.Helper()
	_, portStr, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	return port
}
