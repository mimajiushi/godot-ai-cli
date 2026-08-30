package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mimajiushi/godot-ai-cli/internal/testutil/fakegithub"
	"github.com/mimajiushi/godot-ai-cli/internal/update"
)

// setUpdateAPIBase points the update command at a test server and restores
// the real GitHub API root afterwards.
func setUpdateAPIBase(t *testing.T, url string) {
	t.Helper()
	old := updateAPIBase
	updateAPIBase = url
	t.Cleanup(func() { updateAPIBase = old })
}

// runUpdateCmd executes `update <args...>` and returns the decoded stdout
// payload plus the Execute error.
func runUpdateCmd(t *testing.T, args ...string) (map[string]any, error) {
	t.Helper()
	cmd := NewRootCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(append([]string{"update"}, args...))
	err := cmd.Execute()
	out := map[string]any{}
	if decErr := json.Unmarshal(buf.Bytes(), &out); decErr != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", decErr, buf.String())
	}
	return out, err
}

// TestUpdateCommandAlreadyUpToDate: the release is older than the build-in
// dev version → the up-to-date payload.
func TestUpdateCommandAlreadyUpToDate(t *testing.T) {
	server := fakegithub.New(t, "v0.0.1", nil)
	setUpdateAPIBase(t, server.URL)

	out, err := runUpdateCmd(t)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if out["status"] != "ok" || out["update_available"] != false {
		t.Errorf("out = %v", out)
	}
	if out["message"] != "godot-ai-cli is already up to date" {
		t.Errorf("message = %v", out["message"])
	}
	if out["current_version"] == "" || out["latest_version"] != "0.0.1" {
		t.Errorf("versions = %v/%v", out["current_version"], out["latest_version"])
	}
}

// TestUpdateCommandCancelledNonTTY: a newer release but no terminal → the
// bare cancelled marker and no prompt on stdout.
func TestUpdateCommandCancelledNonTTY(t *testing.T) {
	server := fakegithub.New(t, "v9.9.9", nil)
	setUpdateAPIBase(t, server.URL)

	out, err := runUpdateCmd(t)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if out["status"] != "cancelled" || len(out) != 1 {
		t.Errorf("out = %v, want a bare cancelled marker", out)
	}
}

// TestUpdateCommandAccepted exercises the happy path against a fake install
// dir: download, checksum verification, and the platform replace.
func TestUpdateCommandAccepted(t *testing.T) {
	binName := update.BinaryName(runtime.GOOS)
	zipData := fakegithub.Zip(t, binName, []byte("new-binary"))
	assetName := fmt.Sprintf("godot-ai-cli-9.9.9-%s-%s.zip", runtime.GOOS, runtime.GOARCH)
	server := fakegithub.New(t, "v9.9.9", map[string][]byte{
		assetName:                          zipData,
		"godot-ai-cli-9.9.9-checksums.txt": fakegithub.Checksums(assetName, zipData),
	})
	setUpdateAPIBase(t, server.URL)

	dir := t.TempDir()
	target := filepath.Join(dir, binName)
	if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := runUpdateCmd(t, "--yes", "--from", dir)
	if err != nil {
		t.Fatalf("update --yes --from: %v", err)
	}
	if out["status"] != "updated" || out["restart_required"] != true {
		t.Errorf("out = %v", out)
	}
	if out["latest_version"] != "9.9.9" || out["path"] != target {
		t.Errorf("out = %v", out)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new-binary" {
		t.Errorf("installed binary = %q", data)
	}
	if runtime.GOOS == "windows" {
		old, err := os.ReadFile(target + ".old")
		if err != nil {
			t.Fatalf("previous binary not set aside: %v", err)
		}
		if string(old) != "old-binary" {
			t.Errorf(".old = %q", old)
		}
		if out["previous_binary"] != target+".old" {
			t.Errorf("previous_binary = %v", out["previous_binary"])
		}
	}
}

// TestUpdateCommandChecksumMismatch: the error envelope reaches stdout and
// the fake install is untouched.
func TestUpdateCommandChecksumMismatch(t *testing.T) {
	binName := update.BinaryName(runtime.GOOS)
	zipData := fakegithub.Zip(t, binName, []byte("new-binary"))
	assetName := fmt.Sprintf("godot-ai-cli-9.9.9-%s-%s.zip", runtime.GOOS, runtime.GOARCH)
	server := fakegithub.New(t, "v9.9.9", map[string][]byte{
		assetName:                          zipData,
		"godot-ai-cli-9.9.9-checksums.txt": []byte("0000000000000000000000000000000000000000000000000000000000000000  " + assetName + "\n"),
	})
	setUpdateAPIBase(t, server.URL)

	dir := t.TempDir()
	target := filepath.Join(dir, binName)
	if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := runUpdateCmd(t, "--yes", "--from", dir)
	if err == nil {
		t.Fatal("checksum mismatch succeeded, want an error")
	}
	errObj, ok := out["error"].(map[string]any)
	if !ok {
		t.Fatalf("out = %v", out)
	}
	if out["status"] != "error" || errObj["code"] != "UPDATE_CHECKSUM_MISMATCH" {
		t.Errorf("out = %v", out)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "old-binary" {
		t.Errorf("install was modified despite the mismatch: %q", data)
	}
}

// TestUpdateCommandCheckFailed: a 404 API answer becomes the standard
// envelope and a non-zero exit.
func TestUpdateCommandCheckFailed(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	setUpdateAPIBase(t, server.URL)

	out, err := runUpdateCmd(t)
	if err == nil {
		t.Fatal("update against a 404 API succeeded, want an error")
	}
	errObj, ok := out["error"].(map[string]any)
	if !ok {
		t.Fatalf("out = %v", out)
	}
	if out["status"] != "error" || errObj["code"] != "UPDATE_CHECK_FAILED" {
		t.Errorf("out = %v", out)
	}
}
