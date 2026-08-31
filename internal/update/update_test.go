package update

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mimajiushi/godot-ai-cli/internal/testutil/fakegithub"
)

// TestCompareVersions pins the semver precedence rules the update flow
// relies on: leading v stripped, numeric triple first, a pre-release older
// than the same stable triple (this is what makes the build-in "0.0.0-dev"
// older than any published stable), and §11 pre-release identifier rules.
func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.2.0", "0.2.0", 0},
		{"0.1.0", "0.1.0", 0},
		{"1.2", "1.2.0", 0}, // missing patch defaults to 0
		{"0.1.0-dev", "0.1.0", -1},
		{"0.1.0-dev", "0.2.0", -1},
		{"0.2.0", "0.1.0", 1},
		{"0.2.0", "0.10.0", -1}, // numeric, not lexical
		{"1.0.0-rc.1", "1.0.0", -1},
		{"1.0.0", "1.0.0-rc.1", 1},
		{"1.0.0-alpha", "1.0.0-alpha.1", -1},      // fewer identifiers rank lower
		{"1.0.0-alpha.1", "1.0.0-alpha.beta", -1}, // numeric ranks below alphanumeric
		{"1.0.0-beta.2", "1.0.0-beta.11", -1},
		{"1.0.0+build.5", "1.0.0", 0}, // build metadata ignored
	}
	for _, c := range cases {
		t.Run(c.a+"_vs_"+c.b, func(t *testing.T) {
			got, err := CompareVersions(c.a, c.b)
			if err != nil {
				t.Fatalf("CompareVersions(%q, %q): %v", c.a, c.b, err)
			}
			if got != c.want {
				t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
			}
		})
	}
}

// TestCompareVersionsInvalid: unparseable input is an error, never a
// silently wrong update decision.
func TestCompareVersionsInvalid(t *testing.T) {
	for _, s := range []string{"garbage", "1", "1.2.3.4", "1.x.0", ""} {
		if _, err := CompareVersions(s, "1.0.0"); err == nil {
			t.Errorf("CompareVersions(%q, 1.0.0) succeeded, want an error", s)
		}
	}
}

// TestFetchLatestRelease covers the happy path plus the error shapes:
// 404 (no releases yet), another HTTP failure, and malformed JSON.
func TestFetchLatestRelease(t *testing.T) {
	t.Run("parses the release payload", func(t *testing.T) {
		server := fakegithub.New(t, "v0.2.0", map[string][]byte{
			"godot-ai-cli-0.2.0-windows-amd64.zip": []byte("zip"),
		})
		rel, err := FetchLatestRelease(context.Background(), server.Client(), server.URL, "o", "r")
		if err != nil {
			t.Fatal(err)
		}
		if rel.TagName != "v0.2.0" || !strings.Contains(rel.HTMLURL, "/releases/tag/v0.2.0") {
			t.Errorf("release = %+v", rel)
		}
		if len(rel.Assets) != 1 || rel.Assets[0].Name != "godot-ai-cli-0.2.0-windows-amd64.zip" {
			t.Errorf("assets = %+v", rel.Assets)
		}
	})

	t.Run("draft releases are skipped", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"tag_name": "v0.3.0-beta.1", "draft": true, "assets": []any{}},
				{"tag_name": "v0.2.0", "draft": false, "assets": []any{}},
			})
		}))
		defer server.Close()
		rel, err := FetchLatestRelease(context.Background(), server.Client(), server.URL, "o", "r")
		if err != nil {
			t.Fatal(err)
		}
		if rel.TagName != "v0.2.0" {
			t.Errorf("draft was not skipped, got %q", rel.TagName)
		}
	})

	t.Run("empty list means no release published yet", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("[]"))
		}))
		defer server.Close()
		_, err := FetchLatestRelease(context.Background(), server.Client(), server.URL, "o", "r")
		assertCode(t, err, CodeCheckFailed)
		if err == nil || !strings.Contains(err.Error(), "no release has been published yet") {
			t.Errorf("expected the no-release message, got %v", err)
		}
	})

	t.Run("404 means no release published yet", func(t *testing.T) {
		server := httptest.NewServer(http.NotFoundHandler())
		defer server.Close()
		_, err := FetchLatestRelease(context.Background(), server.Client(), server.URL, "o", "r")
		assertCode(t, err, CodeCheckFailed)
	})

	t.Run("malformed JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("{not json"))
		}))
		defer server.Close()
		_, err := FetchLatestRelease(context.Background(), server.Client(), server.URL, "o", "r")
		assertCode(t, err, CodeCheckFailed)
	})

	t.Run("unreachable host", func(t *testing.T) {
		// A closed server's port refuses connections immediately.
		server := httptest.NewServer(http.NotFoundHandler())
		url := server.URL
		server.Close()
		_, err := FetchLatestRelease(context.Background(), server.Client(), url, "o", "r")
		assertCode(t, err, CodeCheckFailed)
	})
}

