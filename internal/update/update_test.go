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
	"strconv"
	"strings"
	"testing"
	"time"

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
	// Non-TTY without --yes: declined before any download happens, but the
	// payload still reports the available release (see TestRunNonTTYDefaultsToNo).
	if result["status"] != "cancelled" || result["update_available"] != true {
		t.Errorf("result = %v", result)
	}
	if result["latest_version"] != "0.1.0" {
		t.Errorf("latest_version = %v", result["latest_version"])
	}
}

// TestRunNonTTYDefaultsToNo: piped stdin without --yes never updates — but
// the cancelled payload must carry the release details and the --yes hint,
// or a script/agent caller cannot tell what happened or how to proceed.
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
	if result["status"] != "cancelled" || result["update_available"] != true {
		t.Errorf("result = %v", result)
	}
	if result["current_version"] != "0.1.0" || result["latest_version"] != "0.2.0" {
		t.Errorf("versions = %v/%v", result["current_version"], result["latest_version"])
	}
	if url, _ := result["release_notes_url"].(string); !strings.Contains(url, "/releases/tag/v0.2.0") {
		t.Errorf("release_notes_url = %v", result["release_notes_url"])
	}
	if msg, _ := result["message"].(string); !strings.Contains(msg, "--yes") {
		t.Errorf("message does not point at --yes: %v", result["message"])
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

// TestDownloadTruncatedStreamRetries：声明 100 字节只收到 30 的连接中断
// （TUN/fake-ip 现场的典型形状）必须重试到 3 次，且最终错误的 data 带
// url/http_status/content_length/bytes_read/attempts，message 说明中断
// 位置与下一步（需求 update-behind-tun-fakeip §4.2/4.3）。
func TestDownloadTruncatedStreamRetries(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, 30)) // 声明 100 实发 30 后挂起关闭
	}))
	defer server.Close()

	_, err := DownloadAndVerify(context.Background(), server.Client(),
		Asset{Name: "a.zip", BrowserDownloadURL: server.URL + "/a.zip"},
		Asset{Name: "sums.txt", BrowserDownloadURL: server.URL + "/sums.txt"})
	if err == nil {
		t.Fatal("truncated download must fail")
	}
	derr, ok := err.(*Error)
	if !ok || derr.Code != CodeDownloadFailed {
		t.Fatalf("err = %v", err)
	}
	if hits != downloadAttempts {
		t.Errorf("hits = %d, want %d attempts", hits, downloadAttempts)
	}
	for _, key := range []string{"url", "http_status", "content_length", "bytes_read", "attempts"} {
		if _, present := derr.Data[key]; !present {
			t.Errorf("diag missing %s: %v", key, derr.Data)
		}
	}
	if derr.Data["http_status"] != 200 || derr.Data["content_length"] != int64(100) ||
		derr.Data["bytes_read"] != int64(30) || derr.Data["attempts"] != downloadAttempts {
		t.Errorf("diag = %v", derr.Data)
	}
	// 文案里的中断位置随平台/Go 版本可能呈现不同措辞，字段断言严格、文案只钉关键词。
	if !strings.Contains(derr.Message, "字节处中断") || !strings.Contains(derr.Message, "--proxy") {
		t.Errorf("message = %q", derr.Message)
	}
}

// TestDownloadNotFoundNoRetry：4xx 是确定性失败，重试无意义——只试 1 次。
func TestDownloadNotFoundNoRetry(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.NotFound(w, r)
	}))
	defer server.Close()

	_, err := DownloadAndVerify(context.Background(), server.Client(),
		Asset{Name: "a.zip", BrowserDownloadURL: server.URL + "/a.zip"},
		Asset{Name: "sums.txt", BrowserDownloadURL: server.URL + "/sums.txt"})
	derr, ok := err.(*Error)
	if !ok || derr.Code != CodeDownloadFailed {
		t.Fatalf("err = %v", err)
	}
	if hits != 1 {
		t.Errorf("hits = %d, want 1 (no retry on 4xx)", hits)
	}
	if derr.Data["http_status"] != 404 || derr.Data["attempts"] != 1 {
		t.Errorf("diag = %v", derr.Data)
	}
}

