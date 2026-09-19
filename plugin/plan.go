// 插件安装的只读预览与 git 影响面：`launch --dry-run` / `launch
// --no-plugin-upgrade` / `plugin status` / `plugin install --dry-run` 共用。
// 预览用与 Install 完全相同的 embedded 字节逐文件比较，因此"会改哪些文件"
// 与真正落盘的结果一致。
package plugin

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FileAction 是一次计划写入的类别。
type FileAction string

const (
	// ActionCreate: 项目里没有这个文件（新增）。
	ActionCreate FileAction = "create"
	// ActionUpdate: 文件已存在但内容不同（会被覆盖）。
	ActionUpdate FileAction = "update"
)

// FileChange 是一次计划写入。
type FileChange struct {
	Path   string     // 项目相对路径，斜杠分隔（addons/godot_ai/...）
	Action FileAction // create | update
}

// GitImpact 是计划与项目 git 状态的关系。Available=false（本机没有 git，或
// 项目不在工作树里）时各计数保持 0 且 Reason 说明原因——调用方绝不能把它读
// 成"没有跟踪文件"。
type GitImpact struct {
	Available       bool   `json:"available"`
	Reason          string `json:"reason,omitempty"`
	TrackedFiles    int    `json:"tracked_files"`
	TrackedUpdate   int    `json:"tracked_update"`
	UntrackedCreate int    `json:"untracked_create"`
	DirtyNow        bool   `json:"dirty_now"`
	DirtyAfter      bool   `json:"dirty_after"`
}

// Plan 是 Install 的只读预览。
type Plan struct {
	ProjectDir        string
	InstalledVersion  string // plugin.cfg 缺失或不可读时为 ""
	BundledVersion    string
	Changes           []FileChange // 按 path 排序
	WouldCreate       []string     // 排序后的项目相对路径
	WouldUpdate       []string     // 排序后的项目相对路径
	WouldEnablePlugin bool         // project.godot 需要补 godot_ai 的 enabled 项
	Git               GitImpact
}

// RequiresInstall 报告 Install 是否会写盘（含仅需 enable 的情况）。
func (p Plan) RequiresInstall() bool {
	return len(p.Changes) > 0 || p.WouldEnablePlugin
}

// VersionMismatch 报告已装插件与内置版本不同——全新安装（读不到 plugin.cfg）
// 不算 mismatch，否则 --no-plugin-upgrade 会让"第一次装"直接失败。
func (p Plan) VersionMismatch() bool {
	return p.InstalledVersion != "" && p.InstalledVersion != p.BundledVersion
}

// JSON 渲染 launch --dry-run / plugin install --dry-run / plugin status 共用
// 的计划载荷。字段名即协议的一部分：would_update / would_create 让使用者一眼
// 看出哪些文件不是自己的改动。
func (p Plan) JSON() map[string]any {
	create := p.WouldCreate
	if create == nil {
		create = []string{}
	}
	update := p.WouldUpdate
	if update == nil {
		update = []string{}
	}
	return map[string]any{
		"project":             p.ProjectDir,
		"installed":           p.InstalledVersion != "",
		"installed_version":   p.InstalledVersion,
		"bundled_version":     p.BundledVersion,
		"version_match":       !p.VersionMismatch(),
		"would_create":        create,
		"would_update":        update,
		"would_create_count":  len(create),
		"would_update_count":  len(update),
		"would_enable_plugin": p.WouldEnablePlugin,
		"git":                 p.Git,
	}
}

