// Package update implements the godot-ai-cli self-update flow: querying
// GitHub Releases for the latest version, comparing semantic versions,
// downloading the platform asset with SHA256 verification, and swapping
// the running executable (rename-aside on Windows, atomic rename on Unix).
package update

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	urlpkg "net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mimajiushi/godot-ai-cli/internal/godot"
	"github.com/mimajiushi/godot-ai-cli/internal/ops"
	"github.com/mimajiushi/godot-ai-cli/internal/version"
)

// DefaultAPIBase is the public GitHub API root; tests inject an httptest
// server URL through Options.BaseURL.
const DefaultAPIBase = "https://api.github.com"

// Machine-readable failure codes surfaced in the CLI error envelope.
const (
	CodeCheckFailed      = "UPDATE_CHECK_FAILED"
	CodeCheckRateLimited = "UPDATE_CHECK_RATE_LIMITED"
	CodeAtomFailed       = "UPDATE_ATOM_FAILED"
	CodeTagNotFound      = "UPDATE_TAG_NOT_FOUND"
	CodeAssetNotFound    = "UPDATE_ASSET_NOT_FOUND"
	CodeDownloadFailed   = "UPDATE_DOWNLOAD_FAILED"
	CodeChecksumInvalid  = "UPDATE_CHECKSUM_INVALID"
	CodeChecksumMismatch = "UPDATE_CHECKSUM_MISMATCH"
	CodeArchiveInvalid   = "UPDATE_ARCHIVE_INVALID"
	CodeReplaceFailed    = "UPDATE_REPLACE_FAILED"
)

// Error is a user-facing update failure carrying a machine-readable code.
type Error struct {
	Code    string
	Message string
	Data    map[string]any
}

// Error implements the error interface.
func (e *Error) Error() string { return e.Message }

// Release is the subset of the GitHub Releases API payload we consume.
type Release struct {
	TagName string  `json:"tag_name"`
	HTMLURL string  `json:"html_url"`
	Draft   bool    `json:"draft"`
	Assets  []Asset `json:"assets"`
}

// Asset is one downloadable file attached to a Release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Options drives one Run call. Zero fields get production defaults.
type Options struct {
	// CurrentVersion is the running build's version (default version.Version).
	CurrentVersion string
	// BaseURL is the GitHub API root (default DefaultAPIBase).
	BaseURL string
	// HTTPClient performs all requests (default: 2-minute total timeout).
	HTTPClient *http.Client
	// GOOS/GOARCH select the release asset (default: runtime values).
	GOOS   string
	GOARCH string
	// InstallDir (--from) updates the godot-ai-cli install inside this
	// directory instead of the running executable; used by tests against a
	// fake install dir.
	InstallDir string
	// AssumeYes (--yes) applies the update without the interactive prompt.
	AssumeYes bool
	// In is where the confirmation answer is read from (default os.Stdin).
	In io.Reader
	// IsTerminal reports whether In is interactive; without a terminal the
	// answer defaults to No and the cancelled payload carries the release
	// details plus the --yes hint.
	IsTerminal bool
	// PromptOut receives the confirmation prompt (default os.Stderr), so
	// stdout stays pure JSON.
	PromptOut io.Writer
	// CheckOnly (--check) stops after the version-availability check: the
	// result carries update_available and the release details, nothing is
	// downloaded or replaced.
	CheckOnly bool
	// Proxy (--proxy)："" 走默认（net/http 的 ProxyFromEnvironment，认
	// HTTPS_PROXY/HTTP_PROXY 环境变量）；"auto" 先读环境变量，未设置时
	// Windows 再读注册表系统代理（HKCU Internet Settings）；其它值按
	// 显式代理 URL 解析（如 http://127.0.0.1:7897）。
	Proxy string
	// Tag (--tag)：显式版本，改走 /releases/tags/<tag> 单点查询，绕开
	// 被限流的列表端点；命中后复用同一套资产选择/sha256 校验/替换流程。
	Tag string
	// FromAtom (--from-atom)：API 整体不可达时降级到 releases.atom，解析
	// 出最新 tag 后走 --tag 那条路。
	FromAtom bool
	// ZipPath (--zip)：已下载的 release zip，完全离线通道（不发起任何
	// 网络请求）；必须配 ChecksumsPath，双源校验通过才替换。
	ZipPath string
	// ChecksumsPath (--checksums)：与 --zip 配对的 checksums.txt。
	ChecksumsPath string
}

// withDefaults fills zero-value fields with their production defaults.
func (o Options) withDefaults() Options {
	if o.CurrentVersion == "" {
		o.CurrentVersion = version.Version
	}
	if o.BaseURL == "" {
		o.BaseURL = DefaultAPIBase
	}
	if o.HTTPClient == nil {
		o.HTTPClient = proxyHTTPClient(o.Proxy)
	}
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.GOARCH == "" {
		o.GOARCH = runtime.GOARCH
	}
	if o.In == nil {
		o.In = os.Stdin
	}
	if o.PromptOut == nil {
		o.PromptOut = os.Stderr
	}
	return o
}

// proxyHTTPClient 按 Proxy 设置构造 HTTP client：显式/auto 命中时代理
// 进 transport（诊断数据里能如实报 proxy_used）；空值保持默认行为
// （ProxyFromEnvironment 依旧生效）。
func proxyHTTPClient(proxy string) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if urlStr, ok := ResolveProxy(proxy); ok {
		if u, err := urlpkg.Parse(urlStr); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{Timeout: 2 * time.Minute, Transport: transport}
}

