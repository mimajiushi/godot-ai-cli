package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fsWrite 在临时工程里落一个文件（自动建目录）。
func fsWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fsFixture 建一个临时工程：4 个被守卫的工程文件 + .godot/ 与 .git/ 里各一个
// 该被排除的文件，外加 resources/bin/keep.txt（名字叫 bin 但不在工程根，
// 必须仍然被守卫覆盖）。
func fsFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	fsWrite(t, filepath.Join(dir, "project.godot"), "; fixture\n")
	fsWrite(t, filepath.Join(dir, "a.txt"), "one\n")
	fsWrite(t, filepath.Join(dir, "sub", "b.txt"), "two\n")
	fsWrite(t, filepath.Join(dir, "resources", "bin", "keep.txt"), "keep\n")
	fsWrite(t, filepath.Join(dir, ".godot", "imported", "x.import"), "cache\n")
	fsWrite(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main\n")
	return dir
}

// runFS 执行一条 filesystem 子命令并返回解析后的 JSON。
func runFS(t *testing.T, args ...string) map[string]any {
	t.Helper()
	root := NewRootCommand()
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("filesystem %v: %v\n%s", args, err, buf.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, buf.String())
	}
	return payload
}

// runFSErr 执行一条应当失败的 filesystem 子命令（经 root 的 execute 包装，
// 于是结构化错误信封会被打印）。
func runFSErr(t *testing.T, args ...string) string {
	t.Helper()
	root := NewRootCommand()
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(args)
	if code := execute(root); code == 0 {
		t.Fatalf("filesystem %v succeeded, want error", args)
	}
	return buf.String()
}