// TestDownloadRedirectHostRecorded：302 落点（GitHub 资产的
// objects.githubusercontent.com 形态）必须进诊断 data。
func TestDownloadRedirectHostRecorded(t *testing.T) {
	var finalHits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			finalHits++
			w.Header().Set("Content-Length", "50")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(make([]byte, 10)) // 同样中断，触发带诊断的错误
		}
	}))
	defer server.Close()

	_, err := DownloadAndVerify(context.Background(), server.Client(),
		Asset{Name: "a.zip", BrowserDownloadURL: server.URL + "/start"},
		Asset{Name: "sums.txt", BrowserDownloadURL: server.URL + "/sums.txt"})
	derr, ok := err.(*Error)
	if !ok {
		t.Fatalf("err = %v", err)
	}
	host, _ := derr.Data["redirect_host"].(string)
	if !strings.Contains(host, "127.0.0.1") || finalHits == 0 {
		t.Errorf("redirect_host = %q, finalHits = %d", host, finalHits)
	}
}

// TestResolveProxy：显式 URL 原样返回；auto 命中环境变量；环境变量缺失时
// 返回未命中或注册表值（本机是否配了系统代理不可控，两种都合法）。
func TestResolveProxy(t *testing.T) {
	if got, ok := ResolveProxy("http://127.0.0.1:7897"); !ok || got != "http://127.0.0.1:7897" {
		t.Errorf("explicit = %q, %v", got, ok)
	}
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:7890")
	if got, ok := ResolveProxy("auto"); !ok || got != "http://127.0.0.1:7890" {
		t.Errorf("auto+env = %q, %v", got, ok)
	}
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")
	got, ok := ResolveProxy("auto")
	if ok && !strings.HasPrefix(got, "http://") {
		t.Errorf("auto without env = %q, %v", got, ok)
	}
	if _, ok := ResolveProxy(""); ok {
		t.Error("empty proxy must stay default")
	}
}

// TestRunCheckOnly：--check 只回答可用性（status ok + update_available +
// release 详情），绝不触达下载端点。
func TestRunCheckOnly(t *testing.T) {
	var downloadHits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases"):
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"tag_name": "v9.9.9", "html_url": base + "/tag/v9.9.9", "draft": false,
				"assets": []map[string]any{
					{"name": "godot-ai-cli-9.9.9-windows-amd64.zip", "browser_download_url": base + "/dl/zip"},
				},
			}})
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			downloadHits++
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := Run(context.Background(), Options{
		CurrentVersion: "0.1.0", BaseURL: server.URL, HTTPClient: server.Client(),
		GOOS: "windows", GOARCH: "amd64", CheckOnly: true, PromptOut: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "ok" || result["update_available"] != true || result["latest_version"] != "9.9.9" {
		t.Errorf("result = %v", result)
	}
	if downloadHits != 0 {
		t.Errorf("--check touched the download endpoint %d times", downloadHits)
	}

	// 已是最新时 --check 同样干净。
	result, err = Run(context.Background(), Options{
		CurrentVersion: "9.9.9", BaseURL: server.URL, HTTPClient: server.Client(),
		GOOS: "windows", GOARCH: "amd64", CheckOnly: true, PromptOut: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["update_available"] != false {
		t.Errorf("up-to-date --check = %v", result)
	}
}

// TestRunFailedDownloadLeavesNoOld：下载失败路径绝不能留下 .old 半成品
// （需求 §4.5——重命名只许发生在下载+校验+解压全部成功之后）。
func TestRunFailedDownloadLeavesNoOld(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "godot-ai-cli.exe")
	if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases"):
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"tag_name": "v0.2.0", "html_url": base + "/tag", "draft": false,
				"assets": []map[string]any{
					{"name": "godot-ai-cli-0.2.0-windows-amd64.zip", "browser_download_url": base + "/truncated.zip"},
				},
			}})
		default:
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(make([]byte, 20))
		}
	}))
	defer server.Close()

	_, err := Run(context.Background(), Options{
		CurrentVersion: "0.1.0", BaseURL: server.URL, HTTPClient: server.Client(),
		GOOS: "windows", GOARCH: "amd64", InstallDir: dir, AssumeYes: true, PromptOut: io.Discard,
	})
	if err == nil {
		t.Fatal("truncated download must fail the update")
	}
	assertFileContent(t, target, "old-binary")
	if _, statErr := os.Stat(target + ".old"); !os.IsNotExist(statErr) {
		t.Errorf("failed update left a .old behind (stat err = %v)", statErr)
	}
}

