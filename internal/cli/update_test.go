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
	"strconv"
	"strings"
	"testing"
	"time"

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

// TestUpdateCommandAlreadyUpToDate: the release equals the build-in dev
// version (0.0.0-dev is the floor, so equality is the only up-to-date
// case) → the up-to-date payload.
func TestUpdateCommandAlreadyUpToDate(t *testing.T) {
	server := fakegithub.New(t, "v0.0.0-dev", nil)
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
	if out["current_version"] == "" || out["latest_version"] != "0.0.0-dev" {
		t.Errorf("versions = %v/%v", out["current_version"], out["latest_version"])
	}
}

// TestUpdateCommandCancelledNonTTY: a newer release but no terminal → the
// update is declined, yet the payload still carries the release details and
// the --yes hint so a script/agent caller knows how to proceed.
func TestUpdateCommandCancelledNonTTY(t *testing.T) {
	server := fakegithub.New(t, "v9.9.9", nil)
	setUpdateAPIBase(t, server.URL)

	out, err := runUpdateCmd(t)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if out["status"] != "cancelled" || out["update_available"] != true {
		t.Errorf("out = %v", out)
	}
	if out["latest_version"] != "9.9.9" {
		t.Errorf("latest_version = %v", out["latest_version"])
	}
	if msg, _ := out["message"].(string); !strings.Contains(msg, "--yes") {
		t.Errorf("message does not point at --yes: %v", out["message"])
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
	data, _ := errObj["data"].(map[string]any)
	if data["http_status"] != float64(http.StatusNotFound) {
		t.Errorf("data does not carry http_status: %v", data)
	}
}

// updateFallbackHits 记录列表端点（限流挂的那个，降级通道都不该碰）与
// 资产下载（--check 组合不该碰）的命中次数。
type updateFallbackHits struct{ list, download int }

// updateFallbackServer 是 --tag/--from-atom 两条降级通道的布景：列表端点
// 恒 403 + 限流头（复现现场），单点查询按 tag 回单个 release，资产按名字
// 回内容，atomTag 非空时同时提供 releases.atom。
func updateFallbackServer(t *testing.T, tag string, zipData []byte, atomTag string, hits *updateFallbackHits) *httptest.Server {
	t.Helper()
	ver := strings.TrimPrefix(tag, "v")
	assetName := update.AssetName(ver, runtime.GOOS, runtime.GOARCH)
	sumsName := update.ChecksumsName(ver)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases.atom") && atomTag != "":
			_, _ = w.Write([]byte(updateAtomXML(base, atomTag)))
		case strings.HasSuffix(r.URL.Path, "/releases"):
			hits.list++
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
			w.WriteHeader(http.StatusForbidden)
		case strings.Contains(r.URL.Path, "/releases/tags/"):
			if !strings.HasSuffix(r.URL.Path, "/releases/tags/"+tag) {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": tag,
				"html_url": base + "/releases/tag/" + tag,
				"draft":    false,
				"assets": []map[string]any{
					{"name": assetName, "browser_download_url": base + "/download/" + assetName},
					{"name": sumsName, "browser_download_url": base + "/download/" + sumsName},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/download/"):
			hits.download++
			switch name := strings.TrimPrefix(r.URL.Path, "/download/"); name {
			case assetName:
				_, _ = w.Write(zipData)
			case sumsName:
				_, _ = w.Write(fakegithub.Checksums(assetName, zipData))
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

// updateAtomXML 渲染一份 releases.atom 形状的 feed（entry 从新到旧，链接
// 指向 …/releases/tag/<tag>）。
func updateAtomXML(base string, tags ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<feed xmlns="http://www.w3.org/2005/Atom">` + "\n")
	for _, tag := range tags {
		b.WriteString("<entry><title>" + tag + "</title>" +
			`<link rel="alternate" type="text/html" href="` + base + "/releases/tag/" + tag + `"/></entry>` + "\n")
	}
	b.WriteString("</feed>\n")
	return b.String()
}

// TestUpdateCommandRateLimited: 403 + X-RateLimit-Remaining: 0 是限流，不是
// 泛泛的查询失败——envelope 里必须是 UPDATE_CHECK_RATE_LIMITED，data 带
// url / rate_limit_reset / next_steps，且三条降级通道各一句。
func TestUpdateCommandRateLimited(t *testing.T) {
	resetSec := time.Now().Add(30 * time.Minute).Unix()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetSec, 10))
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	setUpdateAPIBase(t, server.URL)

	out, err := runUpdateCmd(t, "--check")
	if err == nil {
		t.Fatal("a rate-limited check succeeded, want an error")
	}
	errObj, ok := out["error"].(map[string]any)
	if !ok || out["status"] != "error" {
		t.Fatalf("out = %v", out)
	}
	if errObj["code"] != "UPDATE_CHECK_RATE_LIMITED" {
		t.Fatalf("code = %v", errObj["code"])
	}
	data, _ := errObj["data"].(map[string]any)
	if url, _ := data["url"].(string); !strings.Contains(url, "/repos/") || !strings.Contains(url, "/releases") {
		t.Errorf("data url = %v", data["url"])
	}
	if data["http_status"] != float64(http.StatusForbidden) {
		t.Errorf("http_status = %v", data["http_status"])
	}
	if want := time.Unix(resetSec, 0).UTC().Format(time.RFC3339); data["rate_limit_reset"] != want {
		t.Errorf("rate_limit_reset = %v, want %s", data["rate_limit_reset"], want)
	}
	steps, _ := data["next_steps"].([]any)
	if len(steps) != 3 {
		t.Fatalf("next_steps = %v", data["next_steps"])
	}
	joined := fmt.Sprint(steps...)
	for _, flag := range []string{"--tag", "--from-atom", "--zip"} {
		if !strings.Contains(joined, flag) {
			t.Errorf("next_steps does not point at %s: %v", flag, steps)
		}
	}
}

// TestUpdateCommandTagChannel: --tag 走单点查询（列表端点恒 403，且计数必须
// 为 0）；与 --check 组合只查不下；--yes 时经同一套资产/校验/替换流程安装。
func TestUpdateCommandTagChannel(t *testing.T) {
	const tag = "v9.9.9"
	binName := update.BinaryName(runtime.GOOS)
	zipData := fakegithub.Zip(t, binName, []byte("new-binary"))

	t.Run("--check only checks", func(t *testing.T) {
		hits := &updateFallbackHits{}
		server := updateFallbackServer(t, tag, zipData, "", hits)
		setUpdateAPIBase(t, server.URL)

		out, err := runUpdateCmd(t, "--check", "--tag", tag)
		if err != nil {
			t.Fatalf("update --check --tag: %v", err)
		}
		if out["status"] != "ok" || out["update_available"] != true || out["latest_version"] != "9.9.9" {
			t.Errorf("out = %v", out)
		}
		if hits.download != 0 || hits.list != 0 {
			t.Errorf("--check touched download/list: %+v", hits)
		}
	})

	t.Run("installs through the tag endpoint", func(t *testing.T) {
		hits := &updateFallbackHits{}
		server := updateFallbackServer(t, tag, zipData, "", hits)
		setUpdateAPIBase(t, server.URL)

		dir := t.TempDir()
		target := filepath.Join(dir, binName)
		if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
			t.Fatal(err)
		}
		out, err := runUpdateCmd(t, "--yes", "--from", dir, "--tag", tag)
		if err != nil {
			t.Fatalf("update --yes --tag: %v", err)
		}
		if out["status"] != "updated" || out["latest_version"] != "9.9.9" {
			t.Errorf("out = %v", out)
		}
		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "new-binary" {
			t.Errorf("installed binary = %q", data)
		}
		if hits.list != 0 {
			t.Errorf("the rate-limited list endpoint was hit: %+v", hits)
		}
	})

	t.Run("unknown tag", func(t *testing.T) {
		server := updateFallbackServer(t, tag, zipData, "", &updateFallbackHits{})
		setUpdateAPIBase(t, server.URL)

		out, err := runUpdateCmd(t, "--check", "--tag", "v0.0.1")
		if err == nil {
			t.Fatal("an unknown tag succeeded, want an error")
		}
		errObj, _ := out["error"].(map[string]any)
		if errObj["code"] != "UPDATE_TAG_NOT_FOUND" {
			t.Errorf("out = %v", out)
		}
	})
}

// TestUpdateCommandFromAtomChannel: 列表端点被限流时 --from-atom 改读
// releases.atom 取最新 tag，再走单点查询把包装上。
func TestUpdateCommandFromAtomChannel(t *testing.T) {
	const tag = "v9.9.9"
	binName := update.BinaryName(runtime.GOOS)
	zipData := fakegithub.Zip(t, binName, []byte("new-binary"))
	hits := &updateFallbackHits{}
	server := updateFallbackServer(t, tag, zipData, tag, hits)
	setUpdateAPIBase(t, server.URL)

	dir := t.TempDir()
	target := filepath.Join(dir, binName)
	if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := runUpdateCmd(t, "--yes", "--from", dir, "--from-atom")
	if err != nil {
		t.Fatalf("update --yes --from-atom: %v", err)
	}
	if out["status"] != "updated" || out["latest_version"] != "9.9.9" {
		t.Errorf("out = %v", out)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new-binary" {
		t.Errorf("installed binary = %q", data)
	}
	if hits.list != 0 {
		t.Errorf("the rate-limited list endpoint was hit: %+v", hits)
	}
}

// TestUpdateCommandOfflineZip: --zip + --checksums 全程离线安装；没给
// --checksums 时拒绝并提示，安装目录保持原样。
func TestUpdateCommandOfflineZip(t *testing.T) {
	binName := update.BinaryName(runtime.GOOS)
	zipData := fakegithub.Zip(t, binName, []byte("new-binary"))
	assetName := update.AssetName("9.9.9", runtime.GOOS, runtime.GOARCH)

	pkgDir := t.TempDir()
	zipPath := filepath.Join(pkgDir, assetName)
	sumsPath := filepath.Join(pkgDir, "checksums.txt")
	if err := os.WriteFile(zipPath, zipData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sumsPath, fakegithub.Checksums(assetName, zipData), 0o644); err != nil {
		t.Fatal(err)
	}
	// freshInstall 备一个装着旧二进制的假安装目录（每个子用例独立）。
	freshInstall := func(t *testing.T) (dir, target string) {
		t.Helper()
		dir = t.TempDir()
		target = filepath.Join(dir, binName)
		if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
			t.Fatal(err)
		}
		return dir, target
	}

	t.Run("installs offline", func(t *testing.T) {
		dir, target := freshInstall(t)
		out, err := runUpdateCmd(t, "--yes", "--from", dir, "--zip", zipPath, "--checksums", sumsPath)
		if err != nil {
			t.Fatalf("update --zip: %v", err)
		}
		if out["status"] != "updated" || out["checksum_verified"] != true || out["offline_zip"] != zipPath {
			t.Errorf("out = %v", out)
		}
		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "new-binary" {
			t.Errorf("installed binary = %q", data)
		}
	})

	t.Run("--check verifies without replacing", func(t *testing.T) {
		dir, target := freshInstall(t)
		out, err := runUpdateCmd(t, "--check", "--from", dir, "--zip", zipPath, "--checksums", sumsPath)
		if err != nil {
			t.Fatalf("update --check --zip: %v", err)
		}
		if out["status"] != "ok" || out["checksum_verified"] != true {
			t.Errorf("out = %v", out)
		}
		data, _ := os.ReadFile(target)
		if string(data) != "old-binary" {
			t.Errorf("--check replaced the install: %q", data)
		}
	})

	t.Run("without --checksums it refuses", func(t *testing.T) {
		dir, target := freshInstall(t)
		out, err := runUpdateCmd(t, "--yes", "--from", dir, "--zip", zipPath)
		if err == nil {
			t.Fatal("--zip without --checksums succeeded, want an error")
		}
		errObj, _ := out["error"].(map[string]any)
		if errObj["code"] != "UPDATE_CHECKSUM_INVALID" {
			t.Errorf("out = %v", out)
		}
		msg, _ := errObj["message"].(string)
		if !strings.Contains(msg, "--checksums") {
			t.Errorf("message does not point at --checksums: %v", msg)
		}
		data, _ := os.ReadFile(target)
		if string(data) != "old-binary" {
			t.Errorf("the refusal modified the install: %q", data)
		}
	})
}