// TestFilesystemSnapshotChanged: snapshot → 改/增/删 → changed 只报 modified，
// 新增与删除分别只出现在 added[] / removed[]（对齐需求 4.1 的期望形状，V-2），
// 且 .godot/（含快照自己）与 .git/ 永远不出现在差异里。
func TestFilesystemSnapshotChanged(t *testing.T) {
	dir := fsFixture(t)

	snap := runFS(t, "filesystem", "snapshot", "--tag", "before", "--project", dir)
	wantPath := filepath.Join(dir, ".godot", "godot_ai_snapshots", "before.json")
	if snap["snapshot"] != wantPath {
		t.Errorf("snapshot = %v, want %s", snap["snapshot"], wantPath)
	}
	if snap["file_count"].(float64) != 4 {
		t.Errorf("file_count = %v, want 4", snap["file_count"])
	}
	// .godot/imported/x.import 与 .git/HEAD 被排除（此刻快照文件还没写）。
	if snap["ignored_count"].(float64) != 2 {
		t.Errorf("ignored_count = %v, want 2", snap["ignored_count"])
	}
	if excluded, _ := snap["excluded"].([]any); len(excluded) == 0 {
		t.Error("excluded 清单不能为空（帮助文本与回包共用同一口径）")
	}

	// 快照文件必须记录相对路径 + sha256 + size + mtime。
	raw, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("快照文件没落盘：%v", err)
	}
	var stored struct {
		Files []struct {
			Path   string `json:"path"`
			Size   int64  `json:"size"`
			SHA256 string `json:"sha256"`
			MTime  string `json:"mtime"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("快照不是合法 JSON: %v", err)
	}
	if len(stored.Files) != 4 {
		t.Fatalf("快照里 %d 个文件, want 4", len(stored.Files))
	}
	for _, f := range stored.Files {
		if len(f.SHA256) != 64 || f.MTime == "" || f.Size <= 0 {
			t.Errorf("快照条目 %+v 缺 sha256/size/mtime", f)
		}
		if strings.Contains(f.Path, "\\") {
			t.Errorf("快照路径 %q 必须是斜杠分隔（跨平台可 diff）", f.Path)
		}
	}

	// 改一个、加一个、删一个。
	fsWrite(t, filepath.Join(dir, "a.txt"), "one changed\n")
	fsWrite(t, filepath.Join(dir, "c.txt"), "three\n")
	if err := os.Remove(filepath.Join(dir, "sub", "b.txt")); err != nil {
		t.Fatal(err)
	}

	ch := runFS(t, "filesystem", "changed", "--tag", "before", "--project", dir)
	if ch["tag"] != "before" || ch["snapshot_created_at"] == "" {
		t.Errorf("tag/snapshot_created_at = %v / %v", ch["tag"], ch["snapshot_created_at"])
	}
	// changed[] 只放内容被改的文件（V-2）：新增/删除各自只出现在 added[]/removed[]。
	changes, _ := ch["changed"].([]any)
	if len(changes) != 1 {
		t.Fatalf("changed = %v, want 只有 a.txt（新增/删除不重复列进 changed）", changes)
	}
	entry := changes[0].(map[string]any)
	if entry["path"] != "a.txt" || entry["status"] != "modified" {
		t.Errorf("changed[0] = %v, want {path:a.txt, status:modified}", entry)
	}
	if entry["sha_before"] == "" || entry["sha_after"] == "" || entry["sha_before"] == entry["sha_after"] {
		t.Errorf("a.txt = %v, want modified + 两个不同的 sha", entry)
	}
	// 明确断言新增/删除的路径没有混进 changed。
	for _, c := range changes {
		m := c.(map[string]any)
		if m["status"] != "modified" {
			t.Errorf("changed 里出现 status=%v 的条目：%v", m["status"], m)
		}
		if p := m["path"].(string); p == "c.txt" || p == "sub/b.txt" {
			t.Errorf("新增/删除的 %q 不该出现在 changed 里", p)
		}
	}
	added, _ := ch["added"].([]any)
	removed, _ := ch["removed"].([]any)
	if len(added) != 1 || added[0] != "c.txt" {
		t.Errorf("added = %v, want [c.txt]", added)
	}
	if len(removed) != 1 || removed[0] != "sub/b.txt" {
		t.Errorf("removed = %v, want [sub/b.txt]", removed)
	}
	if ch["scanned"].(float64) != 4 {
		t.Errorf("scanned = %v, want 4（project.godot + a.txt + c.txt + resources/bin/keep.txt）", ch["scanned"])
	}
	// 此刻 .godot 里有 x.import + before.json 两个文件，.git 里 1 个 → 3 个被忽略；
	// 快照自己也在排除清单里，所以永远不会出现在差异中。
	if ch["ignored_count"].(float64) != 3 {
		t.Errorf("ignored_count = %v, want 3", ch["ignored_count"])
	}

	// 再拍一次快照 → 立刻比对必须干净（changed/added/removed 都是空数组）。
	runFS(t, "filesystem", "snapshot", "--tag", "after", "--project", dir)
	clean := runFS(t, "filesystem", "changed", "--tag", "after", "--project", dir)
	for _, key := range []string{"changed", "added", "removed"} {
		arr, ok := clean[key].([]any)
		if !ok || len(arr) != 0 {
			t.Errorf("干净状态下 %s = %v, want 空数组", key, clean[key])
		}
	}
}

// TestFilesystemChangedContentOnly: 内容没变（mtime 变了、字节一样）不算改动；
// 守卫只按 sha256 判定。
func TestFilesystemChangedContentOnly(t *testing.T) {
	dir := fsFixture(t)
	runFS(t, "filesystem", "snapshot", "--tag", "base", "--project", dir)

	// 重写同样的字节（mtime 会变，sha 不变）。
	fsWrite(t, filepath.Join(dir, "a.txt"), "one\n")
	ch := runFS(t, "filesystem", "changed", "--tag", "base", "--project", dir)
	for _, key := range []string{"changed", "added", "removed"} {
		if arr, ok := ch[key].([]any); !ok || len(arr) != 0 {
			t.Errorf("字节相同的重写：%s = %v, want 空数组", key, ch[key])
		}
	}
}

// TestFilesystemSnapshotErrors: 非法 tag / 非工程目录 / 不存在的快照名。
func TestFilesystemSnapshotErrors(t *testing.T) {
	dir := fsFixture(t)

	for _, badTag := range []string{"../evil", "a/b", "..", "with space"} {
		out := runFSErr(t, "filesystem", "snapshot", "--tag", badTag, "--project", dir)
		if !strings.Contains(out, "INVALID_PARAMS") {
			t.Errorf("tag %q want INVALID_PARAMS, got %s", badTag, out)
		}
	}
	if out := runFSErr(t, "filesystem", "snapshot", "--tag", "ok", "--project", t.TempDir()); !strings.Contains(out, "INVALID_PARAMS") {
		t.Errorf("非工程目录 want INVALID_PARAMS, got %s", out)
	}
	if out := runFSErr(t, "filesystem", "changed", "--tag", "nope", "--project", dir); !strings.Contains(out, "SNAPSHOT_NOT_FOUND") {
		t.Errorf("未知 tag want SNAPSHOT_NOT_FOUND, got %s", out)
	}
	if out := runFSErr(t, "filesystem", "snapshot", "--project", dir); !strings.Contains(out, "USAGE_ERROR") {
		t.Errorf("缺 --tag want USAGE_ERROR, got %s", out)
	}
	if out := runFSErr(t, "filesystem", "changed", "--tag", "../../x", "--project", dir); !strings.Contains(out, "INVALID_PARAMS") {
		t.Errorf("changed 的非法 tag want INVALID_PARAMS, got %s", out)
	}
}