// statusServer 回一个固定状态码与响应头，用于钉死查询端点的分类规则。
func statusServer(t *testing.T, code int, headers map[string]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(server.Close)
	return server
}

// TestUpdateRateLimitedClassification：需求 R-6 的错误分类现场——403 且
// X-RateLimit-Remaining: 0 才是限流，回 UPDATE_CHECK_RATE_LIMITED，data 带
// url / http_status / rate_limit_reset（X-RateLimit-Reset 的 unix 秒转
// RFC3339）/ next_steps（--tag / --from-atom / --zip 三条降级通道）；其余
// 非 200（含不带限流头的 403）维持 UPDATE_CHECK_FAILED 但补 http_status。
func TestUpdateRateLimitedClassification(t *testing.T) {
	resetSec := time.Now().Add(42 * time.Minute).Unix()
	wantReset := time.Unix(resetSec, 0).UTC().Format(time.RFC3339)

	t.Run("403 with the rate-limit headers", func(t *testing.T) {
		server := statusServer(t, http.StatusForbidden, map[string]string{
			"X-RateLimit-Remaining": "0",
			"X-RateLimit-Reset":     strconv.FormatInt(resetSec, 10),
		})
		_, err := FetchLatestRelease(context.Background(), server.Client(), server.URL, "o", "r")
		assertCode(t, err, CodeCheckRateLimited)

		var uerr *Error
		if !errors.As(err, &uerr) {
			t.Fatalf("error %v is not an *update.Error", err)
		}
		if url, _ := uerr.Data["url"].(string); !strings.Contains(url, "/repos/o/r/releases") {
			t.Errorf("data url = %v", uerr.Data["url"])
		}
		if uerr.Data["http_status"] != http.StatusForbidden {
			t.Errorf("http_status = %v", uerr.Data["http_status"])
		}
		if uerr.Data["rate_limit_reset"] != wantReset {
			t.Errorf("rate_limit_reset = %v, want %s", uerr.Data["rate_limit_reset"], wantReset)
		}
		steps, _ := uerr.Data["next_steps"].([]string)
		if len(steps) != 3 {
			t.Fatalf("next_steps = %v", uerr.Data["next_steps"])
		}
		joined := strings.Join(steps, "\n")
		for _, flag := range []string{"--tag", "--from-atom", "--zip"} {
			if !strings.Contains(joined, flag) {
				t.Errorf("next_steps does not point at %s: %v", flag, steps)
			}
		}
		if !strings.Contains(uerr.Message, wantReset) {
			t.Errorf("message does not carry the reset time: %q", uerr.Message)
		}
	})

	t.Run("403 without the rate-limit header", func(t *testing.T) {
		// 权限不足/其它 403 是确定性失败：等一会儿也不会好，绝不能报成限流。
		server := statusServer(t, http.StatusForbidden, nil)
		_, err := FetchLatestRelease(context.Background(), server.Client(), server.URL, "o", "r")
		assertCode(t, err, CodeCheckFailed)
		var uerr *Error
		if errors.As(err, &uerr) && uerr.Data["http_status"] != http.StatusForbidden {
			t.Errorf("http_status = %v", uerr.Data["http_status"])
		}
	})

	t.Run("403 with a remaining budget", func(t *testing.T) {
		server := statusServer(t, http.StatusForbidden, map[string]string{"X-RateLimit-Remaining": "42"})
		_, err := FetchLatestRelease(context.Background(), server.Client(), server.URL, "o", "r")
		assertCode(t, err, CodeCheckFailed)
	})

	t.Run("other statuses keep UPDATE_CHECK_FAILED plus http_status", func(t *testing.T) {
		for _, code := range []int{http.StatusNotFound, http.StatusUnauthorized, http.StatusInternalServerError} {
			server := statusServer(t, code, nil)
			_, err := FetchLatestRelease(context.Background(), server.Client(), server.URL, "o", "r")
			assertCode(t, err, CodeCheckFailed)
			var uerr *Error
			if errors.As(err, &uerr) && uerr.Data["http_status"] != code {
				t.Errorf("status %d: http_status = %v", code, uerr.Data["http_status"])
			}
		}
	})
}