// TestSelectAssets covers platform matching, the no-matching-asset error,
// and the missing-checksums refusal.
func TestSelectAssets(t *testing.T) {
	rel := Release{TagName: "v0.2.0", Assets: []Asset{
		{Name: "godot-ai-cli-0.2.0-windows-amd64.zip"},
		{Name: "godot-ai-cli-0.2.0-linux-amd64.zip"},
		{Name: "godot-ai-cli-0.2.0-checksums.txt"},
	}}

	asset, sums, err := SelectAssets(rel, "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if asset.Name != "godot-ai-cli-0.2.0-linux-amd64.zip" || sums.Name != "godot-ai-cli-0.2.0-checksums.txt" {
		t.Errorf("selected %q / %q", asset.Name, sums.Name)
	}

	_, _, err = SelectAssets(rel, "darwin", "arm64")
	assertCode(t, err, CodeAssetNotFound)
	var uerr *Error
	if errors.As(err, &uerr) {
		avail, _ := uerr.Data["available_assets"].([]string)
		if len(avail) != 3 {
			t.Errorf("available_assets = %v", uerr.Data["available_assets"])
		}
	}

	_, _, err = SelectAssets(Release{TagName: "v0.2.0", Assets: []Asset{
		{Name: "godot-ai-cli-0.2.0-windows-amd64.zip"},
	}}, "windows", "amd64")
	assertCode(t, err, CodeChecksumInvalid)
}

