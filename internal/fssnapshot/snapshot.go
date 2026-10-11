// Package fssnapshot 记录并比对工程工作区的文件哈希快照（需求 R-6 4.1）。
//
// 触发场景：编辑器保存场景时会把"已加载的其它资源"一并重新序列化（抹掉手写
// 注释、删掉等于脚本默认值的属性、补 uid/load_steps），调用方光看 git status
// 很难判断"这次操作到底顺带改了哪些我没打算改的文件"。本包只做本地工作：
// 遍历工程 → 每个文件记 sha256 + size + mtime → 落一份 JSON 快照；之后一条
// 命令列出期间被改/增/删的文件。
//
// 快照位置：<工程>/.godot/godot_ai_snapshots/<tag>.json。.godot 本身在排除
// 清单里，所以快照不会把自己算成差异。不依赖 git：没有版本库、或文件根本没进
// 索引（untracked/ignored）的工程同样可用。
package fssnapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DirName 是快照目录（相对工程根，斜杠形式）。
const DirName = ".godot/godot_ai_snapshots"

// tagRe 限定快照名：字母/数字/点/下划线/连字符，禁止路径分隔符与 ..，
// 于是 tag 永远只能落到快照目录里（写不出工程外）。
var tagRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// ExcludedRules 是排除清单的"人话"版本——帮助文本与快照回包共用它，避免
// 文档和代码两处口径漂移。判定逻辑见 IsExcludedDir（与下面逐条对应）：
//   - .godot/ 与 .git/ 任何层级都跳过（引擎导入缓存 / VCS 元数据，子模块里
//     也会出现 .git）；
//   - C#（Godot mono 版）与常见 IDE 的产物目录只认工程根那一层，避免把工程
//     里真叫 bin/ 的资源目录误伤。
var ExcludedRules = []string{
	".godot/ (any depth — import cache, editor state, and this feature's own snapshot dir)",
	".git/ (any depth — VCS metadata)",
	".mono/ (project root — C# intermediate output)",
	"bin/ (project root — C#/IDE build output)",
	"obj/ (project root — C#/IDE build output)",
	".vs/ (project root — Visual Studio state)",
	".idea/ (project root — JetBrains state)",
}

// anyLevelExcluded 是任何层级都跳过的目录名。
var anyLevelExcluded = map[string]bool{".godot": true, ".git": true}

// rootOnlyExcluded 是只在工程根那一层跳过的目录名。
var rootOnlyExcluded = map[string]bool{
	".mono": true, "bin": true, "obj": true, ".vs": true, ".idea": true,
}

// IsExcludedDir 报告一个相对工程根的目录（rel 用斜杠分隔，name 为目录名）
// 是否在排除清单里。
func IsExcludedDir(rel, name string) bool {
	if anyLevelExcluded[name] {
		return true
	}
	if rel == name { // 工程根的那一层
		return rootOnlyExcluded[name]
	}
	return false
}

// FileEntry 是快照里的一个文件。Path 相对工程根、斜杠分隔，跨平台可 diff。
type FileEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	MTime  string `json:"mtime"` // RFC3339（UTC），仅供人看：差异只按 sha256 判定
}

// Snapshot 是一次 tag 的完整记录。
type Snapshot struct {
	Tag          string      `json:"tag"`
	Project      string      `json:"project"`
	CreatedAt    string      `json:"created_at"`
	Excluded     []string    `json:"excluded"`
	FileCount    int         `json:"file_count"`
	IgnoredCount int         `json:"ignored_count"`
	Files        []FileEntry `json:"files"`
}

// Change 是一个文件的差异。Status ∈ modified / added / removed：
// sha_before 只在 modified/removed 上出现，sha_after 只在 modified/added 上出现。
type Change struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	SHABefore string `json:"sha_before,omitempty"`
	SHAAfter  string `json:"sha_after,omitempty"`
}

// DiffResult 是快照与当前工作区的差异。
type DiffResult struct {
	Changes      []Change `json:"changed"`
	Added        []string `json:"added"`
	Removed      []string `json:"removed"`
	Scanned      int      `json:"scanned"`       // 本次遍历并哈希的文件数
	IgnoredCount int      `json:"ignored_count"` // 被排除清单跳过的文件数
}

// ValidateTag 校验快照名。
func ValidateTag(tag string) error {
	if tag == "" {
		return fmt.Errorf("--tag must not be empty")
	}
	if !tagRe.MatchString(tag) {
		return fmt.Errorf("--tag %q is not a valid snapshot name (allowed: letters, digits, dot, underscore, hyphen)", tag)
	}
	// 只有点的名字（"." / ".." / "..."）不含分隔符、走不出目录，但作为快照名
	// 只会让人误解，直接拒绝。
	if strings.Trim(tag, ".") == "" {
		return fmt.Errorf("--tag %q is not a valid snapshot name (it must contain something besides dots)", tag)
	}
	return nil
}

