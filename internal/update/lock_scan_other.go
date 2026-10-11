//go:build !windows

package update

// processesLocking 在非 Windows 平台上恒返回空数组：Unix 的替换走同目录临时
// 文件 + 原子改名，不存在「运行中的映像删不掉」这类占用语义，UPDATE_REPLACE_FAILED
// 的 locked_by 因此是空数组而不是 null——机器侧无需按平台分支处理字段形状
// （需求 R-7 b）。
func processesLocking(string) []map[string]any {
	return []map[string]any{}
}
