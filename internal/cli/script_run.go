// `script run`：不依赖编辑器/daemon 的独立脚本执行（headless 探针）。
// 用解析出的引擎二进制跑 `godot [--headless] --path <project> --script <res://...>`，
// 等进程退出后返回 exit code + stdout/stderr——回答".tres 能不能写注释"这类
// 实测型问题不再需要开编辑器，也不用记住引擎安装路径。
package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mimajiushi/godot-ai-cli/internal/godot"
)

// newScriptRunCommand 本地执行探针脚本（挂在 script domain 下，不经 daemon）。
func newScriptRunCommand() *cobra.Command {
	var (
		project    string
		script     string
		godotFlag  string
		windowed   bool
		timeoutSec int
	)
	cmd := &cobra.Command{
		Use:   "run --project <dir> --script <res://probe.gd>",
		Short: "Run a standalone GDScript probe without an editor or daemon (engine --script)",
		Long: `script run executes a SceneTree/MainLoop script directly with the engine
binary — no editor session, no daemon, no editor window. It waits for the
process to exit and reports exit_code + captured stdout/stderr.

Binary resolution is the same as launch: --godot > GODOT_BIN > "godot use"
default > PATH > conventional install dirs. On Windows the sibling
<name>_console.exe is preferred when present (the GUI-subsystem binary's
stdout cannot be captured).

注意：裸 --script 启动不会导入资源，不会在 --project 目录生成 .godot/
导入缓存（Godot 4.7.2 实测；早期版本的该说法已作废）。

Examples:
  godot-ai-cli script run --project /tmp/probe_proj --script res://probe.gd
  godot-ai-cli script run --project . --script res://tools/verify.gd --timeout 60`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// 工程目录必须是真实 Godot 工程（含 project.godot）。
			abs, err := filepath.Abs(project)
			if err != nil || !fileExists(filepath.Join(abs, "project.godot")) {
				return jsonError(cmd, "INVALID_PROJECT",
					fmt.Sprintf("%q is not a Godot project (project.godot missing)", project), nil)
			}
			bin, err := godot.Find(godotFlag)
			if err != nil {
				return jsonError(cmd, "GODOT_NOT_FOUND", err.Error(), nil)
			}
			bin = preferConsoleVariant(bin)

			args := []string{}
			if !windowed {
				args = append(args, "--headless")
			}
			args = append(args, "--path", abs, "--script", script)

			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
			defer cancel()
			proc := exec.CommandContext(ctx, bin, args...)
			var stdout, stderr bytes.Buffer
			proc.Stdout = &stdout
			proc.Stderr = &stderr

			start := time.Now()
			runErr := proc.Run()
			durationMs := time.Since(start).Milliseconds()

			if ctx.Err() == context.DeadlineExceeded {
				// 超时：kill 由 CommandContext 完成；把已捕获输出一并返回。
				return jsonError(cmd, "SCRIPT_TIMEOUT",
					fmt.Sprintf("script did not exit within %ds (killed)", timeoutSec),
					map[string]any{
						"exit_code": nil,
						"stdout":    stdout.String(),
						"stderr":    stderr.String(),
					})
			}
			exitCode := 0
			if runErr != nil {
				var exitErr *exec.ExitError
				if errors.As(runErr, &exitErr) {
					exitCode = exitErr.ExitCode()
				} else {
					// 进程根本没能启动（权限/损坏等）。
					return jsonError(cmd, "SCRIPT_RUN_FAILED", runErr.Error(), nil)
				}
			}
			return printJSON(cmd.OutOrStdout(), map[string]any{
				"project":     abs,
				"script":      script,
				"headless":    !windowed,
				"godot":       bin,
				"exit_code":   exitCode,
				"stdout":      stdout.String(),
				"stderr":      stderr.String(),
				"duration_ms": durationMs,
			}, prettyOutput)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Godot project dir (required)")
	cmd.Flags().StringVar(&script, "script", "", "script path, res:// or disk path (required)")
	cmd.Flags().StringVar(&godotFlag, "godot", "", "Godot binary path (default: GODOT_BIN / godot use default / PATH / conventional dirs)")
	cmd.Flags().BoolVar(&windowed, "windowed", false, "run windowed instead of --headless (stdout capture still works with the console build)")
	cmd.Flags().IntVar(&timeoutSec, "timeout", 30, "kill the script after N seconds (partial output is returned)")
	_ = cmd.MarkFlagRequired("project")
	_ = cmd.MarkFlagRequired("script")
	return cmd
}

// preferConsoleVariant：Windows 上 GUI 子系统的 Godot 二进制的 stdout 抓不到，
// 若旁边存在 <name>_console.exe 则优先用它（mono/非 mono 命名都覆盖）。
func preferConsoleVariant(bin string) string {
	if runtime.GOOS != "windows" || !strings.HasSuffix(strings.ToLower(bin), ".exe") {
		return bin
	}
	console := strings.TrimSuffix(bin, filepath.Ext(bin)) + "_console.exe"
	if fileExists(console) {
		return console
	}
	return bin
}