// SnapshotPath 是某个 tag 的快照文件路径。
func SnapshotPath(projectRoot, tag string) string {
	return filepath.Join(projectRoot, filepath.FromSlash(DirName), tag+".json")
}

// Save 把快照写到 <工程>/.godot/godot_ai_snapshots/<tag>.json（自动建目录）。
func Save(projectRoot string, snap *Snapshot) (string, error) {
	path := SnapshotPath(projectRoot, snap.Tag)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// Load 读取某个 tag 的快照；文件不存在时 ok=false（不是错误——调用方要给出
// "先 snapshot 再 changed"的提示）。
func Load(projectRoot, tag string) (*Snapshot, string, bool, error) {
	path := SnapshotPath(projectRoot, tag)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, path, false, nil
		}
		return nil, path, false, err
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, path, false, fmt.Errorf("%s is not a valid snapshot file: %v", path, err)
	}
	return &snap, path, true, nil
}

// Take 遍历工程、哈希每个文件，生成一份新的快照（写盘由 Save 负责）。
// 遍历顺序是词典序（WalkDir），因此 Files 与回包都是确定性的。遍历中遇到的
// 读取错误直接失败——静默跳过会让守卫漏掉文件，比报错更危险。
func Take(projectRoot, tag string) (*Snapshot, error) {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{
		Tag:       tag,
		Project:   filepath.ToSlash(root),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Excluded:  ExcludedRules,
		Files:     []FileEntry{},
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		relSlash := filepath.ToSlash(rel)
		if d.IsDir() {
			if IsExcludedDir(relSlash, d.Name()) {
				snap.IgnoredCount += countFiles(path)
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // 符号链接/设备文件等：不跟随、也不计入
		}
		entry, herr := hashFile(path, relSlash)
		if herr != nil {
			return herr
		}
		snap.Files = append(snap.Files, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	snap.FileCount = len(snap.Files)
	return snap, nil
}

// Diff 比较两份快照：按内容（sha256）判定 modified/added/removed，并按路径
// 排序保证回包可 diff。mtime 变化但内容相同不算改动——重新拷一份同样的内容
// 不该报成"被改写"。IgnoredCount 取 after 的遍历结果。
func Diff(before, after *Snapshot) DiffResult {
	res := DiffResult{Changes: []Change{}, Added: []string{}, Removed: []string{}}
	if after != nil {
		res.Scanned = len(after.Files)
		res.IgnoredCount = after.IgnoredCount
	}
	beforeByPath := map[string]FileEntry{}
	if before != nil {
		for _, f := range before.Files {
			beforeByPath[f.Path] = f
		}
	}
	afterByPath := map[string]FileEntry{}
	for _, f := range after.Files {
		afterByPath[f.Path] = f
		if b, ok := beforeByPath[f.Path]; ok {
			if b.SHA256 != f.SHA256 {
				res.Changes = append(res.Changes, Change{Path: f.Path, Status: "modified", SHABefore: b.SHA256, SHAAfter: f.SHA256})
			}
			continue
		}
		res.Changes = append(res.Changes, Change{Path: f.Path, Status: "added", SHAAfter: f.SHA256})
	}
	for _, f := range before.Files {
		if _, ok := afterByPath[f.Path]; !ok {
			res.Changes = append(res.Changes, Change{Path: f.Path, Status: "removed", SHABefore: f.SHA256})
		}
	}
	sort.Slice(res.Changes, func(i, j int) bool { return res.Changes[i].Path < res.Changes[j].Path })
	// added/removed 是 changed 的"只看新增 / 只看删除"视图，顺序与 changed 一致。
	for _, c := range res.Changes {
		switch c.Status {
		case "added":
			res.Added = append(res.Added, c.Path)
		case "removed":
			res.Removed = append(res.Removed, c.Path)
		}
	}
	return res
}

// hashFile 计算一个文件的 sha256/size/mtime。
func hashFile(path, rel string) (FileEntry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return FileEntry{}, fmt.Errorf("stat %s: %v", rel, err)
	}
	f, err := os.Open(path)
	if err != nil {
		return FileEntry{}, fmt.Errorf("open %s: %v", rel, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return FileEntry{}, fmt.Errorf("hash %s: %v", rel, err)
	}
	return FileEntry{
		Path:   rel,
		Size:   info.Size(),
		SHA256: hex.EncodeToString(h.Sum(nil)),
		MTime:  info.ModTime().UTC().Format(time.RFC3339),
	}, nil
}

// countFiles 统计一个目录（含子目录）里的常规文件数，best-effort：读不动就
// 停下——它只用来告诉调用方"有多少文件没被守卫覆盖"。
func countFiles(dir string) int {
	n := 0
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && d.Type().IsRegular() {
			n++
		}
		return nil
	})
	return n
}