// TestDownloadAndVerify covers the happy path, the hash mismatch (nothing
// returned), the missing checksum entry, and a failing download.
func TestDownloadAndVerify(t *testing.T) {
	zipData := fakegithub.Zip(t, "godot-ai-cli.exe", []byte("new-binary"))
	server := fakegithub.New(t, "v0.2.0", map[string][]byte{
		"godot-ai-cli-0.2.0-windows-amd64.zip": zipData,
		"godot-ai-cli-0.2.0-checksums.txt":     fakegithub.Checksums("godot-ai-cli-0.2.0-windows-amd64.zip", zipData),
	})
	rel, err := FetchLatestRelease(context.Background(), server.Client(), server.URL, "o", "r")
	if err != nil {
		t.Fatal(err)
	}
	asset, sums, err := SelectAssets(rel, "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}

	got, err := DownloadAndVerify(context.Background(), server.Client(), asset, sums)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(zipData) {
		t.Errorf("downloaded %d bytes, want %d", len(got), len(zipData))
	}

	t.Run("checksum mismatch", func(t *testing.T) {
		bad := Asset{Name: sums.Name, BrowserDownloadURL: server.URL + "/download/godot-ai-cli-0.2.0-checksums.txt"}
		// Point the asset at the checksums file: its hash cannot match its own entry.
		err := verifyChecksum(asset.Name, []byte("tampered"), mustDownload(t, server, bad))
		assertCode(t, err, CodeChecksumMismatch)
	})

	t.Run("missing checksum entry", func(t *testing.T) {
		err := verifyChecksum(asset.Name, zipData, []byte("deadbeef  other-file.zip\n"))
		assertCode(t, err, CodeChecksumInvalid)
	})

	t.Run("download 404", func(t *testing.T) {
		_, err := DownloadAndVerify(context.Background(), server.Client(),
			Asset{Name: "x.zip", BrowserDownloadURL: server.URL + "/download/nope.zip"}, sums)
		assertCode(t, err, CodeDownloadFailed)
	})
}

// mustDownload fetches one fake-github file for direct verifyChecksum tests.
func mustDownload(t *testing.T, server *httptest.Server, asset Asset) []byte {
	t.Helper()
	resp, err := server.Client().Get(asset.BrowserDownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestExtractBinary covers the platform binary name lookup and the
// archive-without-binary error.
func TestExtractBinary(t *testing.T) {
	zipData := fakegithub.Zip(t, "godot-ai-cli.exe", []byte("pe-binary"))
	got, err := ExtractBinary(zipData, "windows")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "pe-binary" {
		t.Errorf("extracted %q", got)
	}

	if _, err := ExtractBinary(zipData, "linux"); err == nil {
		t.Error("windows zip accepted for linux, want an error")
	} else {
		assertCode(t, err, CodeArchiveInvalid)
	}

	if _, err := ExtractBinary([]byte("not a zip"), "linux"); err == nil {
		t.Error("garbage zip accepted, want an error")
	}
}

// TestReplaceExecutableWindows exercises the rename-aside dance against a
// fake install dir: the running binary is not involved, so the Windows
// branch is fully testable on any host.
func TestReplaceExecutableWindows(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "godot-ai-cli.exe")
	if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A stale .old from an earlier update must not block the rename.
	if err := os.WriteFile(target+".old", []byte("ancient"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := ReplaceExecutable("windows", target, []byte("new-binary")); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, target, "new-binary")
	assertFileContent(t, target+".old", "old-binary")
}

// TestReplaceExecutableUnix exercises the temp-file + atomic rename branch.
func TestReplaceExecutableUnix(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "godot-ai-cli")
	if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := ReplaceExecutable("linux", target, []byte("new-binary")); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, target, "new-binary")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("staging temp file leaked into the install dir: %v", entries)
	}
}

// TestReplaceExecutableWindowsRollbackFailure: when the new-binary write
// fails AND the rollback rename also fails, the error must state that the
// previous binary survives at <exe>.old — that path is the only recovery.
func TestReplaceExecutableWindowsRollbackFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "godot-ai-cli.exe")
	if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Inject: the write of the new binary fails, and so does the rename
	// that would roll the install back.
	origWrite, origRename := writeFile, rename
	writeFile = func(string, []byte, os.FileMode) error { return errors.New("injected write failure") }
	rename = func(oldpath, newpath string) error {
		if strings.HasSuffix(oldpath, ".old") {
			return errors.New("injected rollback failure")
		}
		return origRename(oldpath, newpath)
	}
	t.Cleanup(func() { writeFile, rename = origWrite, origRename })

	err := ReplaceExecutable("windows", target, []byte("new-binary"))
	assertCode(t, err, CodeReplaceFailed)
	if !strings.Contains(err.Error(), target+".old") {
		t.Errorf("error does not name the surviving backup %s.old: %v", target, err)
	}
	if !strings.Contains(err.Error(), "survives") {
		t.Errorf("error does not state the binary survives: %v", err)
	}
	// The injected rollback failure left the old binary at <exe>.old.
	assertFileContent(t, target+".old", "old-binary")
}

// TestReplaceExecutableMissingTarget refuses to create a binary where no
// install exists (a mistyped --from must not drop a stray executable).
func TestReplaceExecutableMissingTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "godot-ai-cli.exe")
	err := ReplaceExecutable("windows", target, []byte("new-binary"))
	assertCode(t, err, CodeReplaceFailed)
}

// TestCleanupStaleBackup removes <exe>.old and keeps the binary.
func TestCleanupStaleBackup(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "godot-ai-cli.exe")
	if err := os.WriteFile(exe, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe+".old", []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	cleanupStaleBackup(exe)
	if _, err := os.Stat(exe + ".old"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".old still present: %v", err)
	}
	assertFileContent(t, exe, "bin")

	// No leftover: silently a no-op.
	cleanupStaleBackup(exe)
}