// Preview computes the Plan for one project without touching it. It walks the
// very same embedded file list Install writes, so "would_update" means the
// bytes on disk really differ (an mtime-only difference is never reported).
func Preview(projectDir string) (Plan, error) {
	files, err := installPlan()
	if err != nil {
		return Plan{}, fmt.Errorf("plan plugin install into %s: %w", addonDir(projectDir), err)
	}
	dest := addonDir(projectDir)
	out := Plan{ProjectDir: projectDir, BundledVersion: PluginVersion()}
	if v, err := InstalledVersion(projectDir); err == nil {
		out.InstalledVersion = v
	}
	for _, path := range files {
		rel := strings.TrimPrefix(path, "godot_ai/")
		want, err := FS.ReadFile(path)
		if err != nil {
			return Plan{}, fmt.Errorf("read embedded %s: %w", path, err)
		}
		projectRel := addonsRelPath + "/" + rel
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(rel)))
		switch {
		case err != nil && os.IsNotExist(err):
			out.Changes = append(out.Changes, FileChange{Path: projectRel, Action: ActionCreate})
		case err != nil:
			return Plan{}, fmt.Errorf("read %s: %w", filepath.Join(dest, filepath.FromSlash(rel)), err)
		case !bytes.Equal(got, want):
			out.Changes = append(out.Changes, FileChange{Path: projectRel, Action: ActionUpdate})
		}
	}
	sort.Slice(out.Changes, func(i, j int) bool { return out.Changes[i].Path < out.Changes[j].Path })
	for _, c := range out.Changes {
		if c.Action == ActionCreate {
			out.WouldCreate = append(out.WouldCreate, c.Path)
		} else {
			out.WouldUpdate = append(out.WouldUpdate, c.Path)
		}
	}
	needed, err := EnableDecision(projectDir)
	if err != nil {
		return Plan{}, err
	}
	out.WouldEnablePlugin = needed
	out.Git = gitImpact(projectDir, out.Changes)
	return out, nil
}

// VersionMismatchError is the refusal `launch --no-plugin-upgrade` returns when
// the project plugin is not the bundled build: the plan is in hand and nothing
// has been written.
type VersionMismatchError struct {
	Plan Plan
}

func (e *VersionMismatchError) Error() string {
	return fmt.Sprintf("project plugin %s differs from the bundled %s — refusing to modify the project without explicit consent (--no-plugin-upgrade)",
		e.Plan.InstalledVersion, e.Plan.BundledVersion)
}

// gitImpact probes the project's git state with two git calls. Every failure
// degrades to Available=false + Reason: a project outside git (or a machine
// without git) must still be able to preview and install the plugin.
func gitImpact(projectDir string, changes []FileChange) GitImpact {
	impact := GitImpact{UntrackedCreate: countAction(changes, ActionCreate)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// cmd.Dir (not `git -C`) is load-bearing: git prints paths relative to the
	// CURRENT DIRECTORY, which is exactly the project-relative form used for
	// the plan's paths. `-C` keeps the caller's cwd and would yield
	// repo-root-relative paths for a project inside a larger repository.
	tracked, err := gitLines(ctx, projectDir, "ls-files", "-z", "--", addonsRelPath)
	if err != nil {
		impact.Reason = err.Error()
		impact.DirtyAfter = len(changes) > 0
		return impact
	}
	impact.Available = true
	impact.TrackedFiles = len(tracked)
	trackedSet := make(map[string]bool, len(tracked))
	for _, p := range tracked {
		trackedSet[p] = true
	}
	for _, c := range changes {
		if c.Action == ActionUpdate && trackedSet[c.Path] {
			impact.TrackedUpdate++
		}
	}
	if dirty, err := gitLines(ctx, projectDir, "status", "--porcelain", "--", addonsRelPath); err != nil {
		impact.Reason = err.Error()
	} else {
		impact.DirtyNow = len(dirty) > 0
	}
	impact.DirtyAfter = impact.DirtyNow || len(changes) > 0
	return impact
}

// gitLines runs git in projectDir and returns its non-empty output lines
// (NUL-separated output is split on NUL as well, so paths with spaces survive).
func gitLines(ctx context.Context, projectDir string, args ...string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = projectDir
	out, err := cmd.Output()
	if err != nil {
		reason := strings.TrimSpace(err.Error())
		if ee, ok := err.(*exec.ExitError); ok {
			if msg := strings.TrimSpace(string(ee.Stderr)); msg != "" {
				reason = msg
			}
		}
		return nil, fmt.Errorf("git %s failed: %s", strings.Join(args, " "), reason)
	}
	var lines []string
	for _, chunk := range strings.Split(string(out), "\x00") {
		for _, line := range strings.Split(chunk, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				lines = append(lines, line)
			}
		}
	}
	return lines, nil
}

// countAction counts plan entries with the given action.
func countAction(changes []FileChange, action FileAction) int {
	n := 0
	for _, c := range changes {
		if c.Action == action {
			n++
		}
	}
	return n
}
