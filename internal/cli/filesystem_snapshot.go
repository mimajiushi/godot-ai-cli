// filesystem snapshot / filesystem changed：本地工作区哈希快照与差异
// （需求 R-6 4.1）。两条都是纯本地叶子——不经 daemon、不需要编辑器，所以
// 挂在 filesystem 域下但不在 ops 表里（见 mdNonOpLeaves / commands.md）。
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mimajiushi/godot-ai-cli/internal/fssnapshot"
)

// snapshotHelpNote 是两条命令共用的口径说明（帮助文本里只说一次，保证
// "排除清单"与"判定依据"在 -h 里可查）。
const snapshotHelpNote = `Snapshot semantics (identical for both commands):
  - every file under the project is recorded with its relative path, sha256,
    size and mtime; the snapshot is written to
    <project>/.godot/godot_ai_snapshots/<tag>.json.
  - excluded from the walk (and counted in ignored_count):
      .godot/ and .git/ at any depth (import cache / editor state / VCS metadata),
      .mono/, bin/, obj/, .vs/, .idea/ at the project root only (C# and IDE
      build output — a resource directory that happens to be called bin/ deeper
      down is still tracked).
  - a change is decided by CONTENT (sha256): a file rewritten with identical
    bytes is not reported, even though its mtime changed. mtime is recorded for
    reference only.
  - no git required: untracked and git-ignored project files are all covered.`

// resolveSnapshotProjectDir 解析工程根：显式 --project（复用 plugin.go 的
// resolveProjectDir 做 project.godot 校验）> 当前目录 > 上次 launch 记录的
// 工程。当前目录排在记录之前：守卫命令最怕"守错了工程"，而"我在哪个工程里
// 就守哪个工程"没有歧义（回包始终回报 project 绝对路径，选错也看得见）。
// 后两级都要求目录里真的有 project.godot。
func resolveSnapshotProjectDir(project string) (string, error) {
	if project != "" {
		return resolveProjectDir(project)
	}
	if wd, err := os.Getwd(); err == nil {
		if root, err := resolveProjectDir(wd); err == nil {
			return root, nil
		}
	}
	if rec, ok := readLastDaemon(); ok && rec.Project != "" {
		if root, err := resolveProjectDir(rec.Project); err == nil {
			return root, nil
		}
	}
	return "", fmt.Errorf("--project is required: the current directory is not a Godot project and no project is recorded by a prior launch")
}

// newFilesystemSnapshotCommand 记录当前工作区的文件哈希快照。
func newFilesystemSnapshotCommand() *cobra.Command {
	var (
		tag     string
		project string
	)
	cmd := &cobra.Command{
		Use:   "snapshot --tag <name> [--project <dir>]",
		Short: "Record a local workspace hash snapshot (to spot files an editor save rewrote)",
		Long: `filesystem snapshot hashes every file of the project and stores the
result as .godot/godot_ai_snapshots/<tag>.json, so a later
"filesystem changed --tag" can name the files something else wrote. This is the
guard for the "editor save silently re-serialized unrelated .tres resources"
class of collateral changes.

` + snapshotHelpNote + `

Examples:
  godot-ai-cli filesystem snapshot --tag before-fix --project .
  godot-ai-cli filesystem snapshot --tag before-fix          (the current dir when it is a Godot project)`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := fssnapshot.ValidateTag(tag); err != nil {
				return jsonError(cmd, "INVALID_PARAMS", err.Error(), nil)
			}
			root, err := resolveSnapshotProjectDir(project)
			if err != nil {
				return jsonError(cmd, "INVALID_PARAMS", err.Error(), nil)
			}
			snap, err := fssnapshot.Take(root, tag)
			if err != nil {
				return jsonError(cmd, "SNAPSHOT_FAILED", err.Error(), nil)
			}
			path, err := fssnapshot.Save(root, snap)
			if err != nil {
				return jsonError(cmd, "SNAPSHOT_FAILED", err.Error(), nil)
			}
			return printJSON(cmd.OutOrStdout(), map[string]any{
				"tag":           tag,
				"project":       root,
				"snapshot":      path,
				"created_at":    snap.CreatedAt,
				"file_count":    snap.FileCount,
				"ignored_count": snap.IgnoredCount,
				"excluded":      snap.Excluded,
			}, prettyOutput)
		},
	}
	cmd.Flags().StringVar(&tag, "tag", "", "snapshot name (letters/digits/dot/underscore/hyphen; not all dots)")
	cmd.Flags().StringVar(&project, "project", "", "Godot project dir (default: the current dir when it is a Godot project, else the project of the last launch)")
	_ = cmd.MarkFlagRequired("tag")
	return cmd
}