// TestRunUpToDate: same version → the already-up-to-date payload.
func TestRunUpToDate(t *testing.T) {
	server := fakegithub.New(t, "v0.2.0", nil)
	result, err := Run(context.Background(), Options{
		CurrentVersion: "0.2.0",
		BaseURL:        server.URL,
		HTTPClient:     server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "ok" || result["update_available"] != false {
		t.Errorf("result = %v", result)
	}
	if result["message"] != "godot-ai-cli is already up to date" {
		t.Errorf("message = %v", result["message"])
	}
	if result["current_version"] != "0.2.0" || result["latest_version"] != "0.2.0" {
		t.Errorf("versions = %v/%v", result["current_version"], result["latest_version"])
	}
}

// TestRunDevBuildIsOlderThanStable: the build-in "0.0.0-dev" must see the
// v0.1.0 stable release as an update.
func TestRunDevBuildIsOlderThanStable(t *testing.T) {
	server := fakegithub.New(t, "v0.1.0", nil)
	result, err := Run(context.Background(), Options{
		CurrentVersion: "0.0.0-dev",
		BaseURL:        server.URL,
		HTTPClient:     server.Client(),
		IsTerminal:     false,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Non-TTY without --yes: declined before any download happens.
	if result["status"] != "cancelled" || len(result) != 1 {
		t.Errorf("result = %v, want a bare cancelled marker", result)
	}
}

// TestRunNonTTYDefaultsToNo: piped stdin without --yes never updates.
func TestRunNonTTYDefaultsToNo(t *testing.T) {
	server := fakegithub.New(t, "v0.2.0", nil)
	result, err := Run(context.Background(), Options{
		CurrentVersion: "0.1.0",
		BaseURL:        server.URL,
		HTTPClient:     server.Client(),
		IsTerminal:     false,
		In:             strings.NewReader("y\n"), // even a piped "y" is ignored
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "cancelled" || len(result) != 1 {
		t.Errorf("result = %v, want a bare cancelled marker", result)
	}
}

// TestRunDeclinedInteractive: a TTY user answering "n" gets the cancelled
// payload WITH the release details.
func TestRunDeclinedInteractive(t *testing.T) {
	server := fakegithub.New(t, "v0.2.0", nil)
	result, err := Run(context.Background(), Options{
		CurrentVersion: "0.1.0",
		BaseURL:        server.URL,
		HTTPClient:     server.Client(),
		IsTerminal:     true,
		In:             strings.NewReader("n\n"),
		PromptOut:      io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "cancelled" || result["update_available"] != true {
		t.Errorf("result = %v", result)
	}
	if result["current_version"] != "0.1.0" || result["latest_version"] != "0.2.0" {
		t.Errorf("versions = %v/%v", result["current_version"], result["latest_version"])
	}
	if url, _ := result["release_notes_url"].(string); !strings.Contains(url, "/releases/tag/v0.2.0") {
		t.Errorf("release_notes_url = %v", result["release_notes_url"])
	}
}

// TestRunAcceptedViaPrompt: a TTY user answering "y" updates the fake
// install dir end to end.
func TestRunAcceptedViaPrompt(t *testing.T) {
	runAccepted(t, Options{IsTerminal: true, In: strings.NewReader("y\n")})
}

// TestRunAcceptedViaYesFlag: --yes skips the prompt entirely.
func TestRunAcceptedViaYesFlag(t *testing.T) {
	runAccepted(t, Options{AssumeYes: true})
}

// runAccepted drives the happy path against a fake install dir and checks
// the Windows self-replace mechanics (rename to .old, new binary in place).
func runAccepted(t *testing.T, extra Options) {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "godot-ai-cli.exe")
	if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	zipData := fakegithub.Zip(t, "godot-ai-cli.exe", []byte("new-binary"))
	server := fakegithub.New(t, "v0.2.0", map[string][]byte{
		"godot-ai-cli-0.2.0-windows-amd64.zip": zipData,
		"godot-ai-cli-0.2.0-checksums.txt":     fakegithub.Checksums("godot-ai-cli-0.2.0-windows-amd64.zip", zipData),
	})

	opts := Options{
		CurrentVersion: "0.1.0",
		BaseURL:        server.URL,
		HTTPClient:     server.Client(),
		GOOS:           "windows",
		GOARCH:         "amd64",
		InstallDir:     dir,
		PromptOut:      io.Discard,
	}
	// Let the caller override the prompt-related fields.
	opts.AssumeYes = extra.AssumeYes
	opts.IsTerminal = extra.IsTerminal
	if extra.In != nil {
		opts.In = extra.In
	}

	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "updated" || result["restart_required"] != true {
		t.Errorf("result = %v", result)
	}
	if result["path"] != target || result["previous_binary"] != target+".old" {
		t.Errorf("paths = %v / %v", result["path"], result["previous_binary"])
	}
	assertFileContent(t, target, "new-binary")
	assertFileContent(t, target+".old", "old-binary")
}

// TestRunChecksumMismatchLeavesInstallUntouched: a hash mismatch aborts
// before the install dir is touched — no .old, original content intact.
func TestRunChecksumMismatchLeavesInstallUntouched(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "godot-ai-cli.exe")
	if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	zipData := fakegithub.Zip(t, "godot-ai-cli.exe", []byte("new-binary"))
	server := fakegithub.New(t, "v0.2.0", map[string][]byte{
		"godot-ai-cli-0.2.0-windows-amd64.zip": zipData,
		"godot-ai-cli-0.2.0-checksums.txt":     []byte("0000000000000000000000000000000000000000000000000000000000000000  godot-ai-cli-0.2.0-windows-amd64.zip\n"),
	})

	_, err := Run(context.Background(), Options{
		CurrentVersion: "0.1.0",
		BaseURL:        server.URL,
		HTTPClient:     server.Client(),
		GOOS:           "windows",
		GOARCH:         "amd64",
		InstallDir:     dir,
		AssumeYes:      true,
	})
	assertCode(t, err, CodeChecksumMismatch)
	assertFileContent(t, target, "old-binary")
	if _, statErr := os.Stat(target + ".old"); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf(".old exists despite the aborted update: %v", statErr)
	}
}

// TestRunNoMatchingAsset: a release without this platform's asset is a
// clear error, not a confusing download failure.
func TestRunNoMatchingAsset(t *testing.T) {
	zipData := fakegithub.Zip(t, "godot-ai-cli", []byte("new-binary"))
	server := fakegithub.New(t, "v0.2.0", map[string][]byte{
		"godot-ai-cli-0.2.0-linux-amd64.zip": zipData,
		"godot-ai-cli-0.2.0-checksums.txt":   fakegithub.Checksums("godot-ai-cli-0.2.0-linux-amd64.zip", zipData),
	})

	_, err := Run(context.Background(), Options{
		CurrentVersion: "0.1.0",
		BaseURL:        server.URL,
		HTTPClient:     server.Client(),
		GOOS:           "windows",
		GOARCH:         "amd64",
		AssumeYes:      true,
	})
	assertCode(t, err, CodeAssetNotFound)
	var uerr *Error
	if errors.As(err, &uerr) {
		if uerr.Data["goos"] != "windows" || uerr.Data["goarch"] != "amd64" {
			t.Errorf("error data = %v", uerr.Data)
		}
	}
}

// TestRunCheckFailed: a 404 from the API (no releases yet) fails the check.
func TestRunCheckFailed(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	_, err := Run(context.Background(), Options{
		CurrentVersion: "0.1.0",
		BaseURL:        server.URL,
		HTTPClient:     server.Client(),
	})
	assertCode(t, err, CodeCheckFailed)
}

// assertCode fails the test unless err is an *Error with the given code.
func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want code %s", code)
	}
	var uerr *Error
	if !errors.As(err, &uerr) {
		t.Fatalf("error %v is not an *update.Error", err)
	}
	if uerr.Code != code {
		t.Fatalf("code = %s, want %s (message: %s)", uerr.Code, code, uerr.Message)
	}
}

// assertFileContent fails the test unless the file holds exactly want.
func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Errorf("%s = %q, want %q", path, data, want)
	}
}