// ResolveProxy 解析 --proxy 的取值为「实际代理 URL」：
//   - "auto"：HTTPS_PROXY/HTTP_PROXY（含小写形式）→ Windows 注册表系统代理；
//   - 显式 URL：原样返回；
//   - ""/未命中：("", false)——调用方保持默认行为。
//
// 单独成函数是为了让 CLI 能在错误诊断里如实报 proxy_used。
func ResolveProxy(proxy string) (string, bool) {
	switch proxy {
	case "":
		return "", false
	case "auto":
		for _, key := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
			if v := strings.TrimSpace(os.Getenv(key)); v != "" {
				return v, true
			}
		}
		if v := godot.WindowsSystemProxy(); v != "" {
			return v, true
		}
		return "", false
	default:
		return proxy, true
	}
}

// Run executes the full update flow and returns the JSON-ready result map.
// A nil error always carries a printable result; failures come back as
// *Error so the CLI can emit its standard envelope.
func Run(ctx context.Context, opts Options) (map[string]any, error) {
	opts = opts.withDefaults()

	if err := validateSources(opts); err != nil {
		return nil, err
	}
	// --zip：完全离线通道，没有任何 release 元数据可查（它正是为 API
	// 不可达准备的），因此整条流程单独走。
	if opts.ZipPath != "" {
		return runOfflineZip(ctx, opts)
	}

	rel, err := resolveRelease(ctx, opts)
	if err != nil {
		// 查询失败也带上代理诊断：TUN/fake-ip 现场里「API 通不通」与
		// 「走没走代理」是同一个问题（需求 update-behind-tun-fakeip）。
		var uerr *Error
		if errors.As(err, &uerr) && uerr.Data != nil {
			if proxy, ok := ResolveProxy(opts.Proxy); ok {
				uerr.Data["proxy_used"] = proxy
			}
		}
		return nil, err
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	cmp, err := CompareVersions(opts.CurrentVersion, latest)
	if err != nil {
		return nil, &Error{Code: CodeCheckFailed, Message: fmt.Sprintf("compare versions: %v", err)}
	}
	if cmp >= 0 {
		return map[string]any{
			"status":           "ok",
			"update_available": false,
			"current_version":  opts.CurrentVersion,
			"latest_version":   latest,
			"message":          "godot-ai-cli is already up to date",
		}, nil
	}

	// Shared by the declined and the updated results.
	availability := map[string]any{
		"update_available":  true,
		"current_version":   opts.CurrentVersion,
		"latest_version":    latest,
		"release_notes_url": rel.HTMLURL,
	}

	// --check：只回答「有没有新版本」，不下载不替换（需求 §4.4——行为等价
	// 于非 tty 的 cancelled，但显式、可发现、写进 help 示例）。
	if opts.CheckOnly {
		result := withStatus(availability, "ok")
		result["message"] = "new version available; re-run without --check to apply"
		return result, nil
	}

	if !opts.AssumeYes {
		if !opts.IsTerminal {
			// Nobody to ask: decline before any download, but say exactly
			// what is available and how to apply it — a bare cancelled
			// marker forced script/agent callers to guess at --yes.
			result := withStatus(availability, "cancelled")
			result["message"] = "no terminal to confirm the update; re-run with --yes to apply"
			return result, nil
		}
		fmt.Fprintf(opts.PromptOut, "Update now? [y/N]: ")
		if !readYes(opts.In) {
			return withStatus(availability, "cancelled"), nil
		}
	}

	asset, checksums, err := SelectAssets(rel, opts.GOOS, opts.GOARCH)
	if err != nil {
		return nil, err
	}
	zipData, err := DownloadAndVerify(ctx, opts.HTTPClient, asset, checksums)
	if err != nil {
		return nil, err
	}
	binary, err := ExtractBinary(zipData, opts.GOOS)
	if err != nil {
		return nil, err
	}

	target, err := resolveTarget(opts)
	if err != nil {
		return nil, err
	}
	if err := ReplaceExecutable(opts.GOOS, target, binary); err != nil {
		return nil, err
	}

	result := withStatus(availability, "updated")
	result["restart_required"] = true
	result["path"] = target
	if opts.GOOS == "windows" {
		// The old binary survives as <exe>.old until the next startup's
		// CleanupStaleBinary removes it.
		result["previous_binary"] = target + ".old"
	}
	if hint := docsHint(ctx, target); hint != nil {
		result["docs_hint"] = hint
	}
	return result, nil
}

// validateSources 拒绝互相冲突的来源选项：--tag 与 --from-atom 是同一件
// 事的两种问法（后者只是先问 atom 要 tag），--zip 又完全离线；一次给多个
// 只会让「谁生效」变成猜谜，所以在发任何请求之前就明确拒绝。
func validateSources(opts Options) error {
	if opts.ZipPath != "" && (opts.Tag != "" || opts.FromAtom) {
		return &Error{
			Code:    CodeCheckFailed,
			Message: "--zip is fully offline and cannot be combined with --tag or --from-atom; pass exactly one release source",
			Data:    map[string]any{"zip": opts.ZipPath, "tag": opts.Tag, "from_atom": opts.FromAtom},
		}
	}
	if opts.Tag != "" && opts.FromAtom {
		return &Error{
			Code:    CodeCheckFailed,
			Message: "--tag and --from-atom both name a release; pass only one (--from-atom resolves the tag itself)",
			Data:    map[string]any{"tag": opts.Tag},
		}
	}
	if opts.ChecksumsPath != "" && opts.ZipPath == "" {
		return &Error{
			Code:    CodeCheckFailed,
			Message: "--checksums only pairs with --zip; the online channels verify the release's own checksums asset",
			Data:    map[string]any{"checksums": opts.ChecksumsPath},
		}
	}
	return nil
}

// resolveRelease 按选项挑选 release 来源：--from-atom 先向 releases.atom
// 要最新 tag 再走单点查询；显式 --tag 直接单点查询；默认查列表端点。
func resolveRelease(ctx context.Context, opts Options) (Release, error) {
	if opts.FromAtom {
		tag, err := FetchLatestTagFromAtom(ctx, opts.HTTPClient, opts.BaseURL, version.RepoOwner, version.RepoName)
		if err != nil {
			return Release{}, err
		}
		return FetchReleaseByTag(ctx, opts.HTTPClient, opts.BaseURL, version.RepoOwner, version.RepoName, tag)
	}
	if opts.Tag != "" {
		return FetchReleaseByTag(ctx, opts.HTTPClient, opts.BaseURL, version.RepoOwner, version.RepoName, opts.Tag)
	}
	return FetchLatestRelease(ctx, opts.HTTPClient, opts.BaseURL, version.RepoOwner, version.RepoName)
}

// runOfflineZip 是 --zip 通道：全程离线。先双源校验（文件名 + sha256），
// 通过之后才复用与在线通道同一套替换机制——Windows 先改名让位、写失败
// 回滚，任何一步失败都不留半成品（需求 R-6 的手工替代流程）。
func runOfflineZip(ctx context.Context, opts Options) (map[string]any, error) {
	zipData, err := ReadAndVerifyLocalZip(opts.ZipPath, opts.ChecksumsPath)
	if err != nil {
		return nil, err
	}
	binary, err := ExtractBinary(zipData, opts.GOOS)
	if err != nil {
		return nil, err
	}

	// --check 在离线通道同样只查不下：校验并报告，一个字节都不写。
	if opts.CheckOnly {
		return map[string]any{
			"status":            "ok",
			"offline_zip":       opts.ZipPath,
			"checksum_verified": true,
			"binary_bytes":      len(binary),
			"message":           "offline package verified; re-run without --check to replace the install",
		}, nil
	}

	// 离线通道仍然要人点头：--zip 一旦生效就会换掉正在跑的二进制。
	if !opts.AssumeYes {
		cancelled := map[string]any{
			"status":            "cancelled",
			"offline_zip":       opts.ZipPath,
			"checksum_verified": true,
		}
		if !opts.IsTerminal {
			cancelled["message"] = "no terminal to confirm the update; re-run with --yes to apply"
			return cancelled, nil
		}
		fmt.Fprintf(opts.PromptOut, "Update now? [y/N]: ")
		if !readYes(opts.In) {
			return cancelled, nil
		}
	}

	target, err := resolveTarget(opts)
	if err != nil {
		return nil, err
	}
	if err := ReplaceExecutable(opts.GOOS, target, binary); err != nil {
		return nil, err
	}

	result := map[string]any{
		"status":            "updated",
		"restart_required":  true,
		"path":              target,
		"offline_zip":       opts.ZipPath,
		"checksum_verified": true,
	}
	if opts.GOOS == "windows" {
		result["previous_binary"] = target + ".old"
	}
	if hint := docsHint(ctx, target); hint != nil {
		result["docs_hint"] = hint
	}
	return result, nil
}

// ReadAndVerifyLocalZip 读本地 zip 与配对的 checksums.txt，并做「双源」
// 校验：zip 文件名必须在 checksums 里有对应行（来源一），且实测 sha256
// 必须与该行声明的值一致（来源二）。校验全过才返回 zip 内容——不过则
// 安装目录一个字节都不会动。缺 --checksums 直接拒绝：离线通道同样不许
// 安装未经验证的二进制。
func ReadAndVerifyLocalZip(zipPath, checksumsPath string) ([]byte, error) {
	if checksumsPath == "" {
		return nil, &Error{
			Code:    CodeChecksumInvalid,
			Message: "--zip requires --checksums <checksums.txt>: without the release checksums the zip cannot be verified (refusing to install unverified bits)",
			Data:    map[string]any{"zip": zipPath},
		}
	}
	zipData, err := os.ReadFile(zipPath)
	if err != nil {
		return nil, &Error{
			Code:    CodeArchiveInvalid,
			Message: fmt.Sprintf("read the offline package %s: %v", zipPath, err),
			Data:    map[string]any{"zip": zipPath},
		}
	}
	sumsData, err := os.ReadFile(checksumsPath)
	if err != nil {
		return nil, &Error{
			Code:    CodeChecksumInvalid,
			Message: fmt.Sprintf("read the checksums file %s: %v", checksumsPath, err),
			Data:    map[string]any{"checksums": checksumsPath},
		}
	}
	// 比对用 basename：checksums 行里写的永远是资产文件名，而调用方可能
	// 传全路径（如 ./dist/godot-ai-cli-0.1.0-windows-amd64.zip）。
	if err := verifyChecksum(filepath.Base(zipPath), zipData, sumsData); err != nil {
		return nil, err
	}
	return zipData, nil
}

// queryOps lists the op names ("<domain> <name>") the binary at exePath
// exposes, via `exe commands --json`; a package-level seam for tests.
var queryOps = defaultQueryOps

// currentOps lists the running (pre-update) binary's op names; a seam for
// tests. The production implementation reads this binary's own op table —
// at update time the RUNNING binary is the OLD version, so its table is
// exactly the pre-update surface.
var currentOps = defaultCurrentOps

// docsHintMessage tells the caller how to re-sync the skill reference docs
// after the binary changed underneath them.
const docsHintMessage = "sync the skill reference docs against the new binary: `godot-ai-cli commands --format md > references/commands.md` (and refresh the ops counts in SKILL.md when ops were added or removed)"

// docsHint compares the pre-update op surface against the freshly installed
// binary's. A changed surface (or an unqueryable new binary) yields a
// docs_hint payload; an unchanged surface returns nil — version bumps that
// add no ops need no doc sync.
func docsHint(ctx context.Context, newBinary string) map[string]any {
	newOps, err := queryOps(ctx, newBinary)
	if err != nil {
		return map[string]any{
			"message":        docsHintMessage,
			"ops_diff_error": err.Error(),
		}
	}
	added, removed := diffOpNames(currentOps(), newOps)
	if len(added) == 0 && len(removed) == 0 {
		return nil
	}
	hint := map[string]any{"message": docsHintMessage}
	if len(added) > 0 {
		hint["ops_added"] = added
	}
	if len(removed) > 0 {
		hint["ops_removed"] = removed
	}
	return hint
}

// defaultQueryOps runs `exe commands --json` and collects "<domain> <name>".
func defaultQueryOps(ctx context.Context, exe string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "commands", "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("run the new binary's commands --json: %v", err)
	}
	var payload struct {
		Ops []struct {
			Domain string `json:"domain"`
			Name   string `json:"name"`
		} `json:"ops"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, fmt.Errorf("decode the new binary's commands --json: %v", err)
	}
	names := make([]string, 0, len(payload.Ops))
	for _, op := range payload.Ops {
		names = append(names, op.Domain+" "+op.Name)
	}
	return names, nil
}

// defaultCurrentOps reads the running binary's own op table.
func defaultCurrentOps() []string {
	all := ops.All()
	names := make([]string, 0, len(all))
	for _, op := range all {
		names = append(names, op.Domain+" "+op.Name)
	}
	return names
}

// diffOpNames returns the sorted op names added and removed between the old
// and the new surface.
func diffOpNames(oldOps, newOps []string) (added, removed []string) {
	oldSet := make(map[string]bool, len(oldOps))
	for _, n := range oldOps {
		oldSet[n] = true
	}
	newSet := make(map[string]bool, len(newOps))
	for _, n := range newOps {
		newSet[n] = true
		if !oldSet[n] {
			added = append(added, n)
		}
	}
	for _, n := range oldOps {
		if !newSet[n] {
			removed = append(removed, n)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

// withStatus copies m and stamps the status field onto the copy.
func withStatus(m map[string]any, status string) map[string]any {
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	out["status"] = status
	return out
}

// readYes reads one answer line; only y/yes (any case) mean yes.
func readYes(in io.Reader) bool {
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// resolveTarget picks the executable to replace: <dir>/godot-ai-cli[.exe]
// for an explicit install dir, otherwise the running executable itself
// (whatever os.Executable resolves to, even inside a temp/test path).
func resolveTarget(opts Options) (string, error) {
	if opts.InstallDir != "" {
		return filepath.Join(opts.InstallDir, BinaryName(opts.GOOS)), nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", &Error{Code: CodeReplaceFailed, Message: fmt.Sprintf("resolve the running executable: %v", err)}
	}
	return exe, nil
}

// FetchLatestRelease GETs <base>/repos/<owner>/<repo>/releases?per_page=10
// and picks the newest non-draft entry. The list endpoint is used instead
// of /releases/latest because GitHub's "latest" EXCLUDES pre-releases —
// with only a beta published, /latest answers 404 even though a release
// exists. Pre-releases are deliberately included: the project ships beta
// tags and `update` must find them.
func FetchLatestRelease(ctx context.Context, client *http.Client, baseURL, owner, repo string) (Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=10",
		strings.TrimSuffix(baseURL, "/"), owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return Release{}, &Error{
			Code:    CodeCheckFailed,
			Message: fmt.Sprintf("query the releases list: %v", err),
			Data:    map[string]any{"url": url},
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, checkStatusError(resp, url)
	}
	var rels []Release
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		return Release{}, &Error{
			Code:    CodeCheckFailed,
			Message: fmt.Sprintf("decode the releases payload: %v", err),
			Data:    map[string]any{"url": url},
		}
	}
	for _, rel := range rels {
		if rel.Draft || rel.TagName == "" {
			continue
		}
		return rel, nil
	}
	return Release{}, &Error{
		Code:    CodeCheckFailed,
		Message: "no release has been published yet (the releases list is empty)",
		Data:    map[string]any{"url": url},
	}
}

// FetchReleaseByTag GETs <base>/repos/<owner>/<repo>/releases/tags/<tag>：
// 单点查询，绕开列表端点。限流窗口里列表端点先挂、单点查询常常还能用，
// 因此这条既是 --tag 的实现，也是 --from-atom 解析出 tag 之后的取资产
// 步骤；命中后与在线通道共用同一套资产选择/sha256 校验/替换流程。
func FetchReleaseByTag(ctx context.Context, client *http.Client, baseURL, owner, repo, tag string) (Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s",
		strings.TrimSuffix(baseURL, "/"), owner, repo, urlpkg.PathEscape(tag))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return Release{}, &Error{
			Code:    CodeCheckFailed,
			Message: fmt.Sprintf("query the release for tag %s: %v", tag, err),
			Data:    map[string]any{"url": url, "tag": tag},
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Release{}, &Error{
			Code:    CodeTagNotFound,
			Message: fmt.Sprintf("no release is published under tag %q — pass the tag exactly as published (for example v0.1.0)", tag),
			Data:    map[string]any{"url": url, "tag": tag, "http_status": resp.StatusCode},
		}
	}
	if resp.StatusCode != http.StatusOK {
		return Release{}, checkStatusError(resp, url)
	}
	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return Release{}, &Error{
			Code:    CodeCheckFailed,
			Message: fmt.Sprintf("decode the release payload for tag %s: %v", tag, err),
			Data:    map[string]any{"url": url, "tag": tag},
		}
	}
	if rel.TagName == "" {
		// 载荷缺 tag_name 时用请求的 tag 兜底：后续资产名由它推导。
		rel.TagName = tag
	}
	return rel, nil
}

// checkStatusError 把查询端点的非 200 响应分类成结构化错误。
//
// 403 且 X-RateLimit-Remaining: 0 才是「限流」：GitHub 对限流与权限不足
// 都回 403，两者唯一可靠的区分就是剩余额度归零，而限流是「等窗口过去就
// 好」、必须与权限/其它失败分开报（新码 UPDATE_CHECK_RATE_LIMITED），并
// 带上重置时间与三条降级通道指引。其余非 200 维持 UPDATE_CHECK_FAILED，
// 只补一个 http_status 让机器侧自行判定。
func checkStatusError(resp *http.Response, url string) *Error {
	if resp.StatusCode == http.StatusForbidden &&
		strings.TrimSpace(resp.Header.Get("X-RateLimit-Remaining")) == "0" {
		data := map[string]any{
			"url":         url,
			"http_status": resp.StatusCode,
			"next_steps":  rateLimitNextSteps(),
		}
		message := fmt.Sprintf("GitHub API rate limit reached for %s (%s)", url, resp.Status)
		if reset := rateLimitReset(resp); reset != "" {
			data["rate_limit_reset"] = reset
			message += fmt.Sprintf("; the limit resets at %s", reset)
		}
		message += " — the release list can be bypassed with --tag, --from-atom or --zip (see next_steps)"
		return &Error{Code: CodeCheckRateLimited, Message: message, Data: data}
	}
	return &Error{
		Code:    CodeCheckFailed,
		Message: fmt.Sprintf("GitHub API answered %s", resp.Status),
		Data:    map[string]any{"url": url, "http_status": resp.StatusCode},
	}
}

// rateLimitReset 把 X-RateLimit-Reset 的 unix 秒转成 RFC3339（UTC，避免
// 时区随机器漂移），不可解析时返回空串（字段就不写进 data）。
func rateLimitReset(resp *http.Response) string {
	sec, err := strconv.ParseInt(strings.TrimSpace(resp.Header.Get("X-RateLimit-Reset")), 10, 64)
	if err != nil {
		return ""
	}
	return time.Unix(sec, 0).UTC().Format(time.RFC3339)
}

// rateLimitNextSteps 是限流现场的下一步：API 不可达 ≠ 无法升级，三条通道
// 按「联网优先、离线兜底」排序（需求 R-6 的建议新增）。
func rateLimitNextSteps() []string {
	return []string{
		"显式版本走单点查询，绕开被限流的列表端点：godot-ai-cli update --tag vX.Y.Z",
		"API 不可达时降级到 releases.atom 取最新 tag：godot-ai-cli update --from-atom",
		"已下载好 zip 时完全离线安装（需配套 checksums.txt 双源校验）：godot-ai-cli update --zip <zip> --checksums <checksums.txt>",
	}
}

// atomFeed 是 releases.atom 里我们消费的最小子集：按 GitHub 的排布，
// entry 从最新到最旧。
type atomFeed struct {
	Entries []atomEntry `xml:"entry"`
}

// atomEntry 是一条 release 条目：链接指向 …/releases/tag/<tag>，标题在
// GitHub 的 feed 里就是 tag。
type atomEntry struct {
	Title string     `xml:"title"`
	Links []atomLink `xml:"link"`
}

// atomLink 只取 href：feed 里带 rel="alternate" 的那条正是 tag 链接。
type atomLink struct {
	Href string `xml:"href,attr"`
}

// FetchLatestTagFromAtom 在 API 不可达时降级到 releases.atom：解析最新
// 一条 entry 的 tag。atom 与列表端点一样包含 prerelease 条目（需求方的
// 手工替代流程已验证），因此 beta 版本照样看得到。取到 tag 之后由调用方
// 走 FetchReleaseByTag 那条路。网络/解析失败统一回 UPDATE_ATOM_FAILED。
func FetchLatestTagFromAtom(ctx context.Context, client *http.Client, baseURL, owner, repo string) (string, error) {
	url := fmt.Sprintf("%s/%s/%s/releases.atom", siteRoot(baseURL), owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", &Error{
			Code:    CodeAtomFailed,
			Message: fmt.Sprintf("query releases.atom: %v", err),
			Data:    map[string]any{"url": url},
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", &Error{
			Code:    CodeAtomFailed,
			Message: fmt.Sprintf("releases.atom answered %s", resp.Status),
			Data:    map[string]any{"url": url, "http_status": resp.StatusCode},
		}
	}
	var feed atomFeed
	if err := xml.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return "", &Error{
			Code:    CodeAtomFailed,
			Message: fmt.Sprintf("decode releases.atom: %v", err),
			Data:    map[string]any{"url": url},
		}
	}
	if len(feed.Entries) == 0 {
		return "", &Error{
			Code:    CodeAtomFailed,
			Message: "releases.atom carries no entry (no release has been published yet)",
			Data:    map[string]any{"url": url},
		}
	}
	tag := atomEntryTag(feed.Entries[0])
	if tag == "" {
		return "", &Error{
			Code:    CodeAtomFailed,
			Message: "releases.atom's newest entry names no tag",
			Data:    map[string]any{"url": url},
		}
	}
	return tag, nil
}

// atomEntryTag 取一条 entry 的 tag：优先 …/releases/tag/<tag> 链接，链接
// 缺失时退回标题（GitHub feed 的标题就是 tag）。
func atomEntryTag(entry atomEntry) string {
	const marker = "/releases/tag/"
	for _, link := range entry.Links {
		if i := strings.Index(link.Href, marker); i >= 0 {
			if tag := strings.Trim(link.Href[i+len(marker):], "/"); tag != "" {
				return tag
			}
		}
	}
	return strings.TrimSpace(entry.Title)
}

// siteRoot 从 API 根推导站点根：官方 API 主机 api.github.com 换成
// github.com（releases.atom 在站点上，不在 API 上）；其它情况（测试注入
// 的 httptest 服务器）同源直连。解析不出来时退回原值，让请求本身去报
// 网络错误。
func siteRoot(baseURL string) string {
	u, err := urlpkg.Parse(baseURL)
	if err != nil || u.Host == "" {
		return strings.TrimSuffix(baseURL, "/")
	}
	if u.Host == "api.github.com" {
		return "https://github.com"
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "https"
	}
	return scheme + "://" + u.Host
}

// semver is a parsed semantic version; pre holds the pre-release string.
type semver struct {
	major, minor, patch int
	pre                 string
}

// parseSemver parses "v?MAJOR.MINOR[.PATCH][-prerelease][+build]". The
// patch segment defaults to 0 — release tags and the injected build
// version are always full triples; the leniency only helps hand-typed
// input. Build metadata is ignored per semver §10.
func parseSemver(s string) (semver, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	v := semver{}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		v.pre = s[i+1:]
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return semver{}, fmt.Errorf("not a semantic version: %q", s)
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}, fmt.Errorf("not a semantic version: %q", s)
		}
		nums[i] = n
	}
	v.major, v.minor, v.patch = nums[0], nums[1], nums[2]
	return v, nil
}

// CompareVersions returns -1/0/+1 for a <==/> b under semver precedence:
// the numeric major.minor.patch triple decides first; on a tie a version
// WITH a pre-release is older than the same triple without one (so the
// build-in "0.0.0-dev" and any other -dev/pre-release build is older than
// the corresponding stable release); two pre-releases compare per semver
// §11 (numeric identifiers rank below alphanumeric, fewer identifiers
// below more).
func CompareVersions(a, b string) (int, error) {
	va, err := parseSemver(a)
	if err != nil {
		return 0, err
	}
	vb, err := parseSemver(b)
	if err != nil {
		return 0, err
	}
	for _, pair := range [][2]int{{va.major, vb.major}, {va.minor, vb.minor}, {va.patch, vb.patch}} {
		if pair[0] != pair[1] {
			if pair[0] < pair[1] {
				return -1, nil
			}
			return 1, nil
		}
	}
	switch {
	case va.pre == "" && vb.pre == "":
		return 0, nil
	case va.pre == "":
		return 1, nil
	case vb.pre == "":
		return -1, nil
	}
	return comparePreRelease(va.pre, vb.pre), nil
}

// comparePreRelease orders two pre-release strings per semver §11.
func comparePreRelease(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aErr := strconv.Atoi(as[i])
		bn, bErr := strconv.Atoi(bs[i])
		switch {
		case aErr == nil && bErr == nil:
			if an != bn {
				if an < bn {
					return -1
				}
				return 1
			}
		case aErr == nil:
			return -1 // numeric identifiers rank below alphanumeric ones
		case bErr == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	switch {
	case len(as) < len(bs):
		return -1
	case len(as) > len(bs):
		return 1
	}
	return 0
}

// BinaryName is the binary file name inside the release zip (and inside an
// install dir) for the given target OS.
func BinaryName(goos string) string {
	if goos == "windows" {
		return "godot-ai-cli.exe"
	}
	return "godot-ai-cli"
}

// AssetName is the release-asset naming convention the release pipeline
// publishes: godot-ai-cli-<version>-<goos>-<goarch>.zip (version without a
// leading v).
func AssetName(ver, goos, goarch string) string {
	return fmt.Sprintf("godot-ai-cli-%s-%s-%s.zip", ver, goos, goarch)
}

// ChecksumsName is the checksums asset carrying `sha256  filename` lines.
func ChecksumsName(ver string) string {
	return fmt.Sprintf("godot-ai-cli-%s-checksums.txt", ver)
}

// SelectAssets picks the zip asset for the current platform plus the
// checksums asset out of a release.
func SelectAssets(rel Release, goos, goarch string) (asset, checksums Asset, err error) {
	ver := strings.TrimPrefix(rel.TagName, "v")
	wantAsset, wantSums := AssetName(ver, goos, goarch), ChecksumsName(ver)
	available := make([]string, 0, len(rel.Assets))
	for _, a := range rel.Assets {
		available = append(available, a.Name)
		switch a.Name {
		case wantAsset:
			asset = a
		case wantSums:
			checksums = a
		}
	}
	if asset.Name == "" {
		return Asset{}, Asset{}, &Error{
			Code:    CodeAssetNotFound,
			Message: fmt.Sprintf("release %s ships no asset %s for this platform", rel.TagName, wantAsset),
			Data:    map[string]any{"goos": goos, "goarch": goarch, "available_assets": available},
		}
	}
	if checksums.Name == "" {
		return Asset{}, Asset{}, &Error{
			Code:    CodeChecksumInvalid,
			Message: fmt.Sprintf("release %s ships no checksums asset %s — refusing to install unverified bits", rel.TagName, wantSums),
			Data:    map[string]any{"available_assets": available},
		}
	}
	return asset, checksums, nil
}

// downloadAttempts / downloadBackoff 是 release 资产下载的重试预算：
// TUN/fake-ip 代理环境下的典型失败是「连接中途被掐断」，一次重试常常
// 就过；4xx 不重试（重试无意义），指数退避避免对脆弱链路雪上加霜。
const (
	downloadAttempts = 3
	downloadBackoff  = time.Second
)

// downloadDiag 是一次下载尝试的诊断快照——UPDATE_DOWNLOAD_FAILED 的
// data 字段全部来自这里（需求 update-behind-tun-fakeip §4.2：错误必须
// 可诊断，机器侧能据此自动切代理重试）。
type downloadDiag struct {
	URL           string `json:"url"`                     // 资产 URL
	HTTPStatus    int    `json:"http_status"`             // 0 = 连接层失败
	ContentLength int64  `json:"content_length"`          // 声明长度（-1 = 未知）
	BytesRead     int64  `json:"bytes_read"`              // 实际读到
	RedirectHost  string `json:"redirect_host,omitempty"` // 302 后的最终落点
	ProxyUsed     string `json:"proxy_used,omitempty"`    // 生效的代理
	Attempts      int    `json:"attempts"`                // 总尝试次数
}

// DownloadAndVerify fetches the asset and the checksums file and returns
// the asset bytes only when the SHA256 matches. Everything happens in
// memory, so a mismatch provably leaves the install untouched.
func DownloadAndVerify(ctx context.Context, client *http.Client, asset, checksums Asset) ([]byte, error) {
	zipData, err := downloadWithRetry(ctx, client, asset.BrowserDownloadURL)
	if err != nil {
		return nil, err
	}
	sumsData, err := downloadWithRetry(ctx, client, checksums.BrowserDownloadURL)
	if err != nil {
		return nil, err
	}
	if err := verifyChecksum(asset.Name, zipData, sumsData); err != nil {
		return nil, err
	}
	return zipData, nil
}

// downloadWithRetry 包一层重试：连接层失败 / 5xx / 读取中断都重试到
// downloadAttempts 次，最后一次失败的诊断进 Error.Data。
func downloadWithRetry(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= downloadAttempts; attempt++ {
		data, diag, err := downloadOnce(ctx, client, url)
		if err == nil {
			return data, nil
		}
		diag.Attempts = attempt
		lastErr = downloadError(url, diag, err)
		if !retryableDownload(err, diag) {
			break // 4xx 等确定性失败不重试
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(downloadBackoff << (attempt - 1)):
		}
	}
	return nil, lastErr
}

// retryableDownload：4xx（客户端错误）重试无意义，其余（连接层/5xx/
// 读取中断）都值得再试。判定看原始 err（EOF 系=中断）与 diag.HTTPStatus
// （0=连接层、>=500=服务端）；包装后的 *Error 只带 http_status 的副本，
// 2xx 中断会被误判成「成功响应」。
func retryableDownload(err error, diag downloadDiag) bool {
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	return diag.HTTPStatus == 0 || diag.HTTPStatus >= 500
}

// downloadError 把最后一次失败包装成带诊断 data 的 Error：unexpected
// EOF 换成「中断」文案并给出可执行下一步（显式代理示例 + 手动安装三
// 步），不再是一句没头没尾的连接错误。
func downloadError(url string, diag downloadDiag, cause error) *Error {
	message := fmt.Sprintf("download %s: %v", url, cause)
	if errors.Is(cause, io.ErrUnexpectedEOF) || errors.Is(cause, io.EOF) {
		message = fmt.Sprintf("download %s: 连接在 %d/%d 字节处中断（可能被本地代理/TUN 中断）",
			url, diag.BytesRead, diag.ContentLength)
	}
	message += ("；可尝试：① 显式代理重试：godot-ai-cli update --proxy http://127.0.0.1:7897" +
		"（--proxy auto 读取系统代理）② 手动安装：下载 zip → 按 checksums 校验 sha256 → 替换可执行文件")
	data := map[string]any{
		"url":            diag.URL,
		"http_status":    diag.HTTPStatus,
		"content_length": diag.ContentLength,
		"bytes_read":     diag.BytesRead,
		"attempts":       diag.Attempts,
	}
	if diag.RedirectHost != "" {
		data["redirect_host"] = diag.RedirectHost
	}
	if diag.ProxyUsed != "" {
		data["proxy_used"] = diag.ProxyUsed
	}
	return &Error{Code: CodeDownloadFailed, Message: message, Data: data}
}

// countingReader 给 io.ReadAll 挂上字节计数——「声明 100 只收到 30」
// 这类中断必须可观测。
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// downloadOnce 单次下载尝试：返回 body 与诊断快照。失败也带回快照
// （bytes_read 等字段对诊断一样有价值）。
func downloadOnce(ctx context.Context, client *http.Client, url string) ([]byte, downloadDiag, error) {
	diag := downloadDiag{URL: url, ContentLength: -1}
	if url == "" {
		return nil, diag, &Error{Code: CodeDownloadFailed, Message: "the release asset carries no download URL"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, diag, err
	}
	// 如实上报生效代理：显式 transport 从 client 的 Transport 读；
	// 默认 client 用 ProxyFromEnvironment 判定（含 HTTPS_PROXY 环境变量）。
	if transport, ok := client.Transport.(*http.Transport); ok && transport.Proxy != nil {
		if u, _ := transport.Proxy(req); u != nil {
			diag.ProxyUsed = u.String()
		}
	} else if u, _ := http.ProxyFromEnvironment(req); u != nil {
		diag.ProxyUsed = u.String()
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, diag, err
	}
	defer resp.Body.Close()
	diag.HTTPStatus = resp.StatusCode
	diag.ContentLength = resp.ContentLength
	if resp.Request != nil && resp.Request.URL != nil {
		diag.RedirectHost = resp.Request.URL.Host
	}
	if resp.StatusCode != http.StatusOK {
		return nil, diag, fmt.Errorf("server answered %s", resp.Status)
	}
	cr := &countingReader{r: resp.Body}
	data, err := io.ReadAll(cr)
	diag.BytesRead = cr.n
	if err != nil {
		return nil, diag, err
	}
	return data, diag, nil
}

// verifyChecksum looks up the asset's `sha256  filename` line and compares
// it against the downloaded bytes.
func verifyChecksum(name string, data, checksums []byte) error {
	want := ""
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// sha256sum marks binary mode with a leading '*' on the filename.
		if strings.TrimPrefix(fields[len(fields)-1], "*") == name {
			want = fields[0]
			break
		}
	}
	if want == "" {
		return &Error{
			Code:    CodeChecksumInvalid,
			Message: fmt.Sprintf("the checksums file has no entry for %s — refusing to install unverified bits", name),
		}
	}
	got := fmt.Sprintf("%x", sha256.Sum256(data))
	if !strings.EqualFold(got, want) {
		return &Error{
			Code:    CodeChecksumMismatch,
			Message: fmt.Sprintf("checksum mismatch for %s: expected %s, got %s — the install was not modified", name, want, got),
		}
	}
	return nil
}

// ExtractBinary returns the godot-ai-cli[.exe] payload from the release zip.
func ExtractBinary(zipData []byte, goos string) ([]byte, error) {
	want := BinaryName(goos)
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, &Error{Code: CodeArchiveInvalid, Message: fmt.Sprintf("unzip the release asset: %v", err)}
	}
	for _, f := range zr.File {
		// Zip paths always use forward slashes, hence path (not filepath).
		if f.FileInfo().IsDir() || path.Base(f.Name) != want {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, &Error{Code: CodeArchiveInvalid, Message: fmt.Sprintf("read %s from the asset: %v", want, err)}
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, &Error{Code: CodeArchiveInvalid, Message: fmt.Sprintf("read %s from the asset: %v", want, err)}
		}
		return data, nil
	}
	return nil, &Error{Code: CodeArchiveInvalid, Message: fmt.Sprintf("the release asset does not contain %s", want)}
}

// writeFile and rename are the os.WriteFile / os.Rename seams the Windows
// replace path uses; tests swap them to inject mid-swap failures.
var (
	writeFile = os.WriteFile
	rename    = os.Rename
)

// ReplaceExecutable swaps the binary at target for data.
//
// Windows cannot overwrite a running executable, but it CAN rename one:
// the current binary moves to <target>.old (CleanupStaleBinary removes it
// on the next startup) and the new binary is written at the original path.
// Unix replaces atomically: same-directory temp file, chmod, rename.
func ReplaceExecutable(goos, target string, data []byte) error {
	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		return &Error{Code: CodeReplaceFailed, Message: fmt.Sprintf("no existing executable at %s to replace", target)}
	}

	if goos == "windows" {
		old := target + ".old"
		// Windows refuses to rename over an existing file, so a leftover
		// from a previous update goes first.
		if err := os.Remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
			return &Error{Code: CodeReplaceFailed, Message: fmt.Sprintf("remove the stale backup %s: %v", old, err)}
		}
		if err := rename(target, old); err != nil {
			return &Error{Code: CodeReplaceFailed, Message: fmt.Sprintf("move %s aside: %v", target, err)}
		}
		if err := writeFile(target, data, 0o755); err != nil {
			// Roll back so the install is never left without a binary. When
			// even the rollback fails, the message must say exactly where
			// the previous binary survives for manual recovery.
			if rbErr := rename(old, target); rbErr != nil {
				return &Error{Code: CodeReplaceFailed, Message: fmt.Sprintf(
					"write the new binary to %s: %v; rolling back also failed: %v — the previous binary survives at %s",
					target, err, rbErr, old)}
			}
			return &Error{Code: CodeReplaceFailed, Message: fmt.Sprintf("write the new binary to %s: %v", target, err)}
		}
		return nil
	}

	tmp, err := os.CreateTemp(filepath.Dir(target), ".godot-ai-cli-update-*")
	if err != nil {
		return &Error{Code: CodeReplaceFailed, Message: fmt.Sprintf("stage the new binary next to %s: %v", target, err)}
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return &Error{Code: CodeReplaceFailed, Message: fmt.Sprintf("stage the new binary: %v", err)}
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return &Error{Code: CodeReplaceFailed, Message: fmt.Sprintf("mark the new binary executable: %v", err)}
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return &Error{Code: CodeReplaceFailed, Message: fmt.Sprintf("stage the new binary: %v", err)}
	}
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Remove(tmpName)
		return &Error{Code: CodeReplaceFailed, Message: fmt.Sprintf("replace %s: %v", target, err)}
	}
	return nil
}

// CleanupStaleBinary removes the <exe>.old backup a Windows self-update
// left next to the running executable. It runs on every CLI startup, so it
// is deliberately best-effort and silent.
func CleanupStaleBinary() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cleanupStaleBackup(exe)
}

// cleanupStaleBackup does the work for one executable path (testable
// without being mid-exec).
func cleanupStaleBackup(exe string) {
	old := exe + ".old"
	if info, err := os.Stat(old); err == nil && !info.IsDir() {
		// Can still fail while an old daemon keeps the image mapped on
		// Windows — ignored here, retried on the next startup.
		_ = os.Remove(old)
	}
}