// newFilesystemChangedCommand 列出快照之后被改/增/删的文件。
func newFilesystemChangedCommand() *cobra.Command {
	var (
		tag     string
		project string
	)
	cmd := &cobra.Command{
		Use:   "changed --tag <name> [--project <dir>]",
		Short: "List the project files changed since a snapshot (modified / added / removed)",
		Long: `filesystem changed re-hashes the project and diffs it against the
snapshot recorded under <tag>: changed[] lists the files whose CONTENT changed
(sha_before -> sha_after), while added[] / removed[] list the paths that
appeared or disappeared since the snapshot — each file shows up in exactly one
group, so "modified" and "added" stay distinguishable. It writes nothing — run
it after any op you suspect of collateral writes (scene save,
project run --autosave, node set-property, resource set-property, ...).

` + snapshotHelpNote + `

Response: tag, project, snapshot_created_at (when the baseline was taken),
changed[] (modified files only: {path, status:"modified", sha_before,
sha_after}), added[] / removed[] (paths of files that were not in / no longer
in the snapshot), scanned (files hashed now) and ignored_count.

Examples:
  godot-ai-cli filesystem changed --tag before-fix --project .
  godot-ai-cli filesystem changed --tag before-fix --pretty`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := fssnapshot.ValidateTag(tag); err != nil {
				return jsonError(cmd, "INVALID_PARAMS", err.Error(), nil)
			}
			root, err := resolveSnapshotProjectDir(project)
			if err != nil {
				return jsonError(cmd, "INVALID_PARAMS", err.Error(), nil)
			}
			before, path, ok, err := fssnapshot.Load(root, tag)
			if err != nil {
				return jsonError(cmd, "SNAPSHOT_FAILED", err.Error(), nil)
			}
			if !ok {
				return jsonError(cmd, "SNAPSHOT_NOT_FOUND", fmt.Sprintf(
					"no snapshot %q for this project (looked for %s) — run `godot-ai-cli filesystem snapshot --tag %s` first",
					tag, path, tag), nil)
			}
			after, err := fssnapshot.Take(root, tag)
			if err != nil {
				return jsonError(cmd, "SNAPSHOT_FAILED", err.Error(), nil)
			}
			diff := fssnapshot.Diff(before, after)
			// changed[] 只放"内容被改"的文件：新增/删除各自成组（added[]/removed[]），
			// 不再重复列出——否则调用方按 changed 处理时会丢掉"新增 vs 修改"的区分，
			// 也与需求文档 4.1 的期望输出形状不符（V-2）。
			modified := make([]fssnapshot.Change, 0, len(diff.Changes))
			for _, c := range diff.Changes {
				if c.Status == "modified" {
					modified = append(modified, c)
				}
			}
			return printJSON(cmd.OutOrStdout(), map[string]any{
				"tag":                 tag,
				"project":             root,
				"snapshot":            path,
				"snapshot_created_at": before.CreatedAt,
				"snapshot_file_count": before.FileCount,
				"changed":             modified,
				"added":               diff.Added,
				"removed":             diff.Removed,
				"scanned":             diff.Scanned,
				"ignored_count":       diff.IgnoredCount,
			}, prettyOutput)
		},
	}
	cmd.Flags().StringVar(&tag, "tag", "", "snapshot name to diff against (letters/digits/dot/underscore/hyphen; not all dots)")
	cmd.Flags().StringVar(&project, "project", "", "Godot project dir (default: the current dir when it is a Godot project, else the project of the last launch)")
	_ = cmd.MarkFlagRequired("tag")
	return cmd
}