// tagHits 记录三类端点的命中次数：列表端点（--tag/--from-atom 都不该碰，
// 它正是被限流挂掉的那个）、单点查询、资产下载（--check 组合不该碰），
// 另记 atom 的请求路径（用来证明问的是站点根而不是 API 根）。
type tagHits struct {
	list, tag, download, atom int
	atomPath                  string
}

// fallbackServerOpts 是降级通道布景的全部输入。
type fallbackServerOpts struct {
	Tag      string   // /releases/tags/<Tag> 命中的 tag
	Asset    string   // 本平台 zip 资产名
	Zip      []byte   // zip 内容
	AtomTags []string // 非空则提供 releases.atom（entry 从新到旧）
	AtomRaw  string   // 非空则原样返回该 atom 体（畸形 XML 用）
	Hits     *tagHits
}

// fallbackServer 搭出限流现场：列表端点恒 403 + 限流头，单点查询按 tag 回
// 单个 release，资产按名字回内容，需要时同时提供 releases.atom。
func fallbackServer(t *testing.T, opts fallbackServerOpts) *httptest.Server {
	t.Helper()
	sumsName := ChecksumsName(strings.TrimPrefix(opts.Tag, "v"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases.atom") && (opts.AtomRaw != "" || len(opts.AtomTags) > 0):
			opts.Hits.atom++
			opts.Hits.atomPath = r.URL.Path
			if opts.AtomRaw != "" {
				_, _ = w.Write([]byte(opts.AtomRaw))
				return
			}
			_, _ = w.Write([]byte(atomFeedXML(base, opts.AtomTags...)))
		case strings.HasSuffix(r.URL.Path, "/releases"):
			opts.Hits.list++
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
			w.WriteHeader(http.StatusForbidden)
		case strings.Contains(r.URL.Path, "/releases/tags/"):
			opts.Hits.tag++
			if !strings.HasSuffix(r.URL.Path, "/releases/tags/"+opts.Tag) {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": opts.Tag,
				"html_url": base + "/releases/tag/" + opts.Tag,
				"draft":    false,
				"assets": []map[string]any{
					{"name": opts.Asset, "browser_download_url": base + "/download/" + opts.Asset},
					{"name": sumsName, "browser_download_url": base + "/download/" + sumsName},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/download/"):
			opts.Hits.download++
			switch name := strings.TrimPrefix(r.URL.Path, "/download/"); name {
			case opts.Asset:
				_, _ = w.Write(opts.Zip)
			case sumsName:
				_, _ = w.Write(fakegithub.Checksums(opts.Asset, opts.Zip))
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// atomFeedXML 渲染一份 releases.atom 形状的 feed：entry 从新到旧，链接
// 指向 …/releases/tag/<tag>（GitHub 的真实排布）。
func atomFeedXML(base string, tags ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<feed xmlns="http://www.w3.org/2005/Atom" xml:lang="en-US">` + "\n")
	for _, tag := range tags {
		b.WriteString("<entry>\n")
		b.WriteString("<title>" + tag + "</title>\n")
		b.WriteString(`<link rel="alternate" type="text/html" href="` + base + "/mimajiushi/godot-ai-cli/releases/tag/" + tag + `"/>` + "\n")
		b.WriteString("</entry>\n")
	}
	b.WriteString("</feed>\n")
	return b.String()
}

// TestUpdateTagChannel：--tag 走 /releases/tags/<tag> 单点查询，完全不碰
// 被限流的列表端点；命中后复用现有资产选择/sha256 校验/替换流程；tag 不
// 存在回明确错误；与 --check 组合只查不下。
func TestUpdateTagChannel(t *testing.T) {
	const tag = "v0.2.0"
	zipData := fakegithub.Zip(t, "godot-ai-cli.exe", []byte("new-binary"))
	assetName := AssetName("0.2.0", "windows", "amd64")

	t.Run("single release query", func(t *testing.T) {
		hits := &tagHits{}
		server := fallbackServer(t, fallbackServerOpts{Tag: tag, Asset: assetName, Zip: zipData, Hits: hits})
		rel, err := FetchReleaseByTag(context.Background(), server.Client(), server.URL, "o", "r", tag)
		if err != nil {
			t.Fatal(err)
		}
		if rel.TagName != tag || len(rel.Assets) != 2 {
			t.Errorf("release = %+v", rel)
		}
		if hits.tag != 1 || hits.list != 0 {
			t.Errorf("hits = %+v", hits)
		}
	})

	t.Run("--check only checks", func(t *testing.T) {
		hits := &tagHits{}
		server := fallbackServer(t, fallbackServerOpts{Tag: tag, Asset: assetName, Zip: zipData, Hits: hits})
		result, err := Run(context.Background(), Options{
			CurrentVersion: "0.1.0", BaseURL: server.URL, HTTPClient: server.Client(),
			GOOS: "windows", GOARCH: "amd64", Tag: tag, CheckOnly: true, PromptOut: io.Discard,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result["status"] != "ok" || result["update_available"] != true || result["latest_version"] != "0.2.0" {
			t.Errorf("result = %v", result)
		}
		if hits.download != 0 || hits.list != 0 {
			t.Errorf("--check touched download/list: %+v", hits)
		}
	})

	t.Run("installs through the shared pipeline", func(t *testing.T) {
		hits := &tagHits{}
		server := fallbackServer(t, fallbackServerOpts{Tag: tag, Asset: assetName, Zip: zipData, Hits: hits})
		dir := t.TempDir()
		target := filepath.Join(dir, "godot-ai-cli.exe")
		if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
			t.Fatal(err)
		}
		result, err := Run(context.Background(), Options{
			CurrentVersion: "0.1.0", BaseURL: server.URL, HTTPClient: server.Client(),
			GOOS: "windows", GOARCH: "amd64", Tag: tag, InstallDir: dir,
			AssumeYes: true, PromptOut: io.Discard,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result["status"] != "updated" || result["latest_version"] != "0.2.0" {
			t.Errorf("result = %v", result)
		}
		if result["previous_binary"] != target+".old" {
			t.Errorf("previous_binary = %v", result["previous_binary"])
		}
		assertFileContent(t, target, "new-binary")
		if hits.list != 0 {
			t.Errorf("the list endpoint was hit: %+v", hits)
		}
	})

	t.Run("unknown tag", func(t *testing.T) {
		hits := &tagHits{}
		server := fallbackServer(t, fallbackServerOpts{Tag: tag, Asset: assetName, Zip: zipData, Hits: hits})
		_, err := Run(context.Background(), Options{
			CurrentVersion: "0.1.0", BaseURL: server.URL, HTTPClient: server.Client(),
			Tag: "v9.9.9", AssumeYes: true, PromptOut: io.Discard,
		})
		assertCode(t, err, CodeTagNotFound)
		if err == nil || !strings.Contains(err.Error(), "v9.9.9") {
			t.Errorf("message does not name the tag: %v", err)
		}
	})
}

// TestUpdateFromAtomChannel：--from-atom 解析 releases.atom 最新一条 entry
// 的 tag（含 prerelease），再走 --tag 那条路径；atom 的网络/解析失败回结构
// 化 UPDATE_ATOM_FAILED。
func TestUpdateFromAtomChannel(t *testing.T) {
	const tag = "v0.2.0-beta.1" // prerelease：atom 里同样看得到
	zipData := fakegithub.Zip(t, "godot-ai-cli.exe", []byte("new-binary"))
	assetName := AssetName("0.2.0-beta.1", "windows", "amd64")

	t.Run("newest entry wins and installs", func(t *testing.T) {
		hits := &tagHits{}
		server := fallbackServer(t, fallbackServerOpts{
			Tag: tag, Asset: assetName, Zip: zipData, AtomTags: []string{tag, "v0.1.0"}, Hits: hits,
		})
		dir := t.TempDir()
		target := filepath.Join(dir, "godot-ai-cli.exe")
		if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
			t.Fatal(err)
		}
		result, err := Run(context.Background(), Options{
			CurrentVersion: "0.1.0", BaseURL: server.URL, HTTPClient: server.Client(),
			GOOS: "windows", GOARCH: "amd64", FromAtom: true, InstallDir: dir,
			AssumeYes: true, PromptOut: io.Discard,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result["status"] != "updated" || result["latest_version"] != "0.2.0-beta.1" {
			t.Errorf("result = %v", result)
		}
		assertFileContent(t, target, "new-binary")
		if hits.atom != 1 || hits.tag != 1 || hits.list != 0 {
			t.Errorf("hits = %+v", hits)
		}
		if strings.Contains(hits.atomPath, "/repos/") {
			t.Errorf("releases.atom was queried on the API root: %s", hits.atomPath)
		}
	})

	t.Run("parses the first entry", func(t *testing.T) {
		hits := &tagHits{}
		server := fallbackServer(t, fallbackServerOpts{
			Tag: tag, Asset: assetName, Zip: zipData, AtomTags: []string{tag, "v0.1.0"}, Hits: hits,
		})
		got, err := FetchLatestTagFromAtom(context.Background(), server.Client(), server.URL, "o", "r")
		if err != nil {
			t.Fatal(err)
		}
		if got != tag {
			t.Errorf("tag = %q, want %q", got, tag)
		}
	})

	t.Run("http failure", func(t *testing.T) {
		server := httptest.NewServer(http.NotFoundHandler())
		defer server.Close()
		_, err := FetchLatestTagFromAtom(context.Background(), server.Client(), server.URL, "o", "r")
		assertCode(t, err, CodeAtomFailed)
	})

	t.Run("unreachable host", func(t *testing.T) {
		server := httptest.NewServer(http.NotFoundHandler())
		url := server.URL
		server.Close()
		_, err := FetchLatestTagFromAtom(context.Background(), server.Client(), url, "o", "r")
		assertCode(t, err, CodeAtomFailed)
	})

	t.Run("malformed xml", func(t *testing.T) {
		server := fallbackServer(t, fallbackServerOpts{Tag: tag, AtomRaw: "<feed><entry>", Hits: &tagHits{}})
		_, err := FetchLatestTagFromAtom(context.Background(), server.Client(), server.URL, "o", "r")
		assertCode(t, err, CodeAtomFailed)
	})

	t.Run("empty feed", func(t *testing.T) {
		empty := `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"></feed>`
		server := fallbackServer(t, fallbackServerOpts{Tag: tag, AtomRaw: empty, Hits: &tagHits{}})
		_, err := FetchLatestTagFromAtom(context.Background(), server.Client(), server.URL, "o", "r")
		assertCode(t, err, CodeAtomFailed)
		if err == nil || !strings.Contains(err.Error(), "no entry") {
			t.Errorf("message = %v", err)
		}
	})
}

// TestUpdateSiteRootFromAPIBase：releases.atom 在站点上而不是 API 上——
// 官方 API 根换成 github.com，测试注入的 httptest 服务器同源直连，路径
// 部分不参与（atom 在仓库根下）。
func TestUpdateSiteRootFromAPIBase(t *testing.T) {
	cases := map[string]string{
		"https://api.github.com":    "https://github.com",
		"https://api.github.com/":   "https://github.com",
		"http://127.0.0.1:8080":     "http://127.0.0.1:8080",
		"http://127.0.0.1:8080/api": "http://127.0.0.1:8080",
	}
	for in, want := range cases {
		if got := siteRoot(in); got != want {
			t.Errorf("siteRoot(%q) = %q, want %q", in, got, want)
		}
	}
	// 解析不出来时退回原值，让请求本身去报网络错误。
	if got := siteRoot("://bad"); got != "://bad" {
		t.Errorf("siteRoot(\"://bad\") = %q", got)
	}
}

// TestOfflineZipChannel：--zip 完全离线——一个网络请求都不发。给了
// --checksums 才做双源校验（文件名 + sha256）并在通过后替换；没给直接拒绝
// 并提示；校验不过时安装目录一个字节都不动、也不留 .old 半成品。
func TestOfflineZipChannel(t *testing.T) {
	const asset = "godot-ai-cli-0.1.0-windows-amd64.zip"
	zipData := fakegithub.Zip(t, "godot-ai-cli.exe", []byte("new-binary"))
	sums := fakegithub.Checksums(asset, zipData)

	// package 把 zip 与 checksums 落到临时目录（zip 用资产名，checksums 行
	// 里写的就是它）。
	pkg := func(t *testing.T, zipName string, zipBody, sumsBody []byte) (zipPath, sumsPath string) {
		t.Helper()
		dir := t.TempDir()
		zipPath = filepath.Join(dir, zipName)
		sumsPath = filepath.Join(dir, "checksums.txt")
		if err := os.WriteFile(zipPath, zipBody, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sumsPath, sumsBody, 0o644); err != nil {
			t.Fatal(err)
		}
		return zipPath, sumsPath
	}
	// install 备一个装着旧二进制的假安装目录。
	install := func(t *testing.T) (dir, target string) {
		t.Helper()
		dir = t.TempDir()
		target = filepath.Join(dir, "godot-ai-cli.exe")
		if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
			t.Fatal(err)
		}
		return dir, target
	}
	// noNetwork 回一个记录命中次数的「必须不被访问」的服务器。
	noNetwork := func(t *testing.T) (*httptest.Server, *int) {
		t.Helper()
		hits := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits++
			http.NotFound(w, r)
		}))
		t.Cleanup(server.Close)
		return server, &hits
	}

	t.Run("verifies and replaces", func(t *testing.T) {
		zipPath, sumsPath := pkg(t, asset, zipData, sums)
		dir, target := install(t)
		server, hits := noNetwork(t)

		result, err := Run(context.Background(), Options{
			CurrentVersion: "0.1.0", BaseURL: server.URL, HTTPClient: server.Client(),
			GOOS: "windows", GOARCH: "amd64", ZipPath: zipPath, ChecksumsPath: sumsPath,
			InstallDir: dir, AssumeYes: true, PromptOut: io.Discard,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result["status"] != "updated" || result["checksum_verified"] != true {
			t.Errorf("result = %v", result)
		}
		if result["offline_zip"] != zipPath || result["previous_binary"] != target+".old" {
			t.Errorf("result = %v", result)
		}
		assertFileContent(t, target, "new-binary")
		if *hits != 0 {
			t.Errorf("the offline channel made %d network requests", *hits)
		}
	})

	t.Run("--check verifies without replacing", func(t *testing.T) {
		zipPath, sumsPath := pkg(t, asset, zipData, sums)
		dir, target := install(t)

		result, err := Run(context.Background(), Options{
			CurrentVersion: "0.1.0", GOOS: "windows", GOARCH: "amd64",
			ZipPath: zipPath, ChecksumsPath: sumsPath, InstallDir: dir,
			CheckOnly: true, AssumeYes: true, PromptOut: io.Discard,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result["status"] != "ok" || result["checksum_verified"] != true {
			t.Errorf("result = %v", result)
		}
		assertFileContent(t, target, "old-binary")
		if _, statErr := os.Stat(target + ".old"); !os.IsNotExist(statErr) {
			t.Errorf("--check left a .old behind (stat err = %v)", statErr)
		}
	})

	t.Run("without --checksums it refuses", func(t *testing.T) {
		zipPath, _ := pkg(t, asset, zipData, sums)
		dir, target := install(t)

		_, err := Run(context.Background(), Options{
			CurrentVersion: "0.1.0", GOOS: "windows", GOARCH: "amd64",
			ZipPath: zipPath, InstallDir: dir, AssumeYes: true, PromptOut: io.Discard,
		})
		assertCode(t, err, CodeChecksumInvalid)
		if err == nil || !strings.Contains(err.Error(), "--checksums") {
			t.Errorf("message does not point at --checksums: %v", err)
		}
		assertFileContent(t, target, "old-binary")
	})

	t.Run("checksum mismatch leaves the install untouched", func(t *testing.T) {
		zipPath, sumsPath := pkg(t, asset, zipData, fakegithub.Checksums(asset, []byte("other-bytes")))
		dir, target := install(t)

		_, err := Run(context.Background(), Options{
			CurrentVersion: "0.1.0", GOOS: "windows", GOARCH: "amd64",
			ZipPath: zipPath, ChecksumsPath: sumsPath, InstallDir: dir, AssumeYes: true, PromptOut: io.Discard,
		})
		assertCode(t, err, CodeChecksumMismatch)
		assertFileContent(t, target, "old-binary")
		if _, statErr := os.Stat(target + ".old"); !os.IsNotExist(statErr) {
			t.Errorf("mismatch left a .old behind (stat err = %v)", statErr)
		}
	})

	t.Run("no entry for the zip name", func(t *testing.T) {
		zipPath, sumsPath := pkg(t, asset, zipData, fakegithub.Checksums("other.zip", zipData))
		dir, target := install(t)

		_, err := Run(context.Background(), Options{
			CurrentVersion: "0.1.0", GOOS: "windows", GOARCH: "amd64",
			ZipPath: zipPath, ChecksumsPath: sumsPath, InstallDir: dir, AssumeYes: true, PromptOut: io.Discard,
		})
		assertCode(t, err, CodeChecksumInvalid)
		assertFileContent(t, target, "old-binary")
	})

	t.Run("zip without this platform's binary", func(t *testing.T) {
		other := fakegithub.Zip(t, "godot-ai-cli", []byte("new-binary"))
		zipPath, sumsPath := pkg(t, asset, other, fakegithub.Checksums(asset, other))
		dir, target := install(t)

		_, err := Run(context.Background(), Options{
			CurrentVersion: "0.1.0", GOOS: "windows", GOARCH: "amd64",
			ZipPath: zipPath, ChecksumsPath: sumsPath, InstallDir: dir, AssumeYes: true, PromptOut: io.Discard,
		})
		assertCode(t, err, CodeArchiveInvalid)
		assertFileContent(t, target, "old-binary")
	})

	t.Run("conflicting release sources are refused", func(t *testing.T) {
		zipPath, sumsPath := pkg(t, asset, zipData, sums)
		_, err := Run(context.Background(), Options{
			CurrentVersion: "0.1.0", GOOS: "windows", GOARCH: "amd64",
			ZipPath: zipPath, ChecksumsPath: sumsPath, Tag: "v0.1.0", AssumeYes: true, PromptOut: io.Discard,
		})
		assertCode(t, err, CodeCheckFailed)
		if err == nil || !strings.Contains(err.Error(), "--zip") {
			t.Errorf("message = %v", err)
		}

		_, err = Run(context.Background(), Options{
			CurrentVersion: "0.1.0", Tag: "v0.1.0", FromAtom: true, AssumeYes: true, PromptOut: io.Discard,
		})
		assertCode(t, err, CodeCheckFailed)

		_, err = Run(context.Background(), Options{
			CurrentVersion: "0.1.0", ChecksumsPath: sumsPath, AssumeYes: true, PromptOut: io.Discard,
		})
		assertCode(t, err, CodeCheckFailed)
	})
}
